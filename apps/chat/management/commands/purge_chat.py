from django.core.management.base import BaseCommand

from apps.chat.services import purge_expired


class Command(BaseCommand):
    help = "Delete chat messages older than CHAT_RETENTION_DAYS (retention policy)."

    def handle(self, *args, **options):
        removed = purge_expired()
        self.stdout.write(self.style.SUCCESS(f"Purged {removed} expired chat message(s)."))
