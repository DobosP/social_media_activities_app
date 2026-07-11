from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Q
from django.utils import timezone

from apps.safety.sanitize import safe_external_url

from .classify import classify_activity
from .models import Event, EventReport, RoeduEventSyncState
from .sources import RawEvent

DISCOVERABLE_LIFECYCLE_STATUSES = (
    Event.LifecycleStatus.SCHEDULED,
    Event.LifecycleStatus.RESCHEDULED,
    Event.LifecycleStatus.SOLD_OUT,
    Event.LifecycleStatus.MOVED_ONLINE,
)


class StaleRoeduSnapshot(ValueError):
    """An older/different immutable snapshot would overwrite a newer completed sync."""


def _normalized_lifecycle(value: str, *, default: str) -> str:
    value = (value or "").strip().lower()
    return value if value in Event.LifecycleStatus.values else default


def event_is_discoverable(event: Event) -> bool:
    """Whether an upstream event may appear as a current happening."""
    return (
        not event.is_tombstone
        and not event.is_import_held
        and event.lifecycle_status in DISCOVERABLE_LIFECYCLE_STATUSES
    )


@transaction.atomic
def upsert_event(
    raw: RawEvent, *, place=None, activity_type=None, source: str | None = None
) -> Event:
    """Create or update an Event from a RawEvent. Keyed by (source, external_id) when a
    feed UID is present, otherwise by (place, title, starts_at)."""
    src = source or raw.source
    if raw.external_id:
        lookup = {"source": src, "external_id": raw.external_id}
    else:
        lookup = {"place": place, "title": raw.title, "starts_at": raw.starts_at}
    existing = Event.objects.select_for_update().filter(**lookup).first()

    # A late delta must not revert a newer source observation. Snapshot-level
    # ordering is checked separately because a content hash alone is unordered.
    incoming_observed_at = raw.source_updated_at or raw.source_last_seen_at
    if (
        existing is not None
        and src == Event.Source.SCRAPER
        and incoming_observed_at is not None
        and (existing.source_updated_at or existing.source_last_seen_at) is not None
        and incoming_observed_at < (existing.source_updated_at or existing.source_last_seen_at)
    ):
        return existing

    lifecycle_default = (
        existing.lifecycle_status if existing is not None else Event.LifecycleStatus.SCHEDULED
    )
    lifecycle_status = _normalized_lifecycle(
        raw.lifecycle_status,
        default=lifecycle_default,
    )
    is_tombstone = raw.is_tombstone
    if is_tombstone is None:
        is_tombstone = existing.is_tombstone if existing is not None else False
    if is_tombstone:
        lifecycle_status = Event.LifecycleStatus.REMOVED

    def keep(new, field):
        if new not in (None, ""):
            return new
        return getattr(existing, field) if existing is not None else new

    defaults = {
        "place": place,
        "activity_type": activity_type
        or (existing.activity_type if existing is not None else None),
        "title": raw.title,
        "description": raw.description,
        "starts_at": raw.starts_at,
        "ends_at": raw.ends_at,
        # Untrusted feed URL — strip anything that isn't a safe http(s) link so it can
        # never be rendered as a javascript:/data: href (stored XSS).
        "url": safe_external_url(raw.url),
        "source": src,
        "attribution": raw.attribution,
        "license_name": raw.license_name,
        "provenance_url": safe_external_url(raw.provenance_url),
        "source_category": keep(raw.source_category[:64], "source_category"),
        "source_confidence": keep(raw.source_confidence, "source_confidence"),
        "is_import_held": raw.is_import_held,
        "lifecycle_status": lifecycle_status,
        "is_tombstone": is_tombstone,
        "source_venue_id": keep(raw.source_venue_id[:200], "source_venue_id"),
        "source_city": keep(raw.source_city[:128], "source_city"),
        "source_pack_id": keep(raw.source_pack_id[:255], "source_pack_id"),
        "source_snapshot_id": keep(raw.source_snapshot_id[:255], "source_snapshot_id"),
        "source_release_id": keep(raw.source_release_id[:255], "source_release_id"),
        "source_snapshot_generated_at": keep(
            raw.source_snapshot_generated_at, "source_snapshot_generated_at"
        ),
        "source_first_seen_at": keep(raw.source_first_seen_at, "source_first_seen_at"),
        "source_last_seen_at": keep(raw.source_last_seen_at, "source_last_seen_at"),
        "source_updated_at": keep(raw.source_updated_at, "source_updated_at"),
    }
    if existing is None:
        event = Event.objects.create(**{**defaults, **lookup})
    else:
        for field, value in defaults.items():
            setattr(existing, field, value)
        existing.save(update_fields=[*defaults, "updated_at"])
        event = existing
    return event


