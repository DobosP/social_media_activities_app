"""Run all the periodic maintenance jobs in one pass — the single entry point a cron
(or scheduler) invokes on a schedule.

It fans out to the existing per-app commands rather than re-implementing their logic:

  * ``purge_messaging``        — delete expired E2EE messages (retention / disappearing).
  * ``purge_expired_attachments``— reclaim expired temporary-picture blobs (hidden/reported exempt).
  * ``purge_read_notifications``— delete old read, non-DSA notifications (storage hygiene at scale).
  * ``lift_suspensions``       — reactivate accounts whose suspension or timed ban elapsed.
  * ``auto_complete_activities``— move past OPEN activities to COMPLETED.
  * ``expire_arrivals``        — clear stale arrival pings (keep them ephemeral).
  * ``send_activity_reminders``— notify members of activities starting soon.
  * ``organizer_prep_nudge``   — nudge organisers when a soon meetup still has no meeting point.
  * ``supervisor_needed_nudge``— nudge a CHILD organiser's guardians when a supervisor is needed.
  * ``generate_communities``   — re-materialize the per-cohort community discovery labels.
  * ``reverify_sweep``         — nudge/evict minors on a stale age proof (active expiry).
  * ``consent_renewal_sweep``  — nudge guardians on an expiring parental consent; evict on lapse.
  * ``spawn_due_series``       — spawn the next instance of each due recurring activity series.
  * ``match_saved_searches``   — alert savers when a new activity matches a saved search.
  * ``sync_event_feeds``       — pull registered external calendars (EventFeed) into Events.
  * ``expire_api_tokens``      — delete stale API tokens (forced re-login; no forever-credentials).
  * ``process_deferred_tasks``— drain the durable off-request task queue (apps.ops.tasks).

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
    ("purge_read_notifications", {}),  # P1 storage hygiene (read, non-DSA notices past retention)
    ("lift_suspensions", {}),
    ("purge_guardian_invites", {}),  # W3-F16 delete expired guardian invites (minor-PII hygiene)
    ("auto_complete_activities", {}),
    ("expire_arrivals", {}),
    ("expire_interest", {}),  # F27 gauge expiry (ephemeral, self-healing)
    ("send_activity_reminders", {}),
    ("rsvp_finalize_nudge", {}),  # W2-F11 one-shot 'still coming?' nudge to undecided members
    ("organizer_prep_nudge", {}),  # W3-F6 prep nudge to organisers (blank meeting point)
    ("supervisor_needed_nudge", {}),  # W3-F7 nudge a CHILD organiser's guardians (needs supervisor)
    ("generate_communities", {}),
    ("reverify_sweep", {}),
    ("consent_renewal_sweep", {}),  # W3-F4 active parental-consent expiry (nudge + lapse-evict)
    ("spawn_due_series", {}),
    ("match_saved_searches", {}),
    ("sync_event_feeds", {}),
    ("expire_api_tokens", {}),
    ("indexnow_batch_submit", {}),  # ping Bing/Yandex with recently-changed public URLs (opt-in)
    # Drain the durable off-request task queue LAST, so any task an earlier job enqueued this tick
    # is picked up the same tick (apps.ops.tasks; no-op until the first handler is registered).
    ("process_deferred_tasks", {}),
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
        import logging
        import uuid

        from apps.ops.observability import set_request_id

        # Stamp a per-run correlation id so every log line this cron tick emits (and the per-job
        # failures below) carries it, instead of the bare "-" that non-HTTP code logs by default —
        # the same RequestIdFilter the HTTP path uses then picks it up.
        run_id = f"job:run_due_jobs:{uuid.uuid4().hex[:12]}"
        set_request_id(run_id)
        logger = logging.getLogger("apps.ops.run_due_jobs")
        jobs = self._jobs(options)
        logger.info("run_due_jobs starting (%d job(s), run_id=%s)", len(jobs), run_id)
        failures = []
        for name, kwargs in jobs:
            self.stdout.write(f"-> {name}")
            try:
                call_command(name, **kwargs)
            except Exception as exc:  # keep going: one bad job must not skip the others
                failures.append(name)
                # These are GDPR/DSA duties — a silent failure is a compliance miss, so log with a
                # stack and report to Sentry (no-op when Sentry isn't configured) instead of only
                # writing to a cron log nobody watches.
                logger.exception("due job %s failed", name)
                self._capture(exc, name)
                self.stderr.write(self.style.ERROR(f"   {name} failed: {exc}"))

        ran = len(jobs)
        if failures:
            raise CommandError(
                f"{len(failures)} of {ran} due job(s) failed: {', '.join(failures)}."
            )
        # Only a fully-successful run pings the heartbeat — so a missed OR failed pass never pings
        # and the external monitor alerts.
        self._heartbeat()
        self.stdout.write(self.style.SUCCESS(f"All {ran} due job(s) completed."))

    def _capture(self, exc, job_name):
        """Report a failed job to Sentry, tagged by name. No-op if Sentry isn't configured."""
        try:
            import sentry_sdk

            with sentry_sdk.new_scope() as scope:
                scope.set_tag("due_job", job_name)
                sentry_sdk.capture_exception(exc)
        except Exception:  # noqa: BLE001 — reporting must never break the run
            pass

    def _heartbeat(self):
        """Dead-man's-switch: GET OPS_HEARTBEAT_URL (e.g. a healthchecks.io ping). Best-effort."""
        from django.conf import settings

        url = getattr(settings, "OPS_HEARTBEAT_URL", "")
        if not url:
            return
        try:
            import requests

            requests.get(url, timeout=10)
        except Exception:  # noqa: BLE001 — a heartbeat failure must not fail the run
            pass

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
