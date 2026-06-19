"""Media domain logic: the upload pipeline (validate → strip metadata → scan → store),
membership/cohort-scoped visibility, and signed, expiring URLs."""

import hashlib
import logging
import re
import uuid

from django.conf import settings
from django.core import signing
from django.db import transaction

from apps.safety.services import is_blocked, record_audit
from apps.social.services import current_members

from .models import Attachment, Photo
from .processing import DEFAULT_MAX_PIXELS, extension_for, validate_and_strip
from .scanning import get_scanner
from .storage import get_storage

_SIGNING_SALT = "media.signed_url"
_ATTACH_SIGNING_SALT = "media.attachment_url"
PDF_MAGIC = b"%PDF-"
logger = logging.getLogger(__name__)


class MediaError(Exception):
    """Base for expected media errors."""


class NotAuthorized(MediaError):
    """Uploader/viewer lacks permission for this photo or thread."""


class MediaRejected(MediaError):
    """Upload failed safety scanning and was not stored."""


class DuplicateProfileImage(MediaError):
    """A profile picture whose content duplicates another user's profile picture."""


def _is_member(user, thread) -> bool:
    return current_members(thread.activity).filter(user=user).exists()


def profile_image_is_taken(uploader, content_digest: str, perceptual: str = "") -> bool:
    """Whether another user IN THE SAME COHORT already uses this profile picture.

    "Unique" now means (W8): byte-identical stored content (``Photo.sha256`` of the
    post-EXIF-strip re-encode) OR a perceptual near-duplicate — the new picture's 64-bit
    dHash within MEDIA_PERCEPTUAL_MAX_DISTANCE bits of an existing avatar's ``phash``.
    A resize/re-encode/small-crop of someone else's avatar no longer slips through.
    This stays the single seam for the "unique" definition: callers and tests go
    through it, so it can keep tightening here without touching the upload pipeline.

    Scoped to the uploader's own cohort so the duplicate boolean never crosses the cohort
    wall — an adult must not be able to probe whether a given image is a child's avatar
    (profile photos are only viewable within a cohort; see can_view_photo).

    Still best-effort and NOT impersonation-proof: dHash is defeated by heavy crops or
    deliberate perturbation, and legacy rows without a phash only match exactly.
    """
    if not content_digest:
        return False
    others = Photo.objects.filter(
        kind=Photo.Kind.PROFILE, uploader__cohort=uploader.cohort
    ).exclude(uploader=uploader)
    if others.filter(sha256=content_digest).exists():
        return True
    if perceptual:
        from .perceptual import DEFAULT_MAX_DISTANCE, hamming_hex

        max_distance = getattr(settings, "MEDIA_PERCEPTUAL_MAX_DISTANCE", DEFAULT_MAX_DISTANCE)
        # One row per same-cohort user (an account has at most one avatar), HARD-CAPPED
        # so this in-Python scan can never balloon at cohort scale — newest avatars are
        # the realistic impersonation targets, so they are checked first. Best-effort by
        # design (the docstring's honesty clause covers anything beyond the cap).
        cap = getattr(settings, "MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP", 10_000)
        candidates = others.exclude(phash="").order_by("-id").values_list("phash", flat=True)[:cap]
        for existing in candidates:
            if hamming_hex(perceptual, existing) <= max_distance:
                return True
    return False


