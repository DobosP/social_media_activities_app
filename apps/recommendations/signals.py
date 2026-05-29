from django.db.models.signals import post_save
from django.dispatch import receiver

from apps.social.models import Activity

from .services import recompute_activity_embedding


@receiver(post_save, sender=Activity)
def embed_activity(sender, instance, **kwargs):
    """Keep each activity's content embedding in sync (cheap, taxonomy-derived)."""
    recompute_activity_embedding(instance)
