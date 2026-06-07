"""Fire saved-search alerts for newly-matching activities (run via ops run_due_jobs).

All the domain logic — per-saver cohort gate, one-notice-per-(user, activity) ledger, rate caps,
per-search isolation — lives in saved_searches.services.match_saved_searches. Thin wrapper; a clean
no-op on an empty DB (no searches -> notified=0)."""

from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "Notify savers when a newly-created activity matches one of their saved searches."

    def handle(self, *args, **options):
        from apps.saved_searches.services import match_saved_searches

        result = match_saved_searches()
        self.stdout.write(
            self.style.SUCCESS(
                "saved-search: notified={notified} scanned={scanned} skipped={skipped}".format(
                    **result
                )
            )
        )
