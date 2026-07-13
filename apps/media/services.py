"""Media domain logic: the upload pipeline (validate → strip metadata → scan → store),
membership/cohort-scoped visibility, signed, expiring URLs, and the asynchronous
video-processing status machine (ADR-0026)."""

import hashlib
import logging
import os
import re
import shutil
import tempfile
import threading
import uuid
from datetime import timedelta

from django.conf import settings
from django.core import signing
from django.db import close_old_connections, transaction
from django.db.models import Q

from apps.safety.services import is_blocked, record_audit
from apps.social.services import current_members

from . import video
from .models import ActivityCover, Attachment, Photo
from .processing import (
    DEFAULT_MAX_PIXELS,
    ImageError,
    extension_for,
    make_thumbnail,
    validate_and_strip,
)
from .scanning import get_scanner
from .storage import get_storage

_SIGNING_SALT = "media.signed_url"
_ATTACH_SIGNING_SALT = "media.attachment_url"
_COVER_SIGNING_SALT = "media.activity_cover_url"
PDF_MAGIC = b"%PDF-"
logger = logging.getLogger(__name__)

# Matched-quality defaults per codec (ADR-0026): JPEG80 ≈ WebP80-82 ≈ AVIF64. Used when
# MEDIA_IMAGE_QUALITY is 0/unset ("auto"); an explicit setting wins for every codec.
_DEFAULT_IMAGE_QUALITY = {"AVIF": 64, "WEBP": 80, "JPEG": 80}


def _image_encode_params():
    """Resolve (output_format_or_None, quality) from settings, with per-codec auto quality."""
    fmt = (getattr(settings, "MEDIA_IMAGE_OUTPUT_FORMAT", "") or "").upper() or None
    quality = getattr(settings, "MEDIA_IMAGE_QUALITY", 0) or 0
    if not quality:
        quality = _DEFAULT_IMAGE_QUALITY.get(fmt or "", 80)
    return fmt, quality


def _store_thumbnail(clean_bytes: bytes, *, ext: str, content_type: str, quality: int) -> str:
    """Generate + store the one eager card/stream rendition (ADR-0026). Returns the storage
    key, or "" when the source is already small or the rendition fails (serving falls back to
    the full object — a thumbnail is an optimisation, never a gate)."""
    result = make_thumbnail(
        clean_bytes,
        max_dimension=getattr(settings, "MEDIA_THUMB_DIMENSION", 800),
        quality=quality,
    )
    if not result:
        return ""
    thumb_bytes, _size = result
    key = f"thumbs/{uuid.uuid4().hex}.{ext}"
    try:
        get_storage().save(key, thumb_bytes, content_type=content_type)
    except Exception:
        logger.exception("Could not store thumbnail rendition %s", key)
        return ""
    return key


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

    output_format, quality = _image_encode_params()
    clean_bytes, fmt, (width, height) = validate_and_strip(
        data,
        max_bytes=settings.MEDIA_MAX_UPLOAD_BYTES,
        max_dimension=getattr(settings, "MEDIA_MAX_DIMENSION", None),
        max_pixels=getattr(settings, "MEDIA_MAX_IMAGE_PIXELS", DEFAULT_MAX_PIXELS),
        quality=quality,
        output_format=output_format,
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
    thumb_key = _store_thumbnail(
        clean_bytes, ext=extension_for(fmt), content_type=content_type, quality=quality
    )

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
        thumb_storage_key=thumb_key,
        scan_status=Photo.ScanStatus.CLEAN,
        exif_stripped=True,
    )
    record_audit("media.uploaded", actor=uploader, kind=kind, photo_id=photo.id)
    return photo


def _replace_existing_profile(uploader) -> None:
    existing = Photo.objects.filter(uploader=uploader, kind=Photo.Kind.PROFILE).first()
    if existing:
        existing.delete()


def _delete_storage_key_after_commit(storage_key: str, *, model_name: str) -> None:
    def _delete() -> None:
        try:
            get_storage().delete(storage_key)
        except Exception:
            logger.exception(
                "Failed to delete media blob %s during %s replace", storage_key, model_name
            )

    transaction.on_commit(_delete)


def _viewer_id(viewer):
    if viewer is None or not getattr(viewer, "is_authenticated", False):
        return None
    return viewer.id


def _can_manage_activity_cover(user, activity) -> bool:
    user_id = _viewer_id(user)
    return bool(getattr(user, "is_staff", False) or user_id == activity.owner_id)


def _activity_accepts_cover_upload(activity) -> bool:
    from django.utils import timezone

    from apps.social.models import Activity

    return (
        activity.status == Activity.Status.OPEN
        and not activity.is_hidden
        and activity.starts_at > timezone.now()
    )


