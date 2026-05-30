from django.core.management.base import BaseCommand

from apps.communities.services import generate_communities


class Command(BaseCommand):
    help = "Materialize per-cohort discovery communities from real activity (nightly job)."

    def handle(self, *args, **opts):
        result = generate_communities()
        self.stdout.write(
            self.style.SUCCESS(
                f"Communities: published={result['published']} deactivated={result['deactivated']}"
            )
        )
