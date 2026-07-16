"""Pull factual RO-EDU events and reconcile their source-owned lifecycle.

The command keeps the M2 facts-only boundary, holds low-confidence events out of
public discovery, maps producer categories to the activity taxonomy, and consumes
either the legacy products or one immutable app-pack for the whole run. A complete
snapshot-bound app-pack may retract absent events; partial/legacy/delta reads never
infer absence. Explicit cancellation/deletion records are applied in either mode.
"""

from __future__ import annotations

from contextlib import nullcontext
from decimal import Decimal

from django.contrib.gis.geos import Point
from django.core.management.base import BaseCommand, CommandError
from django.db import transaction
from django.utils import timezone
from django.utils.dateparse import parse_datetime

from apps.events.classify import classify_roedu_activity
from apps.events.models import Event
from apps.events.services import (
    StaleRoeduSnapshot,
    check_roedu_snapshot_order,
    reconcile_roedu_snapshot,
    tombstone_roedu_event,
    upsert_event,
)
from apps.events.sources import RawEvent
from apps.ingestion.sources.roedu_client import (
    SOCIAL_APP_PACK_ID,
    AppPackRead,
    RoeduClient,
    RoeduContractError,
    is_canonical_social_app_pack_item,
    require_canonical_social_pack,
)
from apps.places.enrichment.dedup import find_duplicate
from apps.places.models import Place

_ATTRIBUTION_KEYS = ("attribution", "credit", "source_name", "publisher", "provider")
_LICENSE_KEYS = ("license_name", "license", "licence", "license_title")
_PROVENANCE_KEYS = ("provenance_url", "source_url", "url")
_APP_PACK_EVENT_KINDS = frozenset({"event", "event_tombstone"})
_REMOVED_STATUSES = frozenset({"deleted", "removed", "retracted", "tombstone", "withdrawn"})
_STATUS_ALIASES = {
    "active": Event.LifecycleStatus.SCHEDULED,
    "published": Event.LifecycleStatus.SCHEDULED,
    "scheduled": Event.LifecycleStatus.SCHEDULED,
    "rescheduled": Event.LifecycleStatus.RESCHEDULED,
    "postponed": Event.LifecycleStatus.POSTPONED,
    "canceled": Event.LifecycleStatus.CANCELLED,
    "cancelled": Event.LifecycleStatus.CANCELLED,
    "sold_out": Event.LifecycleStatus.SOLD_OUT,
    "soldout": Event.LifecycleStatus.SOLD_OUT,
    "moved_online": Event.LifecycleStatus.MOVED_ONLINE,
    "online": Event.LifecycleStatus.MOVED_ONLINE,
    "expired": Event.LifecycleStatus.EXPIRED,
    **{status: Event.LifecycleStatus.REMOVED for status in _REMOVED_STATUSES},
}


def _first_text(record: dict, keys: tuple[str, ...], *, max_length: int) -> str:
    for key in keys:
        value = record.get(key)
        if value is None:
            continue
        text = str(value).strip()
        if text:
            return text[:max_length]
    return ""


def _optional_datetime(record: dict, *keys: str):
    raw = _first_text(record, keys, max_length=80)
    if not raw:
        return None
    parsed = parse_datetime(raw)
    if parsed is not None and timezone.is_naive(parsed):
        parsed = timezone.make_aware(parsed, timezone.get_current_timezone())
    return parsed


def _confidence(record: dict) -> float:
    try:
        return float(record.get("confidence", 0.0) or 0.0)
    except (TypeError, ValueError):
        return 0.0


def _facets(record: dict) -> dict:
    return record.get("facets") if isinstance(record.get("facets"), dict) else {}


def _event_category(record: dict) -> str:
    return str(record.get("category") or _facets(record).get("category") or "").strip()[:64]


def _event_city(record: dict, fallback: str) -> str:
    return str(record.get("city") or _facets(record).get("city") or fallback or "").strip()[:128]


