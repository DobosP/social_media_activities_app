"""Export gate-filtered PUBLIC open data to static JSON snapshot files (a Go sidecar serves them).

Opt-in, mirroring the IndexNow pattern: a no-op unless ``AGENT_SNAPSHOT_DIR`` is set (default ""
means the feature is off), so dev/CI write nothing by default. All safety filtering lives in
``apps.web.agent_snapshot`` behind the sanctioned public gates — this command is just the runner
that the ``run_due_jobs`` tick fans out to.
"""

from django.conf import settings
from django.core.management.base import BaseCommand


class Command(BaseCommand):
    help = "Export gate-filtered public data to JSON snapshot files (opt-in: AGENT_SNAPSHOT_DIR)."

    def handle(self, *args, **options):
        directory = (getattr(settings, "AGENT_SNAPSHOT_DIR", "") or "").strip()
        if not directory:
            self.stdout.write("AGENT_SNAPSHOT_DIR unset — agent snapshot disabled, skipping.")
            return

        from apps.web.agent_snapshot import export_snapshot

        counts = export_snapshot(directory)
        self.stdout.write(
            "Agent snapshot written to {dir}: {events} event(s), {places} place(s), "
            "{activities} activity(ies), {categories} categorie(s), {activity_types} type(s)"
            "{trunc}.".format(
                dir=directory,
                trunc=" (TRUNCATED)" if counts["truncated"] else "",
                **counts,
            )
        )
