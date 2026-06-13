from django.db import transaction
from django.db.models import Q
from django.utils import timezone

from apps.safety.sanitize import safe_external_url

from .classify import classify_activity
from .models import Event
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
