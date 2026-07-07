"""Storage-blob lifecycle signals.

Photo/Attachment rows cascade-delete when their uploader (or thread/post) is deleted — e.g. on a
GDPR Art. 17 account erasure. Django's cascade only removes the DB rows, so without this the
image/file *bytes* would orphan in object storage and a child's media could survive deletion.
These `pre_delete` receivers fire for every removal path (single delete, queryset delete, and
cascade) and durably enqueue backing-blob removal in the same database transaction.
"""

import logging

from django.db.models.signals import pre_delete
from django.dispatch import receiver

from apps.ops.tasks import enqueue

from .models import ActivityCover, Attachment, Photo

logger = logging.getLogger(__name__)


def _enqueue_blob_cleanup(key: str, *, model_name: str) -> None:
    try:
        enqueue(
            "erasure.blob_cleanup",
            {"blob_keys": [key]},
            dedup_key=f"blob:{key}",
        )
    except Exception:
        logger.exception(
            "Failed to enqueue media blob cleanup for %s during %s delete", key, model_name
        )
        raise


@receiver(pre_delete, sender=Photo, dispatch_uid="media_photo_delete_blob")
def delete_blob_on_photo_delete(sender, instance: Photo, **kwargs) -> None:
    """Remove the stored blob once the Photo row deletion commits.

    Storage deletion is not transactional, so deleting in ``pre_delete`` would break rollback:
    a restored row could point at already-removed bytes. ``on_commit`` keeps the DB row and blob
    lifecycle aligned while still covering cascades/queryset deletes.
    """
    if not instance.storage_key:
        return
    _enqueue_blob_cleanup(instance.storage_key, model_name="Photo")


@receiver(pre_delete, sender=Attachment, dispatch_uid="media_attachment_delete_blob")
def delete_blob_on_attachment_delete(sender, instance: Attachment, **kwargs) -> None:
    """Remove the stored blob once the Attachment row deletion commits.

    Storage deletion is idempotent, so this is safe when the blob was already reclaimed
    (e.g. expired temporary attachments clear ``storage_key`` before retaining the row).
    """
    if not instance.storage_key:
        return
    _enqueue_blob_cleanup(instance.storage_key, model_name="Attachment")


@receiver(pre_delete, sender=ActivityCover, dispatch_uid="media_activity_cover_delete_blob")
def delete_blob_on_activity_cover_delete(sender, instance: ActivityCover, **kwargs) -> None:
    """Remove the stored cover blob once the ActivityCover row deletion commits."""
    if not instance.storage_key:
        return
    _enqueue_blob_cleanup(instance.storage_key, model_name="ActivityCover")


@receiver(pre_delete, sender="places.PlaceCover", dispatch_uid="media_place_cover_delete_blob")
def delete_blob_on_place_cover_delete(sender, instance, **kwargs) -> None:
    """Remove the stored venue-cover blob once the PlaceCover row deletion commits (P6b:
    business uploads + cached Commons images both live in our object storage). Lazy sender
    string avoids a hard media -> places import at app-load time."""
    if not instance.storage_key:
        return
    _enqueue_blob_cleanup(instance.storage_key, model_name="PlaceCover")
