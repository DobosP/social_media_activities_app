from django.core.management.base import BaseCommand

from apps.social.sentiment import evaluate_concerns


class Command(BaseCommand):
    help = (
        "ADR-0029: run the daily conduct-concern restorative ladder + the anti-bully sensors "
        "(coordinated-flagging, pile-on protection, flagger down-weighting). Never auto-delivers "
        "to a minor. Self-gated on a JobMarker; a thin wrapper over "
        "apps.social.sentiment.evaluate_concerns."
    )

    def handle(self, *args, **opts):
        summary = evaluate_concerns()
        if summary["skipped"]:
            self.stdout.write("concerns: not due yet (daily gate)")
            return
        self.stdout.write(
            self.style.SUCCESS(
                "concerns evaluated: "
                f"{summary['notes']} note(s), {summary['escalated']} escalated, "
                f"{summary['teen']} teen, {summary['coordinated']} coordinated, "
                f"{summary['pileon']} pile-on"
            )
        )