@transaction.atomic
def upload_photo(uploader, kind, data: bytes, *, thread=None) -> Photo:
    if kind == Photo.Kind.THREAD:
        if thread is None:
            raise NotAuthorized("A thread is required for thread photos.")
        if not _is_member(uploader, thread):
            raise NotAuthorized("Only current members can post photos in this thread.")
    elif kind == Photo.Kind.PROFILE:
        thread = None
    else:
        raise MediaError("Unknown photo kind.")

    clean_bytes, fmt, (width, height) = validate_and_strip(
        data,
        max_bytes=settings.MEDIA_MAX_UPLOAD_BYTES,
        max_dimension=getattr(settings, "MEDIA_MAX_DIMENSION", None),
        max_pixels=getattr(settings, "MEDIA_MAX_IMAGE_PIXELS", DEFAULT_MAX_PIXELS),
        quality=getattr(settings, "MEDIA_IMAGE_QUALITY", 82),
        output_format=getattr(settings, "MEDIA_IMAGE_OUTPUT_FORMAT", "") or None,
    )
    scanner = get_scanner()
    # Fail closed on a children's platform: if no effective content scanner is configured
    # (e.g. the hash blocklist is empty), refuse the upload rather than store unscreened
    # imagery. Set MEDIA_REQUIRE_SCANNER=False only in dev/test.
    if getattr(settings, "MEDIA_REQUIRE_SCANNER", True) and not scanner.is_effective():
        record_audit("media.upload_blocked", actor=uploader, reason="no_scanner", kind=kind)
        raise MediaRejected(
            "Photo uploads are unavailable until a content safety scanner is configured."
        )
    # Scan the ORIGINAL uploaded bytes — that is what a CSAM hash set / managed service
    # matches. Hashing the metadata-stripped re-encode (clean_bytes) would never match a
    # known-bad source file, so the original is both scanned and used as the match digest.
    orig_digest = hashlib.sha256(data).hexdigest()
    result = scanner.scan(data)
    if not result.clean:
        # Record for moderation/audit but never store or surface blocked content.
        record_audit("media.blocked", actor=uploader, reason="scan_match", sha256=orig_digest)
        raise MediaRejected("Image failed safety screening and was not stored.")

    digest = hashlib.sha256(clean_bytes).hexdigest()
    from .perceptual import dhash_hex

    # Data minimisation (review finding W8-0): the perceptual fingerprint exists ONLY
    # for the avatar-uniqueness rule, so it is computed and stored ONLY for PROFILE
    # pictures. A private in-thread photo never gets a stored fingerprint — storing one
    # would create an unused cross-thread correlation signal over private photos.
    fingerprint = (dhash_hex(clean_bytes) or "") if kind == Photo.Kind.PROFILE else ""

    # Profile pictures must be unique by content: refuse one byte-identical OR a
    # perceptual near-duplicate of another user's avatar (checked before storing so a
    # rejected duplicate leaves no orphan blob). See profile_image_is_taken.
    if kind == Photo.Kind.PROFILE and profile_image_is_taken(uploader, digest, fingerprint):
        # The reason is recorded for audit only. The user-facing message is deliberately
        # GENERIC (indistinguishable from other "bad image" rejections) so avatar upload
        # cannot be used as an oracle to confirm a specific image is in use as someone's
        # avatar — a presence-confirmation primitive we must not hand out on a child platform.
        record_audit("media.profile_duplicate_rejected", actor=uploader, sha256=digest)
        raise DuplicateProfileImage("Please choose a different image.")

    content_type = f"image/{extension_for(fmt)}"
    storage_key = f"{uuid.uuid4().hex}.{extension_for(fmt)}"
    get_storage().save(storage_key, clean_bytes, content_type=content_type)

    if kind == Photo.Kind.PROFILE:
        _replace_existing_profile(uploader)

    photo = Photo.objects.create(
        uploader=uploader,
        kind=kind,
        thread=thread,
        storage_key=storage_key,
        content_type=content_type,
        byte_size=len(clean_bytes),
        sha256=digest,
        phash=fingerprint,
        width=width,
        height=height,
        scan_status=Photo.ScanStatus.CLEAN,
        exif_stripped=True,
    )
    record_audit("media.uploaded", actor=uploader, kind=kind, photo_id=photo.id)
    return photo


def _replace_existing_profile(uploader) -> None:
    existing = Photo.objects.filter(uploader=uploader, kind=Photo.Kind.PROFILE).first()
    if existing:
        if existing.storage_key:
            get_storage().delete(existing.storage_key)
        existing.delete()