@transaction.atomic
def upload_activity_cover(uploader, activity, data: bytes, *, alt_text="") -> ActivityCover:
    """Create or replace an activity's contextual cover photo.

    Cover photos use the same fail-closed safety pipeline as profile/thread images.
    The activity itself remains optional for create_activity; cards fall back to a
    generated accent when no cover is present.
    """
    if not _can_manage_activity_cover(uploader, activity):
        raise NotAuthorized("Only the organizer or staff can manage this activity cover.")
    if not _activity_accepts_cover_upload(activity):
        raise MediaRejected("Covers can only be changed before an open, visible activity starts.")

    output_format, quality = _image_encode_params()
    clean_bytes, fmt, (width, height) = validate_and_strip(
        data,
        max_bytes=settings.MEDIA_MAX_UPLOAD_BYTES,
        max_dimension=getattr(settings, "MEDIA_MAX_DIMENSION", None),
        max_pixels=getattr(settings, "MEDIA_MAX_IMAGE_PIXELS", DEFAULT_MAX_PIXELS),
        quality=quality,
        output_format=output_format,
    )
    scanner = get_scanner()
    if getattr(settings, "MEDIA_REQUIRE_SCANNER", True) and not scanner.is_effective():
        record_audit("media.cover_upload_blocked", actor=uploader, reason="no_scanner")
        raise MediaRejected(
            "Cover uploads are unavailable until a content safety scanner is configured."
        )
    orig_digest = hashlib.sha256(data).hexdigest()
    result = scanner.scan(data)
    if not result.clean:
        record_audit("media.blocked", actor=uploader, reason="scan_match", sha256=orig_digest)
        raise MediaRejected("Image failed safety screening and was not stored.")

    ext = extension_for(fmt)
    content_type = f"image/{ext}"
    storage_key = f"activity-covers/{uuid.uuid4().hex}.{ext}"
    storage = get_storage()
    storage.save(storage_key, clean_bytes, content_type=content_type)
    thumb_key = _store_thumbnail(clean_bytes, ext=ext, content_type=content_type, quality=quality)
    try:
        existing = ActivityCover.objects.select_for_update().filter(activity=activity).first()
        if existing is None:
            cover = ActivityCover.objects.create(
                activity=activity,
                uploader=uploader,
                storage_key=storage_key,
                content_type=content_type,
                byte_size=len(clean_bytes),
                sha256=hashlib.sha256(clean_bytes).hexdigest(),
                width=width,
                height=height,
                thumb_storage_key=thumb_key,
                exif_stripped=True,
                alt_text=(alt_text or "").strip()[:140],
            )
        else:
            old_key = existing.storage_key
            old_thumb = existing.thumb_storage_key
            existing.uploader = uploader
            existing.storage_key = storage_key
            existing.content_type = content_type
            existing.byte_size = len(clean_bytes)
            existing.sha256 = hashlib.sha256(clean_bytes).hexdigest()
            existing.width = width
            existing.height = height
            existing.thumb_storage_key = thumb_key
            existing.exif_stripped = True
            existing.alt_text = (alt_text or "").strip()[:140]
            existing.save(
                update_fields=[
                    "uploader",
                    "storage_key",
                    "content_type",
                    "byte_size",
                    "sha256",
                    "width",
                    "height",
                    "thumb_storage_key",
                    "exif_stripped",
                    "alt_text",
                    "updated_at",
                ]
            )
            cover = existing
            if old_key and old_key != storage_key:
                _delete_storage_key_after_commit(old_key, model_name="ActivityCover")
            if old_thumb and old_thumb != thumb_key:
                _delete_storage_key_after_commit(old_thumb, model_name="ActivityCover")
        record_audit(
            "media.activity_cover_uploaded", actor=uploader, target=activity, cover_id=cover.id
        )
        return cover
    except Exception:
        for key in (storage_key, thumb_key):
            if not key:
                continue
            try:
                storage.delete(key)
            except Exception:
                logger.exception("Failed to clean up activity cover blob after DB error: %s", key)
        raise


@transaction.atomic
def delete_activity_cover(actor, cover) -> None:
    if not _can_manage_activity_cover(actor, cover.activity):
        raise NotAuthorized("Only the organizer or staff can delete this activity cover.")
    record_audit("media.activity_cover_deleted", actor=actor, target=cover.activity)
    cover.delete()


def _can_manage_place_cover(user, place):
    """staff, or the approved business claimant (P6b) — returns (allowed, claim_or_None)."""
    from apps.places.services import approved_business_claim_for

    if getattr(user, "is_staff", False):
        return True, approved_business_claim_for(user, place)
    claim = approved_business_claim_for(user, place)
    return claim is not None, claim


@transaction.atomic
def upload_place_cover(uploader, place, data: bytes, *, alt_text=""):
    """P6b (ADR-0019 §2 lane 2): a verified business claimant (or staff) uploads the ONE
    official venue image, through the exact fail-closed pipeline as every other upload
    (validate → EXIF strip → scan → store). Replaces any existing cover (a business image
    outranks the cached Commons one); the idempotent resolver never overwrites an existing
    cover, so a BUSINESS cover is stable across enrichment re-runs. Goes live immediately
    post-scan — claim approval already put a human in the loop; staff recovery is the
    PlaceCover admin. NOT a child-safety signal: imagery never feeds venue approval."""
    from apps.places.models import PlaceCover
    from apps.places.services import public_places

    allowed, claim = _can_manage_place_cover(uploader, place)
    if not allowed:
        raise NotAuthorized("Only the venue's approved business claimant can manage its image.")
    if not public_places().filter(pk=place.pk).exists():
        raise MediaRejected("Only a public venue can carry an official image.")

    output_format, quality = _image_encode_params()
    clean_bytes, fmt, (width, height) = validate_and_strip(
        data,
        max_bytes=settings.MEDIA_MAX_UPLOAD_BYTES,
        max_dimension=getattr(settings, "MEDIA_MAX_DIMENSION", None),
        max_pixels=getattr(settings, "MEDIA_MAX_IMAGE_PIXELS", DEFAULT_MAX_PIXELS),
        quality=quality,
        output_format=output_format,
    )
    scanner = get_scanner()
    if getattr(settings, "MEDIA_REQUIRE_SCANNER", True) and not scanner.is_effective():
        record_audit("media.cover_upload_blocked", actor=uploader, reason="no_scanner")
        raise MediaRejected(
            "Cover uploads are unavailable until a content safety scanner is configured."
        )
    orig_digest = hashlib.sha256(data).hexdigest()
    result = scanner.scan(data)
    if not result.clean:
        record_audit("media.blocked", actor=uploader, reason="scan_match", sha256=orig_digest)
        raise MediaRejected("Image failed safety screening and was not stored.")

    partner = claim.partner if claim is not None else None
    if partner is None:
        from apps.places.services import official_partner_for_place

        partner = official_partner_for_place(place)
    attribution = f"Official image: {partner.name}" if partner else "Official image"
    source_page_url = (partner.website if partner else "") or place.website or ""

    ext = extension_for(fmt)
    content_type = f"image/{ext}"
    storage_key = f"place-covers/{uuid.uuid4().hex}.{ext}"
    storage = get_storage()
    storage.save(storage_key, clean_bytes, content_type=content_type)
    try:
        existing = PlaceCover.objects.select_for_update().filter(place=place).first()
        fields = {
            "source": PlaceCover.Source.BUSINESS,
            "uploaded_by": uploader,
            "storage_key": storage_key,
            "content_type": content_type,
            "byte_size": len(clean_bytes),
            "sha256": hashlib.sha256(clean_bytes).hexdigest(),
            "width": width,
            "height": height,
            "exif_stripped": True,
            "attribution": attribution,
            "license_name": "Used with permission",
            "source_page_url": source_page_url,
            "alt_text": (alt_text or "").strip()[:140],
        }
        if existing is None:
            cover = PlaceCover.objects.create(place=place, **fields)
        else:
            old_key = existing.storage_key
            for name, value in fields.items():
                setattr(existing, name, value)
            existing.save(update_fields=[*fields.keys(), "updated_at"])
            cover = existing
            if old_key and old_key != storage_key:
                _delete_storage_key_after_commit(old_key, model_name="PlaceCover")
        record_audit("media.place_cover_uploaded", actor=uploader, target=place, cover_id=cover.id)
        return cover
    except Exception:
        try:
            storage.delete(storage_key)
        except Exception:
            logger.exception("Failed to clean up place cover blob after DB error: %s", storage_key)
        raise


