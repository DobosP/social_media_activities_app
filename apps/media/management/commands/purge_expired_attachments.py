from django.core.management.base import BaseCommand

from apps.media.services import purge_expired_attachments


class Command(BaseCommand):
    help = (
        "Reclaim the blobs of expired temporary thread pictures. Hidden or unresolved-reported "
        "content is exempt (evidence preserved); the row is retained, only the bytes are removed."
    )

    def handle(self, *args, **options):
        purged = purge_expired_attachments()
        self.stdout.write(self.style.SUCCESS(f"Purged {purged} expired attachment blob(s)."))