def thread_photos(viewer, thread):
    """Clean photos in a thread visible to `viewer` (must be a current member)."""
    if not _is_member(viewer, thread):
        raise NotAuthorized("Only current members can view this thread's photos.")
    return thread.photos.filter(scan_status=Photo.ScanStatus.CLEAN).order_by("-created_at")


@transaction.atomic
def delete_photo(actor, photo: Photo) -> None:
    """Delete a photo. Allowed for the uploader or staff (moderation removal)."""
    if actor.id != photo.uploader_id and not getattr(actor, "is_staff", False):
        raise NotAuthorized("Not allowed to delete this photo.")
    if photo.storage_key:
        get_storage().delete(photo.storage_key)
    record_audit("media.deleted", actor=actor, target=photo)
    photo.delete()


def can_view_photo(viewer, photo: Photo) -> bool:
    if photo.scan_status != Photo.ScanStatus.CLEAN:
        return False
    if viewer.id == photo.uploader_id:
        return True
    if is_blocked(viewer, photo.uploader):
        return False
    if photo.kind == Photo.Kind.THREAD:
        return _is_member(viewer, photo.thread)
    # Profile picture: the minimal avatar, scoped to the uploader's cohort.
    return viewer.cohort == photo.uploader.cohort


def signed_url(photo: Photo, viewer) -> str:
    if not can_view_photo(viewer, photo):
        raise NotAuthorized("Not allowed to view this photo.")
    token = signing.dumps({"photo_id": photo.id, "viewer_id": viewer.id}, salt=_SIGNING_SALT)
    return f"/api/media/file/{token}/"


def resolve_signed_token(token: str, viewer):
    """Validate an unexpired token and re-check the viewer can still see the photo."""
    try:
        payload = signing.loads(token, salt=_SIGNING_SALT, max_age=settings.MEDIA_SIGNED_URL_TTL)
    except signing.BadSignature as exc:
        raise NotAuthorized("Invalid or expired media link.") from exc
    if payload.get("viewer_id") != viewer.id:
        raise NotAuthorized("This media link was issued to a different user.")
    photo = Photo.objects.filter(id=payload["photo_id"]).first()
    if photo is None or not can_view_photo(viewer, photo):
        raise NotAuthorized("Not allowed to view this photo.")
    return photo


# --- Thread attachments (images + PDF in the activity conversation) -----------------------
#
# Media lives IN the unified Post stream (apps/social), attached to the author's own message.
# Same fail-closed scan + EXIF-strip pipeline as Photos; PDFs (the only FILE type at launch)
# are stored as-is and served ONLY as a forced download so they can never execute in the page.
# No video. Images are allowed in any cohort's thread (members only); FILE (PDF) is gated to
# MEDIA_FILE_COHORTS (adults only at launch — "none for minors").


def _attachments_enabled() -> bool:
    return getattr(settings, "MEDIA_ATTACHMENTS_ENABLED", True)


def _file_cohorts() -> set:
    return set(getattr(settings, "MEDIA_FILE_COHORTS", ["adult"]))


def _attachment_max_bytes() -> int:
    return getattr(
        settings,
        "MEDIA_ATTACHMENT_MAX_BYTES",
        getattr(settings, "MEDIA_MAX_UPLOAD_BYTES", 5_000_000),
    )


def _sanitize_filename(name: str) -> str:
    """A safe display filename for a PDF: strip any path, keep a conservative charset, cap
    length, and force a .pdf suffix (the content is type-verified separately)."""
    base = (name or "").replace("\\", "/").split("/")[-1].strip()
    base = re.sub(r"[^A-Za-z0-9._ -]", "_", base)[:116]
    if not base:
        base = "document"
    if not base.lower().endswith(".pdf"):
        base = base.rsplit(".", 1)[0] + ".pdf"
    return base