@transaction.atomic
def tombstone_roedu_event(
    external_id: str,
    *,
    source_pack_id: str = "",
    source_snapshot_id: str = "",
    source_release_id: str = "",
    source_snapshot_generated_at=None,
    source_updated_at=None,
) -> Event | None:
    """Apply a body-less RO-EDU deletion marker without creating a fake event."""
    event = (
        Event.objects.select_for_update()
        .filter(source=Event.Source.SCRAPER, external_id=external_id)
        .first()
    )
    if event is None:
        return None
    if (
        source_updated_at is not None
        and (event.source_updated_at or event.source_last_seen_at) is not None
        and source_updated_at < (event.source_updated_at or event.source_last_seen_at)
    ):
        return event
    event.lifecycle_status = Event.LifecycleStatus.REMOVED
    event.is_tombstone = True
    if source_pack_id:
        event.source_pack_id = source_pack_id[:255]
    if source_snapshot_id:
        event.source_snapshot_id = source_snapshot_id[:255]
    if source_release_id:
        event.source_release_id = source_release_id[:255]
    if source_snapshot_generated_at is not None:
        event.source_snapshot_generated_at = source_snapshot_generated_at
    if source_updated_at is not None:
        event.source_updated_at = source_updated_at
    event.save(
        update_fields=[
            "lifecycle_status",
            "is_tombstone",
            "source_pack_id",
            "source_snapshot_id",
            "source_release_id",
            "source_snapshot_generated_at",
            "source_updated_at",
            "updated_at",
        ]
    )
    return event


def _check_snapshot_order(
    state: RoeduEventSyncState | None,
    *,
    snapshot_id: str,
    snapshot_generated_at,
    allow_rollback: bool,
) -> None:
    if state is None or allow_rollback:
        return
    if snapshot_generated_at < state.snapshot_generated_at:
        raise StaleRoeduSnapshot(
            f"snapshot {snapshot_id!r} predates completed snapshot {state.snapshot_id!r}"
        )
    if snapshot_generated_at == state.snapshot_generated_at and snapshot_id != state.snapshot_id:
        raise StaleRoeduSnapshot(
            "different snapshot ids share the same generation timestamp; explicit rollback "
            "approval is required"
        )


@transaction.atomic
def check_roedu_snapshot_order(
    *,
    pack_id: str,
    city: str,
    snapshot_id: str,
    snapshot_generated_at,
    allow_rollback: bool = False,
) -> None:
    """Lock and validate a full snapshot before any consumer rows are changed."""
    state = (
        RoeduEventSyncState.objects.select_for_update()
        .filter(pack_id=pack_id, city__iexact=city)
        .first()
    )
    _check_snapshot_order(
        state,
        snapshot_id=snapshot_id,
        snapshot_generated_at=snapshot_generated_at,
        allow_rollback=allow_rollback,
    )


@transaction.atomic
def reconcile_roedu_snapshot(
    *,
    pack_id: str,
    city: str,
    snapshot_id: str,
    release_id: str,
    snapshot_generated_at,
    seen_external_ids: set[str],
    allow_rollback: bool = False,
) -> int:
    """Tombstone rows absent from one complete, immutable, snapshot-bound scope.

    Partial pages, deltas, legacy products, and responses without strong snapshot
    metadata must never call this service. Replays of an older snapshot fail closed
    unless an operator explicitly opts into rollback.
    """
    state = (
        RoeduEventSyncState.objects.select_for_update()
        .filter(pack_id=pack_id, city__iexact=city)
        .first()
    )
    _check_snapshot_order(
        state,
        snapshot_id=snapshot_id,
        snapshot_generated_at=snapshot_generated_at,
        allow_rollback=allow_rollback,
    )
    scope = Event.objects.filter(
        source=Event.Source.SCRAPER,
        source_pack_id=pack_id,
        source_city__iexact=city,
        is_tombstone=False,
    ).exclude(external_id__in=seen_external_ids)
    retracted = scope.update(
        lifecycle_status=Event.LifecycleStatus.REMOVED,
        is_tombstone=True,
        source_snapshot_id=snapshot_id,
        source_release_id=release_id,
        source_snapshot_generated_at=snapshot_generated_at,
        updated_at=timezone.now(),
    )
    RoeduEventSyncState.objects.update_or_create(
        pack_id=pack_id,
        city=city,
        defaults={
            "snapshot_id": snapshot_id,
            "release_id": release_id,
            "snapshot_generated_at": snapshot_generated_at,
        },
    )
    return retracted


def events_with_public_places():
    """Events with the F25 pending-place gate applied (no time filter): an event pinned
    to a still-unpublished user-proposed place must not leak that place's existence or
    name. The single base queryset for EVERY event read surface (web list/detail, the
    events API, discovery, search)."""
    from apps.places.services import public_places

    return Event.objects.select_related("place", "activity_type").filter(
        Q(place__isnull=True) | Q(place_id__in=public_places().values("id")),
        is_tombstone=False,
        is_import_held=False,
    )