@transaction.atomic
def delete_place_cover(actor, cover) -> None:
    """Remove a venue's official image (claimant retracts it, or staff recovery). The
    pre_delete signal reclaims the blob after commit, like every other media model."""
    allowed, _claim = _can_manage_place_cover(actor, cover.place)
    if not allowed:
        raise NotAuthorized("Only the venue's approved business claimant can manage its image.")
    record_audit("media.place_cover_deleted", actor=actor, target=cover.place)
    cover.delete()


def _public_can_view_cover(cover) -> bool:
    from apps.social.services import public_activities

    return public_activities().filter(pk=cover.activity_id).exists()


def can_view_activity_cover(viewer, cover) -> bool:
    if not cover or not cover.storage_key:
        return False
    if getattr(viewer, "is_staff", False):
        return True
    viewer_id = _viewer_id(viewer)
    if viewer_id is None:
        return _public_can_view_cover(cover)

    from apps.social.services import visible_activities

    return visible_activities(viewer).filter(pk=cover.activity_id).exists()


def activity_cover_signed_url(cover, viewer=None, *, variant: str = "full") -> str:
    if not can_view_activity_cover(viewer, cover):
        raise NotAuthorized("Not allowed to view this activity cover.")
    viewer_id = _viewer_id(viewer)
    payload = {"cover_id": cover.id}
    if viewer_id is None:
        payload["public"] = True
    else:
        payload["viewer_id"] = viewer_id
    if variant == "thumb":
        payload["v"] = "t"
    token = signing.dumps(payload, salt=_COVER_SIGNING_SALT)
    return f"/api/media/activity-cover-file/{token}/"


def resolve_activity_cover_token(token: str, viewer=None):
    """Returns ``(cover, variant)`` where variant is "full" or "thumb"."""
    try:
        payload = signing.loads(
            token, salt=_COVER_SIGNING_SALT, max_age=settings.MEDIA_SIGNED_URL_TTL
        )
    except signing.BadSignature as exc:
        raise NotAuthorized("Invalid or expired activity cover link.") from exc
    variant = "thumb" if payload.get("v") == "t" else "full"
    cover = (
        ActivityCover.objects.filter(id=payload.get("cover_id"))
        .select_related("activity", "activity__owner", "uploader")
        .first()
    )
    if cover is None:
        raise NotAuthorized("Not allowed to view this activity cover.")
    if payload.get("public") is True:
        if not _public_can_view_cover(cover):
            raise NotAuthorized("Not allowed to view this activity cover.")
        return cover, variant
    viewer_id = _viewer_id(viewer)
    if viewer_id is None or payload.get("viewer_id") != viewer_id:
        raise NotAuthorized("This activity cover link was issued to a different user.")
    if not can_view_activity_cover(viewer, cover):
        raise NotAuthorized("Not allowed to view this activity cover.")
    return cover, variant


def activity_visual(activity, viewer=None) -> dict:
    """The card/list visual. Serves the thumb rendition (ADR-0026) — cards are the hottest
    media surface and ~800px covers a card at 2× DPR; rows without a rendition fall back to
    the full object at serve time."""
    try:
        cover = activity.cover
    except ActivityCover.DoesNotExist:
        cover = None
    if cover is not None and can_view_activity_cover(viewer, cover):
        return {
            "kind": "activity_cover_photo",
            "url": activity_cover_signed_url(cover, viewer, variant="thumb"),
            "alt": cover.alt_text or activity.title,
        }
    return {"kind": "generated_accent"}


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


def signed_url(photo: Photo, viewer, *, variant: str = "full") -> str:
    """``variant="thumb"`` links the card/stream rendition (falls back to the full object at
    serve time when a row predates renditions or the source was already small)."""
    if not can_view_photo(viewer, photo):
        raise NotAuthorized("Not allowed to view this photo.")
    payload = {"photo_id": photo.id, "viewer_id": viewer.id}
    if variant == "thumb":
        payload["v"] = "t"
    token = signing.dumps(payload, salt=_SIGNING_SALT)
    return f"/api/media/file/{token}/"


