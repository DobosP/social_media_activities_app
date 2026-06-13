from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Q
from django.utils import timezone

from apps.safety.sanitize import safe_external_url

from .classify import classify_activity
from .models import Event, EventReport
from .sources import RawEvent


@transaction.atomic
def upsert_event(
    raw: RawEvent, *, place=None, activity_type=None, source: str | None = None
) -> Event:
    """Create or update an Event from a RawEvent. Keyed by (source, external_id) when a
    feed UID is present, otherwise by (place, title, starts_at)."""
    src = source or raw.source
    defaults = {
        "place": place,
        "activity_type": activity_type,
        "title": raw.title,
        "description": raw.description,
        "starts_at": raw.starts_at,
        "ends_at": raw.ends_at,
        # Untrusted feed URL — strip anything that isn't a safe http(s) link so it can
        # never be rendered as a javascript:/data: href (stored XSS).
        "url": safe_external_url(raw.url),
        "source": src,
    }
    if raw.external_id:
        event, _ = Event.objects.update_or_create(
            source=src, external_id=raw.external_id, defaults=defaults
        )
    else:
        event, _ = Event.objects.update_or_create(
            place=place, title=raw.title, starts_at=raw.starts_at, defaults=defaults
        )
    return event


def events_with_public_places():
    """Events with the F25 pending-place gate applied (no time filter): an event pinned
    to a still-unpublished user-proposed place must not leak that place's existence or
    name. The single base queryset for EVERY event read surface (web list/detail, the
    events API, discovery, search)."""
    from apps.places.services import public_places

    return Event.objects.select_related("place", "activity_type").filter(
        Q(place__isnull=True) | Q(place_id__in=public_places().values("id"))
    )


def upcoming_events():
    """The F25-gated base, narrowed to upcoming."""
    return events_with_public_places().filter(starts_at__gte=timezone.now())


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