@transaction.atomic
def _ephemeral_min_ttl_seconds(cohort) -> int:
    """The per-cohort floor a requested disappear-TTL is clamped UP to. Minors (child + teen) get
    the 24h floor; adults the shorter floor. This is the single safety seam for ephemeral media."""
    from apps.accounts.models import Cohort

    if cohort in (Cohort.CHILD, Cohort.TEEN):
        return getattr(settings, "MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS", 86400)
    return getattr(settings, "MEDIA_EPHEMERAL_MIN_TTL_SECONDS", 3600)


def _resolve_expiry(activity, ttl_seconds):
    """Translate a requested TTL into an absolute expires_at, clamped UP to the cohort floor.
    None ttl → permanent (None). A non-positive ttl is treated as 'no expiry' (permanent), so a
    bug/crafted 0 can never make an image vanish faster than the floor."""
    from django.utils import timezone

    if ttl_seconds is None:
        return None
    try:
        ttl = int(ttl_seconds)
    except (TypeError, ValueError):
        return None
    if ttl <= 0:
        return None
    ttl = max(ttl, _ephemeral_min_ttl_seconds(activity.cohort))
    return timezone.now() + timezone.timedelta(seconds=ttl)


def attach_to_post(uploader, post, *, filename: str, data: bytes, ttl_seconds=None) -> Attachment:
    """Attach a scanned image or PDF to the uploader's OWN thread Post. Fail-closed: if the
    scanner is ineffective or the content matches the blocklist, nothing is stored and the
    caller's transaction should roll back the Post too (so there is no post without its file).

    ``ttl_seconds`` makes it a "temporary picture": the blob stops serving at expiry and a purge
    job later reclaims it (hidden/reported content is exempt — evidence is kept). The TTL is
    clamped UP to the cohort floor (24h for minors), so it can never outrun a guardian/report."""
    from apps.social.services import can_read_thread

    if not _attachments_enabled():
        raise MediaRejected("File sharing is currently unavailable.")
    activity = post.thread.activity
    if uploader.id != post.author_id:
        raise NotAuthorized("You can only attach a file to your own message.")
    # The WRITE gate must mirror the READ gate (can_read_thread): current MEMBER + same cohort +
    # can_participate (consent/assurance) + not blocked-vs-owner + activity not hidden. Using the
    # full gate here (not a bare membership check) keeps the child-safety gate IN the service, so
    # a future DRF/socket caller can't let a cohort-drifted/consent-lapsed/blocked member attach.
    if not can_read_thread(uploader, activity):
        raise NotAuthorized("Only current members can share files in this thread.")
    if len(data) > _attachment_max_bytes():
        raise MediaRejected("File exceeds the size limit.")

    kind = Attachment.Kind.FILE if data[:5] == PDF_MAGIC else Attachment.Kind.IMAGE
    # FILE (PDF) is a new media type — gated to adults at launch (never for minors).
    if kind == Attachment.Kind.FILE and activity.cohort not in _file_cohorts():
        raise NotAuthorized("File sharing isn't available in this thread.")

    # Fail-closed safety scan on the ORIGINAL bytes (what a CSAM hash set matches), same as
    # the photo path — applies to images AND PDFs.
    scanner = get_scanner()
    if getattr(settings, "MEDIA_REQUIRE_SCANNER", True) and not scanner.is_effective():
        record_audit("media.attach_blocked", actor=uploader, reason="no_scanner", kind=kind)
        raise MediaRejected(
            "File sharing is unavailable until a content safety scanner is configured."
        )
    orig_digest = hashlib.sha256(data).hexdigest()
    if not scanner.scan(data).clean:
        record_audit("media.blocked", actor=uploader, reason="scan_match", sha256=orig_digest)
        raise MediaRejected("File failed safety screening and was not stored.")

    # W8: PDFs additionally pass through the document (antivirus) seam. Default is a
    # no-op until an operator wires clamd; MEDIA_REQUIRE_DOCUMENT_SCANNER=True makes it
    # fail-closed. Forced-download + nosniff serving stays regardless of scan result.
    if kind == Attachment.Kind.FILE:
        from .docscan import get_document_scanner

        doc_scanner = get_document_scanner()
        if (
            getattr(settings, "MEDIA_REQUIRE_DOCUMENT_SCANNER", False)
            and not doc_scanner.is_effective()
        ):
            record_audit("media.attach_blocked", actor=uploader, reason="no_doc_scanner")
            raise MediaRejected(
                "File sharing is unavailable until a document scanner is configured."
            )
        if doc_scanner.is_effective() and not doc_scanner.scan(data).clean:
            record_audit(
                "media.blocked", actor=uploader, reason="doc_scan_match", sha256=orig_digest
            )
            raise MediaRejected("File failed safety screening and was not stored.")

    if kind == Attachment.Kind.IMAGE:
        from .processing import ImageError

        try:
            clean_bytes, fmt, (width, height) = validate_and_strip(
                data,
                max_bytes=_attachment_max_bytes(),
                max_dimension=getattr(settings, "MEDIA_MAX_DIMENSION", None),
                max_pixels=getattr(settings, "MEDIA_MAX_IMAGE_PIXELS", DEFAULT_MAX_PIXELS),
                quality=getattr(settings, "MEDIA_IMAGE_QUALITY", 82),
                output_format=getattr(settings, "MEDIA_IMAGE_OUTPUT_FORMAT", "") or None,
            )
        except ImageError as exc:
            # A non-image / non-PDF (or an unreadable/oversized image) — clean rejection, not a 500.
            raise MediaRejected("Only images (PNG/JPEG/WEBP) and PDF files can be shared.") from exc
        ext = extension_for(fmt)
        content_type = f"image/{ext}"
        display_name = ""
        width_h = (width, height)
        exif = True
    else:  # FILE / PDF — stored as-is (no re-encode), only ever served as a download
        clean_bytes = data
        ext = "pdf"
        content_type = "application/pdf"
        display_name = _sanitize_filename(filename)
        width_h = (0, 0)
        exif = False

    expires_at = _resolve_expiry(activity, ttl_seconds)
    storage_key = f"{uuid.uuid4().hex}.{ext}"
    get_storage().save(storage_key, clean_bytes, content_type=content_type)
    att = Attachment.objects.create(
        post=post,
        uploader=uploader,
        kind=kind,
        storage_key=storage_key,
        content_type=content_type,
        byte_size=len(clean_bytes),
        sha256=hashlib.sha256(clean_bytes).hexdigest(),
        original_filename=display_name,
        width=width_h[0],
        height=width_h[1],
        exif_stripped=exif,
        expires_at=expires_at,
    )
    record_audit(
        "media.attachment_uploaded",
        actor=uploader,
        kind=kind,
        attachment_id=att.id,
        expires_at=expires_at.isoformat() if expires_at else None,
    )
    return att


