"""Pull events from the RO-EDU data platform and upsert them into the app.

Companion to the `roedu` place adapter (`apps/ingestion/sources/ro_scraper.py`):
run `ingest_places --source=roedu` first so the venues exist as Places, then this
command resolves each scraped event to its Place and upserts it via the same
hardened `upsert_event` path the iCal sync uses.

Safety rules baked in (ROEDU design §11):
- **M2** — ship event FACTS only; we never copy the scraped `description` (it can be
  copyrighted prose). Title/date/venue + a `source_url` link are non-copyrightable.
- **M5** — auto-ingest only high-confidence (JSON-LD/iCal, confidence 1.0) events by
  default; lower-confidence NER events are held (raise `--min-confidence 0` to include).

    ROEDU_API_URL=http://<scraper-host>:8077 \
      python manage.py sync_roedu_events --city "Cluj-Napoca"
"""

from __future__ import annotations

from django.contrib.gis.geos import Point
from django.core.management.base import BaseCommand
from django.utils.dateparse import parse_datetime

from apps.events.services import upsert_event
from apps.events.sources import RawEvent
from apps.ingestion.sources.roedu_client import RoeduClient
from apps.places.enrichment.dedup import find_duplicate

_ATTRIBUTION_KEYS = ("attribution", "credit", "source_name", "publisher", "provider")
_LICENSE_KEYS = ("license_name", "license", "licence", "license_title")
_PROVENANCE_KEYS = ("provenance_url", "source_url", "url")
_APP_PACK_EVENT_KINDS = frozenset({"event"})


def _first_text(record: dict, keys: tuple[str, ...], *, max_length: int) -> str:
    for key in keys:
        value = record.get(key)
        if value is None:
            continue
        text = str(value).strip()
        if text:
            return text[:max_length]
    return ""


def _app_pack_venue_for_resolution(item: dict) -> dict | None:
    if item.get("kind") not in {"venue", "place"}:
        return None
    location = item.get("location") if isinstance(item.get("location"), dict) else {}
    if location.get("lat") is None or location.get("lon") is None:
        return None
    return {
        "id": item.get("id"),
        "name": item.get("title") or "",
        "lat": location.get("lat"),
        "lon": location.get("lon"),
    }


def _app_pack_event_venue_id(item: dict) -> str:
    facets = item.get("facets") if isinstance(item.get("facets"), dict) else {}
    return str(item.get("venue_id") or item.get("place_id") or facets.get("venue_id") or "")


def _raw_event_from_app_pack_item(item: dict) -> RawEvent | None:
    if item.get("kind") not in _APP_PACK_EVENT_KINDS:
        return None
    starts = parse_datetime(item.get("start_datetime") or item.get("starts_at") or "")
    if starts is None:
        return None
    ends = parse_datetime(item.get("end_datetime") or item.get("ends_at") or "") or None
    return RawEvent(
        title=item.get("title") or "",
        starts_at=starts,
        ends_at=ends,
        description="",  # app packs remain facts-only; never store item bodies/descriptions
        url="",
        external_id=f"roedu:{item.get('id') or ''}",
        source="roedu",
        attribution=str(item.get("source") or "")[:255],
        license_name=str(item.get("license") or "")[:120],
        provenance_url="",
    )


class Command(BaseCommand):
    help = "Pull events from the RO-EDU data platform and upsert them (facts only)."

    def add_arguments(self, parser):
        parser.add_argument("--city", default="Cluj-Napoca")
        parser.add_argument("--api-url", default=None, help="ROEDU_API_URL override")
        parser.add_argument("--api-key", default="social-app-dev")
        parser.add_argument(
            "--app-pack",
            default=None,
            help=(
                "consume a redistributable /v1/app-packs/social_media_activities_app/<pack> "
                "payload instead of legacy products"
            ),
        )
        parser.add_argument(
            "--min-confidence",
            type=float,
            default=1.0,
            help="hold lower-confidence (NER) events; default 1.0 = JSON-LD/iCal only",
        )
        parser.add_argument("--limit", type=int, default=None)
        parser.add_argument("--dry-run", action="store_true")

    def handle(self, *args, **opts):
        client = RoeduClient(base_url=opts["api_url"], api_key=opts["api_key"])
        if opts["app_pack"]:
            app_pack_items = list(
                client.iter_app_pack(
                    opts["app_pack"],
                    city=opts["city"],
                    max_records=opts["limit"],
                )
            )
            venues = {
                str(venue["id"]): venue
                for item in app_pack_items
                if (venue := _app_pack_venue_for_resolution(item)) is not None
            }
            records = app_pack_items
        else:
            # venue_id -> venue record, for resolving each event to a Place by geo+name
            venues = {v["id"]: v for v in client.iter("venues", city=opts["city"])}
            records = client.iter("events", city=opts["city"], max_records=opts["limit"])

        upserted = held = no_place = bad_date = 0
        for rec in records:
            if opts["app_pack"]:
                if rec.get("kind") not in _APP_PACK_EVENT_KINDS:
                    continue
                raw = _raw_event_from_app_pack_item(rec)
                if raw is None:
                    bad_date += 1
                    continue
                venue_id = _app_pack_event_venue_id(rec)
                place = self._resolve_place(venues.get(venue_id))
                if place is None:
                    no_place += 1
                if not opts["dry_run"]:
                    upsert_event(raw, place=place, source="roedu")
                upserted += 1
                continue
            if (rec.get("confidence") or 0) < opts["min_confidence"]:
                held += 1
                continue
            starts = parse_datetime(rec.get("start_datetime") or "")
            if starts is None:
                bad_date += 1
                continue
            raw = RawEvent(
                title=rec["title"],
                starts_at=starts,
                ends_at=parse_datetime(rec.get("end_datetime") or "") or None,
                description="",  # M2: facts only — never republish scraped prose
                url=rec.get("source_url") or "",
                external_id=f"roedu:{rec['id']}",
                source="roedu",
                attribution=_first_text(rec, _ATTRIBUTION_KEYS, max_length=255),
                license_name=_first_text(rec, _LICENSE_KEYS, max_length=120),
                provenance_url=_first_text(rec, _PROVENANCE_KEYS, max_length=500),
            )
            place = self._resolve_place(venues.get(rec.get("venue_id")))
            if place is None:
                no_place += 1
            if not opts["dry_run"]:
                upsert_event(raw, place=place, source="roedu")
            upserted += 1

        verb = "would upsert" if opts["dry_run"] else "upserted"
        self.stdout.write(
            f"{verb} {upserted} events  (held low-confidence: {held}, "
            f"no place match: {no_place}, unparseable date: {bad_date})"
        )

    @staticmethod
    def _resolve_place(venue):
        if not venue or venue.get("lat") is None or venue.get("lon") is None:
            return None
        point = Point(float(venue["lon"]), float(venue["lat"]), srid=4326)
        return find_duplicate(point, venue.get("name") or "")
