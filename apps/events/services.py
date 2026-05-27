from django.db import transaction

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
        "url": raw.url,
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
