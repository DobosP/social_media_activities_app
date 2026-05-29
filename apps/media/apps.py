from django.apps import AppConfig


class MediaConfig(AppConfig):
    default_auto_field = "django.db.models.BigAutoField"
    name = "apps.media"
    label = "media"

    def ready(self):
        # Register the blob-erasure signal so deleting a Photo (incl. via account/thread
        # cascade) also removes the backing storage object — no orphaned bytes.
        from . import signals  # noqa: F401
