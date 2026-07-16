"""Drain the withheld-video queue (ADR-0026): claim pending/stale video attachments and run
the validate → transcode → poster → frame-scan → finalise pipeline for each.

Runs from its own frequent systemd timer (deploy/systemd/socialapp-media.timer) so a clip is
ready within ~a minute even if the inline post-upload kick was lost, and is also registered in
run_due_jobs as a daily safety net. Safe to run concurrently: claims use
select_for_update(skip_locked=True)."""

from django.core.management.base import BaseCommand

from apps.media.services import process_pending_videos


class Command(BaseCommand):
    help = "Process pending video attachments (transcode + poster + frame scan)."

    def add_arguments(self, parser):
        parser.add_argument(
            "--limit",
            type=int,
            default=None,
            help="Maximum number of videos to process this run (default: drain the queue).",
        )

    def handle(self, *args, **options):
        count = process_pending_videos(limit=options.get("limit"))
        self.stdout.write(f"Processed {count} video attachment(s).")
