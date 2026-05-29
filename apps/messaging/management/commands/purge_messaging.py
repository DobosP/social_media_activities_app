from django.core.management.base import BaseCommand

from apps.messaging.services import purge_expired_messages


class Command(BaseCommand):
    help = (
        "Delete expired encrypted messages: per-conversation disappearing timers "
        "plus the global MESSAGING_RETENTION_DAYS backstop (data minimization)."
    )

    def handle(self, *args, **options):
        removed = purge_expired_messages()
        self.stdout.write(self.style.SUCCESS(f"Purged {removed} expired message(s)."))