def event_attribution(event) -> dict[str, str] | None:
    """Neutral source credit for public rendering. Blank metadata stays silent."""
    if not event:
        return None
    attribution = (event.attribution or "").strip()
    license_name = (event.license_name or "").strip()
    provenance_url = safe_external_url(event.provenance_url)
    if not any((attribution, license_name, provenance_url)):
        return None
    return {
        "attribution": attribution,
        "license_name": license_name,
        "provenance_url": provenance_url,
    }


def upcoming_events():
    """The F25-gated base, narrowed to upcoming."""
    return events_with_public_places().filter(
        starts_at__gte=timezone.now(),
        is_import_held=False,
        lifecycle_status__in=DISCOVERABLE_LIFECYCLE_STATUSES,
    )


def search_events(query, *, activity_slug=None, limit=100):
    """Free-text search over upcoming events (W1). Matches title/description and the
    venue name (place gate already applied by ``upcoming_events``); composes with the
    list's type filter so searching never silently drops an active filter. Bounded,
    soonest-first."""
    query = (query or "").strip()
    if len(query) < 2:
        return Event.objects.none()
    qs = upcoming_events()
    if activity_slug:
        qs = qs.filter(activity_type__slug=activity_slug)
    return qs.filter(
        Q(title__icontains=query)
        | Q(description__icontains=query)
        | Q(place__name__icontains=query)
    )[:limit]


def import_events(source, *, place=None, activity_type=None, classify=True) -> int:
    """Pull all RawEvents from an EventSource and upsert them. When no activity_type is
    given, classify each event from its text against the taxonomy. Returns the count."""
    count = 0
    for raw in source.fetch():
        resolved = activity_type
        if resolved is None and classify:
            resolved = classify_activity(f"{raw.title} {raw.description}")
        upsert_event(raw, place=place, activity_type=resolved, source=source.name)
        count += 1
    return count


# --- F21: event accuracy reports (ingest-safe, decaying overlay) ----------------------
# Clones the F28 OpenNowReport pattern for events: a member flags a stale event
# (cancelled/moved/wrong time); once enough RECENT reports land the event is shown as
# "members reported this may have changed" and dropped from the Happening feed. Never a field
# on Event (re-ingest clobbers), counts-only, decaying so a re-listed event self-heals.


def _event_report_settings():
    return (
        getattr(settings, "EVENT_REPORT_THRESHOLD", 3),
        getattr(settings, "EVENT_REPORT_DECAY_SECONDS", 14 * 24 * 3600),
    )


def recent_event_report_count(event, *, now=None) -> int:
    """Count of reports within the decay window. Prefers a ``recent_report_n`` annotation when
    present (so a list view avoids a per-row query)."""
    annotated = getattr(event, "recent_report_n", None)
    if annotated is not None:
        return annotated
    _, decay = _event_report_settings()
    cutoff = (now or timezone.now()) - timedelta(seconds=decay)
    return event.reports.filter(created_at__gte=cutoff).count()


def event_is_flagged(event, *, now=None) -> bool:
    """True once >= threshold recent reports say the event has changed (auto-decay self-heals)."""
    threshold, _ = _event_report_settings()
    return recent_event_report_count(event, now=now) >= threshold


def event_reliability(event, *, now=None):
    """Read-time accuracy sentinel for the UI: ``"unverified"`` when enough recent member reports
    say the event has changed, else ``None`` (treated as reliable). Ingest-safe (F21)."""
    return "unverified" if event_is_flagged(event, now=now) else None


@transaction.atomic
def file_event_report(reporter, event, kind):
    """File one 'this event has changed' report (F21). Idempotent per reporter per event per decay
    window (anti-brigading); rate-limited across events; a fixed kind. Returns the report, or None
    if throttled / already reported this window. Clones file_open_now_report verbatim."""
    from apps.accounts.services import can_participate
    from apps.safety.services import allow_action

    if kind not in EventReport.Kind.values:
        raise ValueError("Unknown event report kind.")
    if not event_is_discoverable(event):
        raise ValueError("This event is no longer listed as an upcoming activity.")
    if not can_participate(reporter):
        raise PermissionError("Verified, consented participation is required to report an event.")
    if not allow_action(
        reporter,
        "event_report",
        limit=getattr(settings, "EVENT_REPORT_RATE_LIMIT", 10),
        window_seconds=getattr(settings, "EVENT_REPORT_RATE_WINDOW_SECONDS", 3600),
    ):
        return None  # over the cross-event rate limit
    _, decay = _event_report_settings()
    cutoff = timezone.now() - timedelta(seconds=decay)
    if event.reports.filter(reporter=reporter, created_at__gte=cutoff).exists():
        return None  # one report per reporter per event per window
    return EventReport.objects.create(event=event, reporter=reporter, kind=kind)


@transaction.atomic
def clear_event_reports(event, *, moderator=None) -> int:
    """Moderator reset — delete all reports so the event re-appears on the next read."""
    n = event.reports.count()
    event.reports.all().delete()
    if moderator is not None:
        from apps.safety.services import record_audit

        record_audit("event.reports_cleared", actor=moderator, target=event)
    return n