def can_view_attachment(viewer, attachment) -> bool:
    """Members of the post's activity thread may view its attachments. A hidden (removed/self-
    deleted) post's attachments are hidden to everyone but staff; blocked-vs-uploader hides."""
    from apps.social.services import can_read_thread

    if attachment.post.is_hidden and not getattr(viewer, "is_staff", False):
        return False
    if viewer.id != attachment.uploader_id and is_blocked(viewer, attachment.uploader):
        return False
    if getattr(viewer, "is_staff", False):
        return True
    return can_read_thread(viewer, attachment.post.thread.activity)


def _blob_retrievable(attachment, viewer) -> bool:
    """Whether ``viewer`` may still pull the bytes. A live (not expired, not purged) attachment is
    retrievable by any permitted viewer. An EXPIRED-but-not-yet-purged blob is additionally
    retrievable by STAFF only — a moderator must be able to fetch the evidence in the window between
    expiry and the purge job. A PURGED blob (bytes gone) is retrievable by no one."""
    if attachment.is_available():
        return True
    return (
        attachment.purged_at is None
        and bool(attachment.storage_key)
        and getattr(viewer, "is_staff", False)
    )


def attachment_signed_url(attachment, viewer) -> str:
    if not can_view_attachment(viewer, attachment):
        raise NotAuthorized("Not allowed to view this file.")
    if not _blob_retrievable(attachment, viewer):
        # A temporary picture that has expired (or been purged) stops serving immediately, even if
        # the caller still holds a freshly-minted token — expiry is the upper bound, not the TTL.
        # (Staff retain access to a not-yet-purged blob so moderation can still pull the evidence.)
        raise NotAuthorized("This file is no longer available.")
    token = signing.dumps(
        {"attachment_id": attachment.id, "viewer_id": viewer.id}, salt=_ATTACH_SIGNING_SALT
    )
    return f"/api/media/attachment/{token}/"


