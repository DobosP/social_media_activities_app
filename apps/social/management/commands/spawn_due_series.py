"""Spawn the next instance of each due recurring activity series (run via ops run_due_jobs).

All the domain logic — per-series isolation, cohort re-assertion, lead-time/one-at-a-time
spawning, idempotency, and audit — lives in social.services.spawn_due_series. This command is a
thin wrapper. It is a clean no-op on an empty database (nothing due -> spawned=0)."""

from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "Spawn the next single instance of each due recurring activity series."

    def handle(self, *args, **options):
        from apps.social.services import spawn_due_series

        result = spawn_due_series()
        self.stdout.write(
            self.style.SUCCESS(
                "series: spawned={spawned} skipped={skipped} paused={paused}".format(**result)
            )
        )