def resolve_signed_token(token: str, viewer):
    """Validate an unexpired token and re-check the viewer can still see the photo.
    Returns ``(photo, variant)`` where variant is "full" or "thumb"."""
    try:
        payload = signing.loads(token, salt=_SIGNING_SALT, max_age=settings.MEDIA_SIGNED_URL_TTL)
    except signing.BadSignature as exc:
        raise NotAuthorized("Invalid or expired media link.") from exc
    if payload.get("viewer_id") != viewer.id:
        raise NotAuthorized("This media link was issued to a different user.")
    photo = Photo.objects.filter(id=payload["photo_id"]).first()
    if photo is None or not can_view_photo(viewer, photo):
        raise NotAuthorized("Not allowed to view this photo.")
    return photo, ("thumb" if payload.get("v") == "t" else "full")


def maybe_presigned_url(storage_key, *, content_type, download_name=None) -> str | None:
    """If MEDIA_REDIRECT_TO_PRESIGNED is on AND the backend can presign (S3), return a short-lived
    presigned GET URL so the object store serves the bytes directly (offloading the app process).
    Returns None to fall back to streaming through the app — the default, and always for the local
    filesystem backend. CALLER CONTRACT: the per-viewer access check MUST already have passed, so a
    URL is only minted for an authorized viewer. Its lifetime is the SHORT MEDIA_PRESIGNED_TTL (NOT
    the longer token TTL) — bounding the window in which a revocation/hide/ephemeral-expiry is not
    yet enforced (the redirect skips the streaming path's per-byte re-auth — see the settings note).
    ``download_name`` (PDF) pins a forced-download disposition so a direct fetch can't run."""
    if not getattr(settings, "MEDIA_REDIRECT_TO_PRESIGNED", False):
        return None
    disposition = f'attachment; filename="{download_name}"' if download_name else None
    return get_storage().presigned_get_url(
        storage_key,
        expires_in=getattr(settings, "MEDIA_PRESIGNED_TTL", 60),
        content_type=content_type,
        content_disposition=disposition,
    )


# --- Thread attachments (images + PDF + video in the activity conversation) ----------------
#
# Media lives IN the unified Post stream (apps/social), attached to the author's own message.
# Same fail-closed scan + EXIF-strip pipeline as Photos; PDFs are stored as-is and served ONLY
# as a forced download so they can never execute in the page. Images are allowed in any
# cohort's thread (members only); FILE (PDF) is gated to MEDIA_FILE_COHORTS and VIDEO
# (ADR-0026, default-off) to MEDIA_VIDEO_COHORTS (both adults-only at launch — "none for
# minors"). Video is admitted withheld (status=pending) and only becomes servable after the
# asynchronous transcode + frame scan succeeds.


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


def _video_enabled() -> bool:
    return getattr(settings, "MEDIA_VIDEO_ENABLED", False)


def _video_cohorts() -> set:
    return set(getattr(settings, "MEDIA_VIDEO_COHORTS", ["adult"]))


def _video_max_bytes() -> int:
    return getattr(settings, "MEDIA_VIDEO_MAX_UPLOAD_BYTES", 80 * 1024 * 1024)


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