def resolve_attachment_token(token: str, viewer):
    try:
        payload = signing.loads(
            token, salt=_ATTACH_SIGNING_SALT, max_age=settings.MEDIA_SIGNED_URL_TTL
        )
    except signing.BadSignature as exc:
        raise NotAuthorized("Invalid or expired file link.") from exc
    if payload.get("viewer_id") != viewer.id:
        raise NotAuthorized("This file link was issued to a different user.")
    att = (
        Attachment.objects.filter(id=payload["attachment_id"])
        .select_related("post__thread__activity", "uploader")
        .first()
    )
    if att is None or not can_view_attachment(viewer, att):
        raise NotAuthorized("Not allowed to view this file.")
    if not _blob_retrievable(att, viewer):
        raise NotAuthorized("This file is no longer available.")
    return att


@transaction.atomic
def delete_attachment(actor, attachment) -> None:
    """Delete an attachment (uploader or staff). The blob is removed; the Post itself remains
    (the message may still carry text) — to remove the whole message use the post path."""
    if actor.id != attachment.uploader_id and not getattr(actor, "is_staff", False):
        raise NotAuthorized("Not allowed to delete this file.")
    if attachment.storage_key:
        get_storage().delete(attachment.storage_key)
    record_audit("media.attachment_deleted", actor=actor, target=attachment)
    attachment.delete()


def _under_moderation(*, post_ids, activity_ids, uploader_ids):
    """Return (reported_posts, reported_activities, reported_users): the ids with an UNRESOLVED
    report so the ephemeral purge can preserve evidence. A report can target the Post, its
    Activity, OR the uploading User — and the web report UI only ever emits user/activity targets,
    so matching the post alone would miss the dominant child-safety report (reporting the groomer
    or the activity). "Unresolved" excludes only DISMISSED: OPEN/REVIEWING hold during triage and
    ACTIONED holds through the DSA appeal window (a WARN/BAN substantiates the report but does NOT
    hide the image, so the bytes must stay)."""
    from django.contrib.auth import get_user_model
    from django.contrib.contenttypes.models import ContentType

    from apps.safety.models import Report
    from apps.social.models import Activity, Post

    def _hits(model, ids):
        if not ids:
            return set()
        ct = ContentType.objects.get_for_model(model)
        return set(
            Report.objects.filter(target_type=ct, target_id__in=list(ids))
            .exclude(status=Report.Status.DISMISSED)
            .values_list("target_id", flat=True)
        )

    return (
        _hits(Post, post_ids),
        _hits(Activity, activity_ids),
        _hits(get_user_model(), uploader_ids),
    )


