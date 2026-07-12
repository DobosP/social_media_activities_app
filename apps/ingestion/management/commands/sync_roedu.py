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
"""

import os

from django.conf import settings
from django.core.management import call_command
from django.core.management.base import BaseCommand, CommandError

from apps.ingestion.sources.roedu_client import (
    SOCIAL_APP_PACK_ID,
    RoeduContractError,
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
        call_command("ingest_places", "--source", "roedu", "--city", city)
        event_args = ["--city", city]
        if app_pack:
            event_args.extend(["--app-pack", app_pack])
        call_command("sync_roedu_events", *event_args)
        # New venues may carry Commons/Wikidata refs — resolve a bounded batch per tick.
        call_command("resolve_place_covers", "--city", city, "--limit", "100")
        self.stdout.write(self.style.SUCCESS(f"sync_roedu: completed for {city}."))