def attach_to_post(
    uploader, post, *, filename: str, data: bytes | None = None, fileobj=None, ttl_seconds=None
) -> Attachment:
    """Attach a scanned image, PDF, or (ADR-0026) video to the uploader's OWN thread Post.
    Fail-closed: if the scanner is ineffective or the content matches the blocklist, nothing is
    stored and the caller's transaction should roll back the Post too (so there is no post
    without its file).

    ``fileobj`` (an uploaded file object) is preferred for large payloads: a video is sniffed
    from its head and streamed to storage without ever buffering the whole file in memory;
    anything else is read into ``data`` and follows the classic image/PDF path.

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

    if fileobj is not None and data is None:
        head = fileobj.read(16)
        fileobj.seek(0)
        if video.looks_like_video(head):
            return _attach_video_to_post(
                uploader, post, activity, fileobj=fileobj, ttl_seconds=ttl_seconds
            )
        data = fileobj.read()

    if data is None:
        raise MediaError("attach_to_post needs data or fileobj.")
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

    thumb_key = ""
    if kind == Attachment.Kind.IMAGE:
        output_format, quality = _image_encode_params()
        try:
            clean_bytes, fmt, (width, height) = validate_and_strip(
                data,
                max_bytes=_attachment_max_bytes(),
                max_dimension=getattr(settings, "MEDIA_MAX_DIMENSION", None),
                max_pixels=getattr(settings, "MEDIA_MAX_IMAGE_PIXELS", DEFAULT_MAX_PIXELS),
                quality=quality,
                output_format=output_format,
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
    if kind == Attachment.Kind.IMAGE:
        thumb_key = _store_thumbnail(
            clean_bytes, ext=ext, content_type=content_type, quality=quality
        )
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
        thumb_storage_key=thumb_key,
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


# --- Video attachments: withheld admission + asynchronous processing (ADR-0026) ------------
#
# Admission is synchronous and fail-closed (cohort gate, size cap, streamed-SHA-256 scan of the
# ORIGINAL bytes) but the row is created WITHHELD (status=pending, no storage_key) — per
# docs/ASYNC_TASKS.md, deferral only moves work that is already authorised, never a safety
# gate, so an unprocessed video is structurally unservable. The row's own status machine is
# the work queue: `transcode_videos` (management command / inline kick) claims pending rows
# with select_for_update(skip_locked=True) in a SHORT transaction, then runs ffprobe/ffmpeg on
# scratch disk OUTSIDE any transaction (the DeferredTask queue holds its claim transaction
# open across handlers — unusable for multi-minute CPU work on a 4-connection pool), then
# finalises in a second short transaction. A crashed worker leaves a stale `processing` row
# that a later run reclaims; attempts are capped.


def _attach_video_to_post(uploader, post, activity, *, fileobj, ttl_seconds=None) -> Attachment:
    if not _video_enabled():
        raise MediaRejected("Only images (PNG/JPEG/WEBP) and PDF files can be shared.")
    if activity.cohort not in _video_cohorts():
        raise NotAuthorized("Video sharing isn't available in this thread.")
    if not video.ffmpeg_available():
        record_audit("media.attach_blocked", actor=uploader, reason="no_ffmpeg", kind="video")
        raise MediaRejected("Video sharing is temporarily unavailable.")

    fileobj.seek(0, os.SEEK_END)
    size = fileobj.tell()
    fileobj.seek(0)
    if size > _video_max_bytes():
        raise MediaRejected("Video exceeds the size limit.")

    scanner = get_scanner()
    if getattr(settings, "MEDIA_REQUIRE_SCANNER", True) and not scanner.is_effective():
        record_audit("media.attach_blocked", actor=uploader, reason="no_scanner", kind="video")
        raise MediaRejected(
            "File sharing is unavailable until a content safety scanner is configured."
        )
    # Streamed SHA-256 of the ORIGINAL bytes (what a hash set matches) — the file is never
    # fully buffered in memory. The perceptual layer runs later, against sampled frames.
    hasher = hashlib.sha256()
    for chunk in iter(lambda: fileobj.read(1024 * 1024), b""):
        hasher.update(chunk)
    fileobj.seek(0)
    orig_digest = hasher.hexdigest()
    if not scanner.scan_digest(orig_digest).clean:
        record_audit("media.blocked", actor=uploader, reason="scan_match", sha256=orig_digest)
        raise MediaRejected("File failed safety screening and was not stored.")

    head = fileobj.read(16)
    fileobj.seek(0)
    source_ext = "webm" if head[:4] == b"\x1a\x45\xdf\xa3" else "mp4"
    source_key = f"video-src/{uuid.uuid4().hex}.{source_ext}"
    get_storage().save_fileobj(source_key, fileobj, content_type="application/octet-stream")

    expires_at = _resolve_expiry(activity, ttl_seconds)
    att = Attachment.objects.create(
        post=post,
        uploader=uploader,
        kind=Attachment.Kind.VIDEO,
        status=Attachment.Status.PENDING,
        storage_key="",  # withheld until the transcode + frame scan succeeds
        content_type="video/mp4",  # the (only) delivery format
        byte_size=size,
        sha256=orig_digest,
        source_storage_key=source_key,
        exif_stripped=False,  # becomes True when the metadata-stripping re-encode lands
        expires_at=expires_at,
    )
    record_audit(
        "media.attachment_uploaded",
        actor=uploader,
        kind="video",
        attachment_id=att.id,
        expires_at=expires_at.isoformat() if expires_at else None,
    )
    transaction.on_commit(_kick_video_processing)
    return att


# Single-flight guard for the inline kick (review HIGH): each upload's on_commit would
# otherwise spawn its OWN transcode thread inside the one ASGI process — a handful of quick
# uploads could hold every pooled DB connection (DB_POOL_MAX_SIZE=4 in prod) and stack ffmpeg
# processes on the 3-vCPU box. One kick thread at a time per process; everything it doesn't
# drain is picked up by the next kick or the transcode_videos timer.
_INLINE_KICK_RUNNING = threading.Lock()


def _kick_video_processing() -> None:
    """Best-effort near-real-time processing without new infrastructure: ONE daemon thread
    drains a few pending videos right after an upload commits. Crash-safe by design — if
    this thread (or the whole process) dies, the `transcode_videos` timer picks the row up."""
    if not getattr(settings, "MEDIA_VIDEO_INLINE_PROCESSING", True):
        return
    if not _INLINE_KICK_RUNNING.acquire(blocking=False):
        return  # a kick is already draining; the queue is shared, nothing is lost

    def _run():
        try:
            process_pending_videos(limit=3)
        except Exception:
            logger.exception("Inline video processing kick failed (timer will retry).")
        finally:
            _INLINE_KICK_RUNNING.release()
            close_old_connections()

    threading.Thread(target=_run, name="video-transcode-kick", daemon=True).start()


def process_pending_videos(limit: int | None = None) -> int:
    """Drain the withheld-video queue: claim → process on scratch disk → finalise. Returns
    the number of rows brought to a terminal-or-ready state this run. Never raises for a
    single bad item (it is finalised FAILED and the loop continues)."""
    from django.utils import timezone

    scanner = get_scanner()
    if getattr(settings, "MEDIA_REQUIRE_SCANNER", True) and not scanner.is_effective():
        # Fail closed and DON'T claim/burn attempts: rows stay pending until an effective
        # scanner is configured (the frame scan below is a safety gate, not an optimisation).
        logger.warning("process_pending_videos: no effective scanner; leaving videos pending.")
        return 0
    if not video.ffmpeg_available():
        logger.warning("process_pending_videos: ffmpeg/ffprobe not available on this host.")
        return 0

    processed = 0
    while limit is None or processed < limit:
        now = timezone.now()
        att = _claim_next_video(now)
        if att is None:
            break
        _process_one_video(att)
        processed += 1
    return processed


# A claimed-but-already-finalised marker so the drain loop keeps going without processing.
_sentinel_claimed = object()


def _claim_next_video(now):
    """Short-transaction claim: flip one due row to PROCESSING and commit. Also finalises
    rows whose attempts are exhausted (FAILED) so nothing sits in the queue forever."""
    stale_cutoff = now - timedelta(
        seconds=getattr(settings, "MEDIA_VIDEO_STALE_PROCESSING_SECONDS", 1800)
    )
    max_attempts = getattr(settings, "MEDIA_VIDEO_MAX_ATTEMPTS", 3)
    due = Q(status=Attachment.Status.PENDING) | Q(
        status=Attachment.Status.PROCESSING, processing_started_at__lt=stale_cutoff
    )
    with transaction.atomic():
        att = (
            Attachment.objects.select_for_update(skip_locked=True)
            .filter(kind=Attachment.Kind.VIDEO, purged_at__isnull=True)
            .filter(due)
            .order_by("created_at")
            .first()
        )
        if att is None:
            return None
        if att.processing_attempts >= max_attempts:
            _finalize_video_failed_locked(att, reason="attempts_exhausted")
            return _sentinel_claimed
        att.status = Attachment.Status.PROCESSING
        att.processing_started_at = now
        att.processing_attempts += 1
        att.save(update_fields=["status", "processing_started_at", "processing_attempts"])
        return att


def _process_one_video(att) -> None:
    if att is _sentinel_claimed:
        return
    # The claim transaction is committed; the minutes of ffmpeg work ahead must not pin a
    # pooled DB connection (prod pool is 4). Finalisers reopen one for their short commit.
    # Guarded: under test the whole call runs inside the test's transaction on the test's
    # connection — closing it there would wreck the harness, and there is no pool to protect.
    from django.db import connection

    if not connection.in_atomic_block:
        close_old_connections()
    scratch = tempfile.mkdtemp(prefix="video-transcode-")
    try:
        try:
            result = _transcode_and_scan(att, scratch)
        except (video.VideoError, ImageError) as exc:
            # Deterministic rejection (invalid/oversized/unsupported/corrupt content, or a
            # transcode this input will always fail) — no retry.
            _finalize_video_failed(att, reason=str(exc)[:200])
            return
        except Exception:
            # Transient (storage hiccup, OOM-kill of ffmpeg, ...): leave the row PROCESSING —
            # the stale-cutoff re-admits it on a LATER run, so a momentary condition can't
            # burn every attempt back-to-back within one drain loop (review finding).
            logger.exception("Video processing failed transiently for attachment %s", att.id)
            return
        if result == "blocked":
            return
    finally:
        shutil.rmtree(scratch, ignore_errors=True)


def _transcode_and_scan(att, scratch: str) -> str:
    """The off-transaction work: validate → transcode → poster → frame scan → store →
    finalise READY (or BLOCKED). Returns "ready"/"blocked". Raises for failures."""
    storage = get_storage()
    src = os.path.join(scratch, "src.bin")
    out = os.path.join(scratch, "out.mp4")
    storage.download_to(att.source_storage_key, src)

    probe_timeout = getattr(settings, "MEDIA_VIDEO_PROBE_TIMEOUT", 60)
    info = video.probe(src, timeout=probe_timeout)
    video.validate_probe(
        info,
        max_duration=getattr(settings, "MEDIA_VIDEO_MAX_DURATION_SECONDS", 90),
        max_side=getattr(settings, "MEDIA_VIDEO_MAX_SOURCE_SIDE", 3840),
    )

    video.transcode(
        src,
        out,
        max_side=getattr(settings, "MEDIA_VIDEO_TARGET_MAX_SIDE", 1280),
        max_duration=getattr(settings, "MEDIA_VIDEO_MAX_DURATION_SECONDS", 90),
        crf=getattr(settings, "MEDIA_VIDEO_CRF", 23),
        preset=getattr(settings, "MEDIA_VIDEO_PRESET", "medium"),
        audio_bitrate=getattr(settings, "MEDIA_VIDEO_AUDIO_BITRATE", "96k"),
        threads=getattr(settings, "MEDIA_VIDEO_THREADS", 2),
        timeout=getattr(settings, "MEDIA_VIDEO_FFMPEG_TIMEOUT", 600),
    )

    # Ground truth from the OUTPUT (dimensions may have been downscaled, duration -t capped).
    out_info = video.probe(out, timeout=probe_timeout)
    out_probe = video.validate_probe(
        out_info,
        max_duration=getattr(settings, "MEDIA_VIDEO_MAX_DURATION_SECONDS", 90) + 5,
        max_side=getattr(settings, "MEDIA_VIDEO_TARGET_MAX_SIDE", 1280),
    )

    # Poster from the transcoded output (inherits the strip + baked rotation), through the
    # ordinary image pipeline for the canonical re-encode.
    output_format, quality = _image_encode_params()
    poster_jpeg = video.extract_poster(out, duration=out_probe.duration, timeout=probe_timeout)
    poster_bytes, poster_fmt, _size = validate_and_strip(
        poster_jpeg,
        max_bytes=getattr(settings, "MEDIA_MAX_UPLOAD_BYTES", 5 * 1024 * 1024),
        max_dimension=getattr(settings, "MEDIA_THUMB_DIMENSION", 800),
        quality=quality,
        output_format=output_format or "WEBP",
    )

    # Fail-closed frame scan BEFORE anything is stored: the perceptual blocklist matches
    # known-bad imagery appearing inside the video (hash-blocklist-first — ADR-0004/0026).
    frames = video.sample_frames(
        out,
        scratch,
        interval_seconds=getattr(settings, "MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS", 5),
        max_frames=getattr(settings, "MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES", 25),
    )
    scanner = get_scanner()
    for frame in (poster_jpeg, *frames):
        if not scanner.scan(frame).clean:
            _finalize_video_blocked(att)
            return "blocked"

    # DETERMINISTIC per-attachment keys (review finding): a worker crash between these stores
    # and the finalise commit means the next attempt re-stores to the SAME keys — an orphaned
    # partial output can never accumulate per retry.
    poster_ext = extension_for(poster_fmt)
    poster_key = f"video-posters/{att.pk}-{att.sha256[:16]}.{poster_ext}"
    poster_content_type = f"image/{poster_ext}"
    out_key = f"videos/{att.pk}-{att.sha256[:16]}.mp4"
    storage = get_storage()
    with open(out, "rb") as fh:
        storage.save_fileobj(out_key, fh, content_type="video/mp4")
    storage.save(poster_key, poster_bytes, content_type=poster_content_type)

    _finalize_video_ready(
        att,
        out_key=out_key,
        out_size=os.path.getsize(out),
        poster_key=poster_key,
        poster_content_type=poster_content_type,
        width=out_probe.width,
        height=out_probe.height,
        duration=out_probe.duration,
    )
    return "ready"


def _broadcast_attachment_update_after_commit(att) -> None:
    """Tell connected thread members this post's attachments changed state (video became
    ready/failed/blocked) so the placeholder swaps live. After commit, so a rolled-back
    finalise broadcasts nothing; per-viewer URL resolution happens in each member's consumer."""

    def _send():
        try:
            from apps.social.services import broadcast_attachment_update

            broadcast_attachment_update(att.post)
        except Exception:
            logger.exception("Live attachment-update broadcast failed (reload still works).")

    transaction.on_commit(_send)