def purge_expired_attachments(now=None) -> int:
    """Reclaim the blobs of expired temporary pictures. EXEMPT (never purged) — evidence is
    preserved for moderation/appeal/DSA/law: a hidden post, a hidden activity, OR any attachment
    whose post / containing activity / uploader is under an unresolved report. Each item is purged
    in its OWN transaction with a fresh row-locked re-check (so a report or hide that lands after
    the candidate snapshot still wins the race) and its own try/except (one bad blob is logged and
    skipped, retried next tick — it never starves the rest). Idempotent (skips already-purged
    rows). The row is RETAINED (only the bytes go) so the audit trail + sha256 survive. Returns the
    number of blobs reclaimed."""
    from django.utils import timezone

    from apps.social.models import Post

    now = now or timezone.now()
    candidates = list(
        Attachment.objects.filter(
            expires_at__isnull=False, expires_at__lte=now, purged_at__isnull=True
        ).select_related("post__thread__activity")
    )
    if not candidates:
        return 0
    # Cheap pre-filter from a snapshot so the common (non-exempt) case avoids a row lock; the
    # authoritative check is re-run under the lock below to close the report-after-snapshot race.
    reported_posts, reported_acts, reported_users = _under_moderation(
        post_ids={a.post_id for a in candidates},
        activity_ids={a.post.thread.activity_id for a in candidates},
        uploader_ids={a.uploader_id for a in candidates},
    )
    storage = get_storage()
    purged = 0
    for att in candidates:
        activity = att.post.thread.activity
        if (
            att.post.is_hidden
            or activity.is_hidden
            or att.post_id in reported_posts
            or activity.id in reported_acts
            or att.uploader_id in reported_users
        ):
            continue  # preserve evidence — reconsidered on the next run
        try:
            with transaction.atomic():
                # Re-check under a row lock against FRESH state: a report filed or a hide applied
                # since the snapshot must be honoured (evidence preservation can't lose the race).
                # Lock ONLY the Post row (of=("self",)): Thread.activity is nullable (group threads
                # have no activity), so select_related makes that join a LEFT OUTER JOIN, and an
                # unscoped FOR UPDATE can't lock the nullable side. We only re-READ the activity's
                # is_hidden here, never lock it, so locking just the Post is correct.
                locked = (
                    Post.objects.select_for_update(of=("self",))
                    .select_related("thread__activity")
                    .get(pk=att.post_id)
                )
                fresh_p, fresh_a, fresh_u = _under_moderation(
                    post_ids=[locked.id],
                    activity_ids=[locked.thread.activity_id],
                    uploader_ids=[att.uploader_id],
                )
                if (
                    locked.is_hidden
                    or locked.thread.activity.is_hidden
                    or fresh_p
                    or fresh_a
                    or fresh_u
                ):
                    continue
                key = att.storage_key
                att.purged_at = now
                att.storage_key = ""  # so it is never re-served or re-deleted
                att.save(update_fields=["purged_at", "storage_key"])
                record_audit("media.attachment_purged", actor=None, target=att, reason="expired")
                # Delete the bytes LAST: if it throws, the atomic rolls back the row changes too, so
                # we never end up "row says purged, blob still present" or vice-versa.
                if key:
                    storage.delete(key)
            purged += 1
        except Exception:
            # One unreclaimable blob (storage hiccup) must not abort the whole sweep; it stays
            # not-purged and is retried on the next tick.
            logger.exception("purge_expired_attachments: could not reclaim attachment %s", att.id)
            continue
    return purged


def attachments_for_posts(posts, viewer):
    """Map post_id -> [attachment, ...] (with a per-viewer signed `url`) for a batch of posts,
    so the thread template renders inline media without an N+1. Skips anything the viewer can't
    see. Bounded by the caller's already-bounded post window."""
    by_post: dict = {}
    post_ids = [p.id for p in posts]
    if not post_ids:
        return by_post
    qs = (
        Attachment.objects.filter(post_id__in=post_ids)
        .select_related("post__thread__activity", "uploader")
        .order_by("created_at")
    )
    for att in qs:
        if not can_view_attachment(viewer, att):
            continue
        if _blob_retrievable(att, viewer):
            # Live for a member; for STAFF this also covers an expired-not-purged blob (evidence).
            att.url = attachment_signed_url(att, viewer)
            att.expired = False
        else:
            # Keep an honest placeholder in the stream (a temporary picture that has disappeared)
            # rather than a broken image — no URL is issued for a gone blob.
            att.url = ""
            att.expired = True
        by_post.setdefault(att.post_id, []).append(att)
    return by_post
