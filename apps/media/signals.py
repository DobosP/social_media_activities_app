"""Storage-blob lifecycle signals.

Photo/Attachment rows cascade-delete when their uploader (or thread/post) is deleted — e.g. on a
GDPR Art. 17 account erasure. Django's cascade only removes the DB rows, so without this the
image/file *bytes* would orphan in object storage and a child's media could survive deletion.
These `pre_delete` receivers fire for every removal path (single delete, queryset delete, and
cascade) and schedule backing-blob removal after the database deletion commits.
"""

import logging

from django.db import transaction
from django.db.models.signals import pre_delete
from django.dispatch import receiver

from .models import Attachment, Photo
from .storage import get_storage

logger = logging.getLogger(__name__)


def _delete_blob_after_commit(key: str, *, model_name: str) -> None:
    def _delete() -> None:
        try:
            get_storage().delete(key)
        except Exception:
            logger.exception("Failed to delete media blob %s during %s delete", key, model_name)

    transaction.on_commit(_delete)


@receiver(pre_delete, sender=Photo, dispatch_uid="media_photo_delete_blob")
def delete_blob_on_photo_delete(sender, instance: Photo, **kwargs) -> None:
    """Remove the stored blob once the Photo row deletion commits.

    Storage deletion is not transactional, so deleting in ``pre_delete`` would break rollback:
    a restored row could point at already-removed bytes. ``on_commit`` keeps the DB row and blob
    lifecycle aligned while still covering cascades/queryset deletes.
    """
    if not instance.storage_key:
        return
    _delete_blob_after_commit(instance.storage_key, model_name="Photo")


@receiver(pre_delete, sender=Attachment, dispatch_uid="media_attachment_delete_blob")
def delete_blob_on_attachment_delete(sender, instance: Attachment, **kwargs) -> None:
    """Remove the stored blob once the Attachment row deletion commits.

    Storage deletion is idempotent, so this is safe when the blob was already reclaimed
    (e.g. expired temporary attachments clear ``storage_key`` before retaining the row).
    """
    if not instance.storage_key:
        return
    _delete_blob_after_commit(instance.storage_key, model_name="Attachment")
