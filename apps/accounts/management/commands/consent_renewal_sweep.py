"""W3-F4 parental-consent renewal sweep (run via ops `run_due_jobs`).

Makes parental-consent expiry ACTIVE rather than lazy: nudges the ACTIVE guardians of a CHILD
ward whose consent is expiring soon, and evicts a ward whose LAST valid consent has lapsed from
cohort-pinned rosters and conversations. The domain logic + safety guards (per-consent at-most-
once marker, per-tick eviction cap, mass-lapse audit) live in
accounts.services.run_consent_renewal_sweep."""

from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "Nudge guardians whose ward's parental consent is expiring; evict + notify on lapse."

    def handle(self, *args, **options):
        from apps.accounts.services import run_consent_renewal_sweep

        r = run_consent_renewal_sweep()
        self.stdout.write(
            self.style.SUCCESS(
                f"consent_renewal_sweep: nudged={r['nudged']} paused={r['paused']} "
                f"newly_lapsed={r['newly_lapsed']}"
            )
        )
