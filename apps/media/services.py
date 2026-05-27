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
from .processing import extension_for, validate_and_strip
from .scanning import get_scanner
from .storage import get_storage

_SIGNING_SALT = "media.signed_url"


class MediaError(Exception):
    """Base for expected media errors."""


class NotAuthorized(MediaError):
    """Uploader/viewer lacks permission for this photo or thread."""


class MediaRejected(MediaError):
    """Upload failed safety scanning and was not stored."""


def _is_member(user, thread) -> bool:
    return current_members(thread.activity).filter(user=user).exists()


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
    )
    digest = hashlib.sha256(clean_bytes).hexdigest()
    result = get_scanner().scan(clean_bytes)

    if not result.clean:
        # Record for moderation/audit but never store or surface blocked content.
        record_audit("media.blocked", actor=uploader, reason="scan_match", sha256=digest)
        raise MediaRejected("Image failed safety screening and was not stored.")

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