def _finalize_video_ready(
    att, *, out_key, out_size, poster_key, poster_content_type, width, height, duration
):
    with transaction.atomic():
        fresh = (
            Attachment.objects.select_for_update()
            .filter(pk=att.pk, kind=Attachment.Kind.VIDEO)
            .first()
        )
        if fresh is None:
            # Row vanished mid-transcode (post/account deleted): reclaim the fresh blobs.
            for key in (out_key, poster_key):
                _delete_storage_key_after_commit(key, model_name="Attachment")
            return
        source_key = fresh.source_storage_key
        fresh.storage_key = out_key
        fresh.byte_size = out_size
        fresh.poster_storage_key = poster_key
        fresh.poster_content_type = poster_content_type
        fresh.width = width
        fresh.height = height
        fresh.duration_seconds = int(round(duration))
        fresh.source_storage_key = ""
        fresh.exif_stripped = True  # the re-encode dropped every metadata atom
        fresh.status = Attachment.Status.READY
        fresh.save(
            update_fields=[
                "storage_key",
                "byte_size",
                "poster_storage_key",
                "poster_content_type",
                "width",
                "height",
                "duration_seconds",
                "source_storage_key",
                "exif_stripped",
                "status",
            ]
        )
        record_audit("media.video_ready", actor=None, target=fresh, attachment_id=fresh.id)
        if source_key:
            # The quarantined original still carries the source's metadata — delete on commit.
            _delete_storage_key_after_commit(source_key, model_name="Attachment")
        _broadcast_attachment_update_after_commit(fresh)


