from django.core.management.base import BaseCommand

from apps.social.sentiment import recompute_post_sentiment


class Command(BaseCommand):
    help = (
        "ADR-0029: re-derive every post's appreciation footer from surviving reaction rows (daily) "
        "and advance the adult-only dissent window (weekly). Self-gated on JobMarkers; a thin "
        "wrapper over apps.social.sentiment.recompute_post_sentiment."
    )

    def handle(self, *args, **opts):
        summary = recompute_post_sentiment()
        if summary["skipped"]:
            self.stdout.write("post sentiment: not due yet (daily gate)")
            return
        self.stdout.write(
            self.style.SUCCESS(
                "post sentiment recomputed: "
                f"+{summary['latched']} latched, -{summary['unlatched']} unlatched, "
                f"{summary['permanent']} graduated; dissent={summary['dissent']}"
            )
        )
