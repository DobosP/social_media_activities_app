from django.core.management.base import BaseCommand

from apps.media.services import purge_expired_attachments


class Command(BaseCommand):
    help = (
        "Reclaim the blobs of expired temporary thread pictures. Hidden or unresolved-reported "
        "content is exempt (evidence preserved), EXCEPT a post hidden only by its author's own "
        "deletion with no standing REMOVE — that is nobody's evidence and is reclaimed (GDPR "
        "storage limitation). The row is retained, only the bytes are removed."
    )

    def add_arguments(self, parser):
        parser.add_argument(
            "--limit",
            type=int,
            default=None,
            help="Maximum number of blobs to reclaim this run (default: drain the backlog).",
        )

    def handle(self, *args, **options):
        purged = purge_expired_attachments(limit=options.get("limit"))
        self.stdout.write(self.style.SUCCESS(f"Purged {purged} expired attachment blob(s)."))