def _finalize_video_blocked(att) -> None:
    """Frame scan matched: never serve, retain the SOURCE bytes as moderation evidence
    (mirrors the expired-but-reported evidence posture), audit."""
    with transaction.atomic():
        fresh = Attachment.objects.select_for_update().filter(pk=att.pk).first()
        if fresh is None:
            return
        fresh.status = Attachment.Status.BLOCKED
        fresh.storage_key = ""
        fresh.save(update_fields=["status", "storage_key"])
        record_audit(
            "media.blocked",
            actor=None,
            target=fresh,
            reason="video_frame_scan",
            sha256=fresh.sha256,
        )
        _broadcast_attachment_update_after_commit(fresh)


def _finalize_video_failed(att, *, reason: str) -> None:
    with transaction.atomic():
        fresh = Attachment.objects.select_for_update().filter(pk=att.pk).first()
        if fresh is None:
            return
        _finalize_video_failed_locked(fresh, reason=reason)


def _finalize_video_failed_locked(fresh, *, reason: str) -> None:
    """Terminal failure (already under a row lock): reclaim every blob, keep the row as an
    honest 'couldn't be processed' placeholder."""
    source_key = fresh.source_storage_key
    fresh.status = Attachment.Status.FAILED
    fresh.storage_key = ""
    fresh.source_storage_key = ""
    fresh.save(update_fields=["status", "storage_key", "source_storage_key"])
    record_audit("media.video_failed", actor=None, target=fresh, reason=reason[:200])
    if source_key:
        _delete_storage_key_after_commit(source_key, model_name="Attachment")
    _broadcast_attachment_update_after_commit(fresh)


def can_view_attachment(viewer, attachment) -> bool:
    """Members of the post's activity thread may view its attachments. A hidden (removed/self-
    deleted) post's attachments are hidden to everyone but staff; blocked-vs-uploader hides.
    A safety-BLOCKED video is staff-only (moderation evidence) — members never even see a
    placeholder for it."""
    from apps.social.services import can_read_thread

    if attachment.post.is_hidden and not getattr(viewer, "is_staff", False):
        return False
    if attachment.status == Attachment.Status.BLOCKED and not getattr(viewer, "is_staff", False):
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


_ATTACHMENT_VARIANTS = {"m": "main", "t": "thumb", "p": "poster"}


def attachment_signed_url(attachment, viewer, *, variant: str = "main") -> str:
    """``variant``: "main" (the object), "thumb" (image rendition, falls back to main), or
    "poster" (a READY video's poster frame)."""
    if not can_view_attachment(viewer, attachment):
        raise NotAuthorized("Not allowed to view this file.")
    if not _blob_retrievable(attachment, viewer):
        # A temporary picture that has expired (or been purged) stops serving immediately, even if
        # the caller still holds a freshly-minted token — expiry is the upper bound, not the TTL.
        # (Staff retain access to a not-yet-purged blob so moderation can still pull the evidence.)
        raise NotAuthorized("This file is no longer available.")
    if variant == "poster" and not attachment.poster_storage_key:
        raise NotAuthorized("This file has no poster.")
    payload = {"attachment_id": attachment.id, "viewer_id": viewer.id}
    if variant != "main":
        payload["v"] = variant[0]
    token = signing.dumps(payload, salt=_ATTACH_SIGNING_SALT)
    return f"/api/media/attachment/{token}/"


