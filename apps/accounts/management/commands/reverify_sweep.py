"""F6 re-verify-or-pause sweep (run via ops `run_due_jobs`).

Makes age-proof expiry ACTIVE rather than lazy: nudges minors whose proof is expiring soon
(and their active guardians) and evicts already-lapsed minors from cohort-pinned rosters and
conversations. The domain logic + safety guards live in accounts.services.run_reverify_sweep."""

from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "Nudge minors whose age proof is expiring; evict + notify minors whose proof has lapsed."

    def handle(self, *args, **options):
        from apps.accounts.services import run_reverify_sweep

        r = run_reverify_sweep()
        self.stdout.write(
            self.style.SUCCESS(
                f"reverify_sweep: nudged={r['nudged']} paused={r['paused']} "
                f"newly_expired={r['newly_expired']}"
            )
        )