def _event_venue_id(record: dict) -> str:
    facets = _facets(record)
    return str(
        record.get("venue_id")
        or record.get("place_id")
        or facets.get("venue_id")
        or facets.get("place_id")
        or ""
    )


def _external_id(record: dict) -> str:
    record_id = str(record.get("id") or "").strip()
    return f"roedu:{record_id}" if record_id else ""


def _lifecycle(record: dict) -> tuple[str, bool]:
    facets = _facets(record)
    raw_status = (
        str(
            record.get("lifecycle_status")
            or record.get("status")
            or facets.get("lifecycle_status")
            or facets.get("status")
            or ""
        )
        .strip()
        .lower()
    )
    explicit_tombstone = any(
        record.get(key) is True for key in ("tombstone", "deleted", "is_tombstone")
    )
    if record.get("cancelled") is True or record.get("canceled") is True:
        raw_status = "cancelled"
    is_tombstone = explicit_tombstone or raw_status in _REMOVED_STATUSES
    if is_tombstone:
        return Event.LifecycleStatus.REMOVED, True
    if not raw_status:
        return "", False
    return _STATUS_ALIASES.get(raw_status, Event.LifecycleStatus.UNKNOWN), False


def _app_pack_venue_for_resolution(item: dict) -> dict | None:
    if item.get("kind") not in {"venue", "place"} or not is_canonical_social_app_pack_item(item):
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


def _raw_event(
    record: dict,
    *,
    city: str,
    min_confidence: float,
    app_pack: AppPackRead | None = None,
    snapshot_generated_at=None,
) -> RawEvent | None:
    if app_pack is not None and not is_canonical_social_app_pack_item(record):
        return None
    starts = parse_datetime(record.get("start_datetime") or record.get("starts_at") or "")
    title = str(record.get("title") or "").strip()
    external_id = _external_id(record)
    if starts is None or not title or not external_id:
        return None
    if timezone.is_naive(starts):
        starts = timezone.make_aware(starts, timezone.get_current_timezone())
    ends = parse_datetime(record.get("end_datetime") or record.get("ends_at") or "") or None
    if ends is not None and timezone.is_naive(ends):
        ends = timezone.make_aware(ends, timezone.get_current_timezone())
    lifecycle_status, is_tombstone = _lifecycle(record)
    confidence = _confidence(record)
    source_pack_id = app_pack.pack_id if app_pack else ""
    source_snapshot_id = app_pack.snapshot_id if app_pack else ""
    source_release_id = app_pack.release_id if app_pack else ""
    return RawEvent(
        title=title,
        starts_at=starts,
        ends_at=ends,
        description="",  # M2: never store app-pack bodies or scraped prose
        url=(record.get("ticket_url") or "") if app_pack else record.get("source_url") or "",
        external_id=external_id,
        source=Event.Source.SCRAPER,
        attribution=(
            str(record.get("attribution") or record.get("source") or "")[:255]
            if app_pack
            else _first_text(record, _ATTRIBUTION_KEYS, max_length=255)
        ),
        license_name=(
            str(record.get("license") or "")[:120]
            if app_pack
            else _first_text(record, _LICENSE_KEYS, max_length=120)
        ),
        provenance_url=("" if app_pack else _first_text(record, _PROVENANCE_KEYS, max_length=500)),
        source_category=_event_category(record),
        source_confidence=confidence,
        is_import_held=confidence < min_confidence,
        lifecycle_status=lifecycle_status,
        is_tombstone=is_tombstone,
        source_venue_id=_event_venue_id(record),
        source_city=_event_city(record, city),
        source_pack_id=source_pack_id,
        source_snapshot_id=source_snapshot_id,
        source_release_id=source_release_id,
        source_snapshot_generated_at=snapshot_generated_at,
        source_first_seen_at=_optional_datetime(record, "first_seen", "first_seen_at"),
        source_last_seen_at=_optional_datetime(record, "last_seen", "last_seen_at"),
        source_updated_at=_optional_datetime(record, "updated_at", "last_modified"),
        source_recurrence=str(record.get("recurrence") or ""),
        source_timezone=str(record.get("timezone") or ""),
        source_price_min=(
            Decimal(str(record["price_min"])) if record.get("price_min") is not None else None
        ),
        source_price_max=(
            Decimal(str(record["price_max"])) if record.get("price_max") is not None else None
        ),
        source_currency=str(record.get("currency") or ""),
        source_is_free=(record.get("is_free") if isinstance(record.get("is_free"), bool) else None),
        source_availability=str(record.get("availability") or ""),
    )


