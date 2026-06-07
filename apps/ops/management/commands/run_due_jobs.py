"""Run all the periodic maintenance jobs in one pass — the single entry point a cron
(or scheduler) invokes on a schedule.

It fans out to the existing per-app commands rather than re-implementing their logic:

  * ``purge_messaging``        — delete expired E2EE messages (retention / disappearing).
  * ``purge_expired_attachments``— reclaim expired temporary-picture blobs (hidden/reported exempt).
  * ``lift_suspensions``       — reactivate accounts whose temporary suspension elapsed.
  * ``auto_complete_activities``— move past OPEN activities to COMPLETED.
  * ``expire_arrivals``        — clear stale arrival pings (keep them ephemeral).
  * ``send_activity_reminders``— notify members of activities starting soon.
  * ``generate_communities``   — re-materialize the per-cohort community discovery labels.
  * ``reverify_sweep``         — nudge/evict minors on a stale age proof (active expiry).
  * ``spawn_due_series``       — spawn the next instance of each due recurring activity series.

Each job is isolated: a failure in one is reported but does not abort the rest, so a
single broken job never blocks the others on a shared cron tick. Exit status is non-zero
if any job failed, so the scheduler can surface the problem.
"""

from django.core.management import call_command
from django.core.management.base import BaseCommand, CommandError

# The jobs to run, in order, as (command_name, kwargs-for-call_command).
DUE_JOBS = (
    ("purge_messaging", {}),
    ("purge_expired_attachments", {}),
    ("lift_suspensions", {}),
    ("auto_complete_activities", {}),
    ("expire_arrivals", {}),
    ("send_activity_reminders", {}),
    ("generate_communities", {}),
    ("reverify_sweep", {}),
    ("spawn_due_series", {}),
)


class Command(BaseCommand):
    help = "Run all due periodic maintenance jobs (purges, suspension lifts, reminders)."

    def add_arguments(self, parser):
        parser.add_argument(
            "--reminder-within-hours",
            type=int,
            default=None,
            help="Lookahead window passed to send_activity_reminders (--within-hours).",
        )

    def handle(self, *args, **options):
        jobs = self._jobs(options)
        failures = []
        for name, kwargs in jobs:
            self.stdout.write(f"-> {name}")
            try:
                call_command(name, **kwargs)
            except Exception as exc:  # keep going: one bad job must not skip the others
                failures.append(name)
                self.stderr.write(self.style.ERROR(f"   {name} failed: {exc}"))

        ran = len(jobs)
        if failures:
            raise CommandError(
                f"{len(failures)} of {ran} due job(s) failed: {', '.join(failures)}."
            )
        self.stdout.write(self.style.SUCCESS(f"All {ran} due job(s) completed."))

    def _jobs(self, options):
        """Resolve the job list, threading optional per-job arguments."""
        within_hours = options.get("reminder_within_hours")
        jobs = []
        for name, kwargs in DUE_JOBS:
            kwargs = dict(kwargs)
            if name == "send_activity_reminders" and within_hours is not None:
                kwargs["within_hours"] = within_hours
            jobs.append((name, kwargs))
        return jobs
