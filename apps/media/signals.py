"""Storage-blob lifecycle signals.

Photo/Attachment rows cascade-delete when their uploader (or thread/post) is deleted — e.g. on a
GDPR Art. 17 account erasure. Django's cascade only removes the DB rows, so without this the
image/file *bytes* would orphan in object storage and a child's media could survive deletion.
These `pre_delete` receivers fire for every removal path (single delete, queryset delete, and
cascade) and remove the backing blob first.
"""

import logging

from django.db.models.signals import pre_delete
from django.dispatch import receiver

from .models import Attachment, Photo
from .storage import get_storage

logger = logging.getLogger(__name__)


@receiver(pre_delete, sender=Photo, dispatch_uid="media_photo_delete_blob")
def delete_blob_on_photo_delete(sender, instance: Photo, **kwargs) -> None:
    """Remove the stored blob before the Photo row is deleted. Storage ``delete`` is
    idempotent, so this is safe even when the blob was already removed (e.g. by
    ``delete_photo``/profile replacement)."""
    if not instance.storage_key:
        return
    try:
        get_storage().delete(instance.storage_key)
    except Exception:  # never let a storage hiccup block account/content erasure
        logger.exception("Failed to delete media blob %s during Photo delete", instance.storage_key)


@receiver(pre_delete, sender=Attachment, dispatch_uid="media_attachment_delete_blob")
def delete_blob_on_attachment_delete(sender, instance: Attachment, **kwargs) -> None:
    """Remove the stored blob before the Attachment row is deleted.

    ``delete_attachment`` may already have removed it explicitly; storage deletion is idempotent, so
    the signal is still safe and covers cascade/queryset delete paths.
    """
    if not instance.storage_key:
        return
    try:
        get_storage().delete(instance.storage_key)
    except Exception:  # never let a storage hiccup block account/content erasure
        logger.exception(
            "Failed to delete media blob %s during Attachment delete", instance.storage_key
        )