class Command(BaseCommand):
    help = "Pull and reconcile factual events from one RO-EDU delivery mode."

    def add_arguments(self, parser):
        parser.add_argument("--city", default="Cluj-Napoca")
        parser.add_argument("--api-url", default=None, help="ROEDU_API_URL override")
        parser.add_argument("--api-key", default="social-app-dev")
        parser.add_argument(
            "--app-pack",
            default=None,
            help="consume one redistributable immutable social app-pack",
        )
        parser.add_argument(
            "--updated-since",
            default=None,
            help="legacy product delta boundary; absence is never inferred in delta mode",
        )
        parser.add_argument(
            "--min-confidence",
            type=float,
            default=1.0,
            help="hold lower-confidence events out of discovery; default 1.0",
        )
        parser.add_argument("--limit", type=int, default=None)
        parser.add_argument("--dry-run", action="store_true")
        parser.add_argument(
            "--allow-snapshot-rollback",
            action="store_true",
            help="explicitly permit replaying an older immutable snapshot",
        )

    def handle(self, *args, **opts):
        if opts["app_pack"] and opts["updated_since"]:
            raise CommandError("--app-pack and --updated-since are mutually exclusive")
        if opts["app_pack"]:
            try:
                opts["app_pack"] = require_canonical_social_pack(opts["app_pack"])
            except RoeduContractError as exc:
                raise CommandError(str(exc)) from exc
        client = RoeduClient(base_url=opts["api_url"], api_key=opts["api_key"])
        app_pack = None
        snapshot_generated_at = None
        if opts["app_pack"]:
            try:
                app_pack = client.read_app_pack(
                    opts["app_pack"],
                    city=opts["city"],
                    max_records=opts["limit"],
                )
            except RoeduContractError as exc:
                raise CommandError(str(exc)) from exc
            records = app_pack.items
            if app_pack.pack_id != SOCIAL_APP_PACK_ID:
                raise CommandError("RO-EDU client returned the wrong social app-pack")
            snapshot_generated_at = _optional_datetime(
                {"generated_at": app_pack.snapshot_generated_at}, "generated_at"
            )
            venues = {
                str(venue["id"]): venue
                for item in records
                if (venue := _app_pack_venue_for_resolution(item)) is not None
            }
        else:
            venues = {v["id"]: v for v in client.iter("venues", city=opts["city"])}
            filters = {"city": opts["city"]}
            if opts["updated_since"]:
                filters["updated_since"] = opts["updated_since"]
            records = tuple(client.iter("events", max_records=opts["limit"], **filters))

        can_reconcile = bool(
            app_pack
            and app_pack.snapshot_complete
            and snapshot_generated_at is not None
            and not opts["limit"]
        )
        if can_reconcile:
            try:
                check_roedu_snapshot_order(
                    pack_id=app_pack.pack_id,
                    city=opts["city"],
                    snapshot_id=app_pack.snapshot_id,
                    snapshot_generated_at=snapshot_generated_at,
                    allow_rollback=opts["allow_snapshot_rollback"],
                )
            except StaleRoeduSnapshot as exc:
                raise CommandError(str(exc)) from exc

        counters = {
            "upserted": 0,
            "held": 0,
            "no_place": 0,
            "bad_record": 0,
            "explicit_tombstones": 0,
            "snapshot_tombstones": 0,
        }
        seen_external_ids: set[str] = set()
        # A complete app-pack applies atomically with its absence reconciliation.
        atomic = transaction.atomic() if can_reconcile and not opts["dry_run"] else nullcontext()
        with atomic:
            for record in records:
                if app_pack and record.get("kind") not in _APP_PACK_EVENT_KINDS:
                    continue
                if app_pack and not is_canonical_social_app_pack_item(record):
                    counters["bad_record"] += 1
                    continue
                external_id = _external_id(record)
                _, is_tombstone = _lifecycle(record)
                if not external_id:
                    counters["bad_record"] += 1
                    continue
                if external_id in seen_external_ids:
                    counters["bad_record"] += 1
                    continue
                seen_external_ids.add(external_id)
                if is_tombstone:
                    counters["explicit_tombstones"] += 1
                    if not opts["dry_run"]:
                        tombstone_roedu_event(
                            external_id,
                            source_pack_id=app_pack.pack_id if app_pack else "",
                            source_snapshot_id=app_pack.snapshot_id if app_pack else "",
                            source_release_id=app_pack.release_id if app_pack else "",
                            source_snapshot_generated_at=snapshot_generated_at,
                            source_updated_at=_optional_datetime(
                                record, "updated_at", "last_modified", "last_seen"
                            ),
                        )
                    continue
                raw = _raw_event(
                    record,
                    city=opts["city"],
                    min_confidence=opts["min_confidence"],
                    app_pack=app_pack,
                    snapshot_generated_at=snapshot_generated_at,
                )
                if raw is None:
                    counters["bad_record"] += 1
                    continue
                if raw.is_import_held:
                    counters["held"] += 1
                venue_id = raw.source_venue_id
                if app_pack and venue_id and venue_id not in venues:
                    counters["bad_record"] += 1
                    continue
                place = self._resolve_place(venues.get(venue_id), venue_id=venue_id)
                if place is None:
                    counters["no_place"] += 1
                    if app_pack:
                        # Canonical social rows describe in-person events at a
                        # served venue. Never create a discoverable null-place
                        # event when the local venue stage failed to materialize
                        # that required relationship.
                        counters["bad_record"] += 1
                        continue
                activity_type = classify_roedu_activity(raw.source_category, raw.title)
                if not opts["dry_run"]:
                    upsert_event(
                        raw,
                        place=place,
                        activity_type=activity_type,
                        source=Event.Source.SCRAPER,
                    )
                counters["upserted"] += 1

            # A malformed item makes absence unsafe: keep successful upserts but
            # never retract a prior row from an incomplete consumer interpretation.
            if can_reconcile and not counters["bad_record"] and not opts["dry_run"]:
                try:
                    counters["snapshot_tombstones"] = reconcile_roedu_snapshot(
                        pack_id=app_pack.pack_id,
                        city=opts["city"],
                        snapshot_id=app_pack.snapshot_id,
                        release_id=app_pack.release_id,
                        snapshot_generated_at=snapshot_generated_at,
                        seen_external_ids=seen_external_ids,
                        allow_rollback=opts["allow_snapshot_rollback"],
                    )
                except StaleRoeduSnapshot as exc:
                    raise CommandError(str(exc)) from exc

        verb = "would apply" if opts["dry_run"] else "applied"
        reconcile_note = "complete snapshot" if can_reconcile else "no absence reconciliation"
        if counters["bad_record"] and can_reconcile:
            reconcile_note = "snapshot retraction skipped: malformed event item"
        self.stdout.write(
            f"{verb} {counters['upserted']} events ({reconcile_note}; "
            f"held: {counters['held']}, explicit tombstones: "
            f"{counters['explicit_tombstones']}, snapshot tombstones: "
            f"{counters['snapshot_tombstones']}, no place: {counters['no_place']}, "
            f"bad records: {counters['bad_record']})"
        )

    @staticmethod
    def _resolve_place(venue, *, venue_id: str = ""):
        if venue_id:
            exact = Place.objects.filter(source=Place.Source.ROEDU, external_id=venue_id).first()
            if exact is not None:
                return exact
        if not venue or venue.get("lat") is None or venue.get("lon") is None:
            return None
        point = Point(float(venue["lon"]), float(venue["lat"]), srid=4326)
        return find_duplicate(point, venue.get("name") or "")
