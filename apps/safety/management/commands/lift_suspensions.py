from django.core.management.base import BaseCommand

from apps.safety.services import lift_expired_suspensions


class Command(BaseCommand):
    help = "Reactivate accounts whose suspension or timed ban has elapsed (run on a schedule)."

    def handle(self, *args, **options):
        count = lift_expired_suspensions()
        self.stdout.write(self.style.SUCCESS(f"Reactivated {count} account(s)."))
