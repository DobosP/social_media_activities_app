"""W8 follow-up (review W1-20): pre-existing profile pictures have no perceptual
fingerprint, so they sit outside the near-duplicate protection until backfilled.
One-shot operator command: recomputes the dHash from each stored PROFILE blob.
Idempotent; rows whose blob is missing or undecodable are skipped, not failed."""

from django.core.management.base import BaseCommand

from apps.media.models import Photo
from apps.media.perceptual import dhash_hex
from apps.media.storage import get_storage


class Command(BaseCommand):
    help = "Backfill Photo.phash for existing profile pictures (one-shot, idempotent)."

    def handle(self, *args, **opts):
        storage = get_storage()
        done = skipped = 0
        qs = Photo.objects.filter(kind=Photo.Kind.PROFILE, phash="").exclude(storage_key="")
        for photo in qs.iterator():
            try:
                data = storage.open(photo.storage_key)
            except Exception:  # noqa: BLE001 — missing blob: skip, never abort the sweep
                skipped += 1
                continue
            fingerprint = dhash_hex(data)
            if not fingerprint:
                skipped += 1
                continue
            Photo.objects.filter(pk=photo.pk).update(phash=fingerprint)
            done += 1
        self.stdout.write(self.style.SUCCESS(f"Backfilled {done}; skipped {skipped}."))
