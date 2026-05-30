"""Media domain logic: the upload pipeline (validate → strip metadata → scan → store),
membership/cohort-scoped visibility, and signed, expiring URLs."""

import hashlib
import uuid

from django.conf import settings
from django.core import signing
from django.db import transaction

from apps.safety.services import is_blocked, record_audit
from apps.social.services import current_members

from .models import Photo
from .processing import DEFAULT_MAX_PIXELS, extension_for, validate_and_strip
from .scanning import get_scanner
from .storage import get_storage

_SIGNING_SALT = "media.signed_url"


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


def profile_image_is_taken(uploader, content_digest: str) -> bool:
    """Whether another user IN THE SAME COHORT already uses a profile picture with this exact
    content.

    First-pass definition of "unique": byte-identical stored content — the post-EXIF-strip
    re-encode whose hash we keep in ``Photo.sha256`` (NOT the raw upload, so two images that
    differ only in stripped metadata still collide). This is the single seam for refining
    what "unique" means later (e.g. perceptual near-duplicate detection): callers and tests
    go through it, so the rule can change here without touching the upload pipeline.

    Scoped to the uploader's own cohort so the duplicate boolean never crosses the cohort
    wall — an adult must not be able to probe whether a given image is a child's avatar
    (profile photos are only viewable within a cohort; see can_view_photo).

    Guarantee is best-effort and exact-bytes only: it is NOT perceptual and NOT
    impersonation-proof — a re-encode, resize, or single-pixel change defeats it, and the
    digest is tied to the Pillow/codec version. Do not assume it prevents lookalike avatars.
    """
    if not content_digest:
        return False
    return (
        Photo.objects.filter(
            kind=Photo.Kind.PROFILE,
            sha256=content_digest,
            uploader__cohort=uploader.cohort,
        )
        .exclude(uploader=uploader)
        .exists()
    )


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

    # Profile pictures must be unique by content: refuse one byte-identical to another user's
    # avatar (checked before storing so a rejected duplicate leaves no orphan blob). See
    # profile_image_is_taken for the (extensible) definition of "unique".
    if kind == Photo.Kind.PROFILE and profile_image_is_taken(uploader, digest):
        # The reason is recorded for audit only. The user-facing message is deliberately
        # GENERIC (indistinguishable from other "bad image" rejections) so avatar upload
        # cannot be used as an oracle to confirm a specific image is in use as someone's
        # avatar — a presence-confirmation primitive we must not hand out on a child platform.
        record_audit("media.profile_duplicate_rejected", actor=uploader, sha256=digest)
        raise DuplicateProfileImage("Please choose a different image.")

    storage_key = f"{uuid.uuid4().hex}.{extension_for(fmt)}"
    get_storage().save(storage_key, clean_bytes)

    if kind == Photo.Kind.PROFILE:
        _replace_existing_profile(uploader)

    photo = Photo.objects.create(
        uploader=uploader,
        kind=kind,
        thread=thread,
        storage_key=storage_key,
        content_type=f"image/{extension_for(fmt)}",
        byte_size=len(clean_bytes),
        sha256=digest,
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
