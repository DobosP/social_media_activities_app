"""Scheduled RO-EDU freshness job (ADR-0019 §7).

One due-job wrapper so the daily `run_due_jobs` tick keeps the roedu provenance lane
fresh without new infrastructure: venue upsert (`ingest_places --source=roedu`), then
event facts (`sync_roedu_events` — M2 facts-only rules live there), then the Commons
cover ladder for any new venues (`resolve_place_covers`).

Fail-open guard: when `ROEDU_SYNC_ENABLED` is off (default) or no `ROEDU_API_KEY` is
configured, the job SKIPS quietly — a dev box or an outage must not fail the whole
due-jobs tick (its heartbeat gates external monitoring). Explicit opt-in beats
guessing from env presence: the operator flips one setting when the serving layer is
reachable in that environment.

A server-side REFUSAL (`available: false` / a producer-emptied app pack — and ONLY that;
anything else still fails the job) is isolated the same way, for a sharper reason than
politeness: `run_due_jobs` pings its heartbeat
only when every job succeeded, and the tick it shares carries `purge_messaging`,
`lift_suspensions`, `consent_renewal_sweep` and the rest of the GDPR/DSA duties. Letting
an opt-in external source red-line that tick nightly would withhold the compliance
heartbeat for a reason outside this app's control, and bury a real safety-job failure in
the noise. So the refusal is caught, logged with a stack, reported to Sentry (when it is
configured) and written to stderr — never swallowed — and the tick continues. This
mirrors `sync_event_feeds`, which isolates one bad feed by design.

`resolve_place_covers` still runs afterwards, deliberately: it is city-scoped, not
RO-EDU-scoped, so an RO-EDU outage must not freeze Commons cover resolution for
OSM-sourced places. Run either command by hand and it still exits non-zero with the
server's note.
"""

import logging
import os

from django.conf import settings
from django.core.management import call_command
from django.core.management.base import BaseCommand, CommandError

from apps.ingestion.sources.roedu_client import (
    SOCIAL_APP_PACK_ID,
    RoeduContractError,
    RoeduProductUnavailable,
    require_canonical_social_pack,
)


class Command(BaseCommand):
    help = (
        "Daily RO-EDU sync: roedu venues + event facts + Commons covers; "
        f"canonical pack {SOCIAL_APP_PACK_ID}."
    )

    def add_arguments(self, parser):
        parser.add_argument("--city", default=None, help="default: ROEDU_SYNC_CITY setting")

    def handle(self, *args, **opts):
        if not getattr(settings, "ROEDU_SYNC_ENABLED", False):
            self.stdout.write("sync_roedu: skipped (ROEDU_SYNC_ENABLED is off).")
            return
        if not os.environ.get("ROEDU_API_KEY"):
            self.stdout.write("sync_roedu: skipped (no ROEDU_API_KEY in the environment).")
            return
        city = opts["city"] or getattr(settings, "ROEDU_SYNC_CITY", "Cluj-Napoca")
        app_pack = (os.environ.get("ROEDU_APP_PACK") or "").strip()
        if app_pack:
            try:
                app_pack = require_canonical_social_pack(app_pack)
            except RoeduContractError as exc:
                # Validate before venue ingestion: a near-miss/legacy product
                # must never leave a half-applied mixed-mode run.
                raise CommandError(str(exc)) from exc
        refusal = None
        try:
            call_command("ingest_places", "--source", "roedu", "--city", city)
            # Forward the SAME credential the venues lane just used. Without
            # this the events lane fell back to its own hard-coded default,
            # so one nightly job spoke to the producer as two different
            # clients. The guard above already proved the variable is set.
            event_args = ["--city", city, "--api-key", os.environ["ROEDU_API_KEY"]]
            if app_pack:
                event_args.extend(["--app-pack", app_pack])
            call_command("sync_roedu_events", *event_args)
        except CommandError as exc:
            if not isinstance(exc.__cause__, RoeduProductUnavailable):
                # Only a SOURCE REFUSAL is isolated. A missing seed, a stale-snapshot
                # guard, a wrong pack identity or any other contract breach is this
                # app's own failure and must still red-line the tick — swallowing it
                # would recreate the silent zero one level up.
                raise
            # Loud, but isolated — see the module docstring for why this must not
            # red-line the shared compliance tick.
            refusal = exc
            logging.getLogger("apps.ingestion.sync_roedu").exception(
                "sync_roedu: the RO-EDU source failed for %s", city
            )
            self._capture(exc)
            self.stderr.write(self.style.ERROR(f"sync_roedu: RO-EDU source failed: {exc}"))

        # New venues may carry Commons/Wikidata refs — resolve a bounded batch per tick.
        # City-scoped, so it runs even when the RO-EDU lane above failed.
        call_command("resolve_place_covers", "--city", city, "--limit", "100")

        if refusal is not None:
            self.stdout.write(
                f"sync_roedu: covers resolved for {city}; RO-EDU data was NOT refreshed."
            )
            return
        self.stdout.write(self.style.SUCCESS(f"sync_roedu: completed for {city}."))

    @staticmethod
    def _capture(exc):
        """Report the refusal to Sentry. No-op when Sentry isn't configured."""
        try:
            import sentry_sdk

            with sentry_sdk.new_scope() as scope:
                scope.set_tag("due_job", "sync_roedu")
                sentry_sdk.capture_exception(exc)
        except Exception:  # noqa: BLE001 — reporting must never break the run
            pass
