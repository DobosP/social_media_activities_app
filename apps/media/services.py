import uuid

from django.conf import settings

from apps.safety.services import record_audit
from apps.social.models import Activity, Membership

from .imaging import InvalidImage, process_image
from .models import MediaImage
from .scanning import get_image_scanner
from .storage import get_storage_backend

_EXT = {"image/jpeg": "jpg", "image/png": "png", "image/webp": "webp"}


class MediaError(Exception):
    """An upload or access rule was violated."""


def _is_thread_member(user, thread) -> bool:
    activity = thread.activity
    if user.cohort != activity.cohort:
        return False
    return activity.memberships.filter(user=user, state=Membership.State.MEMBER).exists()


def _assert_thread_access(user, thread) -> None:
    if not _is_thread_member(user, thread):
        raise MediaError("You are not a member of this activity's thread.")


def upload_image(owner, *, kind, data: bytes, thread=None) -> MediaImage:
    max_bytes = getattr(settings, "MEDIA_MAX_BYTES", 5 * 1024 * 1024)
    if not data:
        raise MediaError("Empty upload.")
    if len(data) > max_bytes:
        raise MediaError(f"Image exceeds the {max_bytes} byte limit.")

    if kind == MediaImage.Kind.THREAD_PHOTO:
        if thread is None:
            raise MediaError("A thread is required for a thread photo.")
        _assert_thread_access(owner, thread)
        if thread.activity.status != Activity.Status.OPEN:
            raise MediaError("This activity is closed.")
    elif kind == MediaImage.Kind.PROFILE:
        thread = None
    else:
        raise MediaError(f"Unknown image kind: {kind}.")

    try:
        processed = process_image(data)
    except InvalidImage as exc:
        raise MediaError(str(exc)) from exc

    # Safety screening BEFORE the image is visible (CSAM hash-match seam).
    result = get_image_scanner().scan(data=processed.data, content_type=processed.content_type)
    if not result.allowed:
        rejected = MediaImage.objects.create(
            owner=owner,
            kind=kind,
            thread=thread,
            content_type=processed.content_type,
            byte_size=len(processed.data),
            width=processed.width,
            height=processed.height,
            status=MediaImage.Status.REJECTED,
            scan_reason=result.reason,
        )
        record_audit("media.rejected", actor=owner, target=rejected, reason=result.reason)
        raise MediaError(result.reason or "Image rejected by safety screening.")

    ext = _EXT.get(processed.content_type, "img")
    key = f"{kind}/{owner.public_id}/{uuid.uuid4().hex}.{ext}"
    get_storage_backend().save(key, processed.data, processed.content_type)

    return MediaImage.objects.create(
        owner=owner,
        kind=kind,
        thread=thread,
        storage_key=key,
        content_type=processed.content_type,
        byte_size=len(processed.data),
        width=processed.width,
        height=processed.height,
        status=MediaImage.Status.APPROVED,
    )


def set_profile_picture(owner, data: bytes) -> MediaImage:
    return upload_image(owner, kind=MediaImage.Kind.PROFILE, data=data)


def profile_picture(user) -> MediaImage | None:
    return (
        user.images.filter(kind=MediaImage.Kind.PROFILE, status=MediaImage.Status.APPROVED)
        .order_by("-created_at")
        .first()
    )


def thread_photos(viewer, thread) -> list[MediaImage]:
    _assert_thread_access(viewer, thread)
    return list(thread.photos.filter(status=MediaImage.Status.APPROVED).order_by("-created_at"))


def can_view(viewer, image: MediaImage) -> bool:
    if viewer == image.owner:
        return True
    if image.status != MediaImage.Status.APPROVED:
        return False
    if image.kind == MediaImage.Kind.PROFILE:
        # Conservative: profile pictures are visible only within the same cohort.
        return viewer.cohort == image.owner.cohort
    return image.thread is not None and _is_thread_member(viewer, image.thread)


def signed_url(viewer, image: MediaImage, *, expires_in: int = 300) -> str:
    if not can_view(viewer, image):
        raise MediaError("You are not allowed to view this image.")
    return get_storage_backend().signed_url(image.storage_key, expires_in=expires_in)
