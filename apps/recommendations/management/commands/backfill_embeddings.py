"""Compute content embeddings for any activities missing one (e.g. created before the
recommendations app shipped)."""

from django.core.management.base import BaseCommand

from apps.recommendations.services import recompute_activity_embedding
from apps.social.models import Activity


class Command(BaseCommand):
    help = "Backfill ActivityEmbedding rows for activities without one."

    def handle(self, *args, **options):
        count = 0
        for activity in Activity.objects.filter(embedding__isnull=True).iterator():
            recompute_activity_embedding(activity)
            count += 1
        self.stdout.write(self.style.SUCCESS(f"Embedded {count} activities."))