def resolve_attachment_token(token: str, viewer):
    """Returns ``(attachment, variant)`` — variant is "main", "thumb", or "poster"."""
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
    variant = _ATTACHMENT_VARIANTS.get(payload.get("v", "m"), "main")
    if variant == "poster" and not att.poster_storage_key:
        raise NotAuthorized("This file has no poster.")
    return att, variant


@transaction.atomic
def delete_attachment(actor, attachment) -> None:
    """Delete an attachment (uploader or staff). The blob is removed; the Post itself remains
    (the message may still carry text) — to remove the whole message use the post path."""
    if actor.id != attachment.uploader_id and not getattr(actor, "is_staff", False):
        raise NotAuthorized("Not allowed to delete this file.")
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
        if att.status == Attachment.Status.BLOCKED:
            continue  # safety-blocked video: the retained source IS the evidence — never purge
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
                # ALSO lock + re-read the Attachment itself (review finding): the video
                # finalisers lock this row — never the snapshot — so purging from the stale
                # snapshot could destroy a just-BLOCKED row's retained evidence or clobber a
                # just-READY row's fresh keys. Under the row lock the finalisers are
                # serialised out; re-derive everything from fresh state.
                fresh_att = Attachment.objects.select_for_update().filter(pk=att.pk).first()
                if fresh_att is None or fresh_att.purged_at is not None:
                    continue
                if fresh_att.status == Attachment.Status.BLOCKED:
                    continue  # evidence — never purge
                if fresh_att.status == Attachment.Status.PROCESSING:
                    continue  # a worker is mid-flight; reconsidered next tick
                keys = [
                    k
                    for k in (
                        fresh_att.storage_key,
                        fresh_att.thumb_storage_key,
                        fresh_att.poster_storage_key,
                        fresh_att.source_storage_key,
                    )
                    if k
                ]
                fresh_att.purged_at = now
                # Clear every key so nothing is ever re-served or re-deleted.
                fresh_att.storage_key = ""
                fresh_att.thumb_storage_key = ""
                fresh_att.poster_storage_key = ""
                fresh_att.source_storage_key = ""
                fresh_att.save(
                    update_fields=[
                        "purged_at",
                        "storage_key",
                        "thumb_storage_key",
                        "poster_storage_key",
                        "source_storage_key",
                    ]
                )
                record_audit(
                    "media.attachment_purged", actor=None, target=fresh_att, reason="expired"
                )
                # Delete the bytes LAST: if it throws, the atomic rolls back the row changes too, so
                # we never end up "row says purged, blob still present" or vice-versa.
                for key in keys:
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
        # Only staff ever reach a BLOCKED row (can_view_attachment gates it) — render an
        # honest moderation placeholder, not the misleading "temporary picture" note. The
        # retained source is bucket-level evidence, deliberately not servable in-app.
        att.blocked = att.status == Attachment.Status.BLOCKED
        att.processing = att.kind == Attachment.Kind.VIDEO and att.status in (
            Attachment.Status.PENDING,
            Attachment.Status.PROCESSING,
        )
        att.failed = att.status == Attachment.Status.FAILED
        att.thumb_url = ""
        att.poster_url = ""
        if att.blocked:
            att.url = ""
            att.expired = False
        elif att.processing or att.failed:
            # An honest in-stream state, never bytes: the withheld/broken video renders as a
            # calm placeholder (ADR-0026 — unprocessed media is structurally unservable).
            att.url = ""
            att.expired = False
        elif _blob_retrievable(att, viewer):
            # Live for a member; for STAFF this also covers an expired-not-purged blob (evidence).
            att.url = attachment_signed_url(att, viewer)
            att.expired = False
            if att.kind == Attachment.Kind.IMAGE:
                # Streams render the rendition; the full object stays one click away.
                att.thumb_url = attachment_signed_url(att, viewer, variant="thumb")
            elif att.kind == Attachment.Kind.VIDEO and att.poster_storage_key:
                att.poster_url = attachment_signed_url(att, viewer, variant="poster")
        else:
            # Keep an honest placeholder in the stream (a temporary picture that has disappeared)
            # rather than a broken image — no URL is issued for a gone blob.
            att.url = ""
            att.expired = True
        by_post.setdefault(att.post_id, []).append(att)
    return by_post


# --- Place covers (ADR-0019 §2) -------------------------------------------------------
# Venue images are public content on public places: no viewer binding, but the place's
# public status is re-checked at BOTH issue and resolve time so a hidden/pending venue
# never leaks its image through an old link.

_PLACE_COVER_SIGNING_SALT = "media.place_cover_url"


def _place_is_public(place_id) -> bool:
    from apps.places.services import public_places

    return public_places().filter(pk=place_id).exists()


def place_cover_signed_url(cover) -> str | None:
    """A short-lived serving URL for a public place's cover, or None."""
    if not cover or not cover.storage_key or not _place_is_public(cover.place_id):
        return None
    token = signing.dumps({"place_cover_id": cover.id}, salt=_PLACE_COVER_SIGNING_SALT)
    return f"/api/media/place-cover-file/{token}/"


def resolve_place_cover_token(token: str):
    from apps.places.models import PlaceCover

    try:
        payload = signing.loads(
            token, salt=_PLACE_COVER_SIGNING_SALT, max_age=settings.MEDIA_SIGNED_URL_TTL
        )
    except signing.BadSignature as exc:
        raise NotAuthorized("Invalid or expired place cover link.") from exc
    cover = PlaceCover.objects.filter(id=payload.get("place_cover_id")).first()
    if cover is None or not _place_is_public(cover.place_id):
        raise NotAuthorized("Not allowed to view this place cover.")
    return cover
