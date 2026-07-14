from django.core.management.base import BaseCommand

from apps.social.sentiment import purge_stale_reaction_rows


class Command(BaseCommand):
    help = (
        "ADR-0029: hard-delete reaction / dissent / concern rows older than "
        "REACTION_ROW_RETENTION_DAYS (delete beats anonymize; derived footers keep their permanent "
        "appreciation slugs). Self-gated daily; a thin wrapper over "
        "apps.social.sentiment.purge_stale_reaction_rows."
    )

    def handle(self, *args, **opts):
        summary = purge_stale_reaction_rows()
        if summary["skipped"]:
            self.stdout.write("reaction purge: not due yet (daily gate)")
            return
        self.stdout.write(
            self.style.SUCCESS(
                "reaction rows purged: "
                f"{summary['reactions']} reactions, {summary['dissents']} dissents, "
                f"{summary['concerns']} concerns"
            )
        )
