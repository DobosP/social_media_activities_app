"""Agent-snapshot exporter: gate-filtered PUBLIC open data → static JSON files.

A Go sidecar (built separately) serves these files verbatim to answer engines / agents. It
runs NO safety logic of its own, so EVERY safety decision lives here, in Python, behind the
already-sanctioned public gates:

  * activities → ``apps.social.services.public_activities`` (cohort HARD-CODED to ADULT +
    ``is_publicly_listed`` opt-in). NEVER queries ``Activity`` directly, so a CHILD/TEEN meetup
    can never reach these files regardless of any stray flag. The exported field set is a strict
    SUBSET of the already-reviewed anonymous ``ActivityCardSerializer`` (no owner, description,
    meeting point, member data, or login-walled web URL).
  * events → ``apps.events.services.upcoming_events`` (built on ``events_with_public_places``, so
    an event at a still-unpublished user-proposed place — and that place — is filtered out).
  * places → ``apps.places.services.public_places`` (the single public-visibility chokepoint;
    crowd-corrected ``display_*`` reads apply, ``raw_tags`` is never exported).
  * taxonomy → ``ActivityCategory`` + active ``ActivityType`` (no user data exists here).

Contract (the sidecar is built against exactly this — do not deviate):

  * Each data file: ``{"schema_version": 1, "generated_at": <UTC Z>, "count": N, "records": [...]}``
    (``generated_at`` is UTC ISO-8601 with a ``Z`` suffix; see below).
  * ``taxonomy.json`` deviates by design (there is no single record list): it uses TOP-LEVEL
    ``categories`` + ``activity_types`` keys alongside ``schema_version``/``generated_at`` — the
    simplest shape, documented here and NOT wrapped in a ``records`` list.
  * All datetimes are UTC ISO-8601 with a literal ``Z`` suffix (the sidecar parses RFC3339 and
    sorts lexicographically, which both require the ``Z`` normalisation).
  * Event prices mirror what DRF's ``EventSerializer`` emits: ``DecimalField`` with the project's
    default ``COERCE_DECIMAL_TO_STRING`` (unset → True) is serialized as a STRING (e.g. "10.00"),
    or ``null``.
  * Files are written ``<name>.tmp`` then ``os.replace``d (atomic); ``manifest.json`` is written
    LAST, because the sidecar reloads keyed on the manifest changing.
"""

import json
import logging
import os
from datetime import UTC

from django.utils import timezone

logger = logging.getLogger(__name__)

SCHEMA_VERSION = 1

# Hard caps so a runaway dataset can never silently truncate: we slice to the cap AND flag it.
EVENTS_CAP = 10000
PLACES_CAP = 50000
ACTIVITIES_CAP = 2000

EVENTS_FILE = "events.json"
PLACES_FILE = "places.json"
ACTIVITIES_FILE = "activities.json"
TAXONOMY_FILE = "taxonomy.json"
MANIFEST_FILE = "manifest.json"


def _iso_z(dt):
    """UTC ISO-8601 with a literal ``Z`` suffix, or None. The sidecar's RFC3339 parse +
    lexicographic sort depend on this normalisation (never a ``+00:00`` offset)."""
    if dt is None:
        return None
    return dt.astimezone(UTC).isoformat().replace("+00:00", "Z")


def _decimal_str(value):
    """Mirror DRF's default DecimalField rendering (COERCE_DECIMAL_TO_STRING=True): a string,
    or None. Keeps the sidecar's price fields byte-identical to the /api/v1/events/ payload."""
    return None if value is None else str(value)


def _lat(place):
    return place.location.y if place.location else None


def _lon(place):
    return place.location.x if place.location else None


def _place_activity_slugs(place):
    """Activity-type slugs a place supports, excluding disputed edges — mirrors the anonymous
    PlaceCardSerializer. Relies on a ``place_activities__activity`` prefetch (no per-row query)."""
    return [pa.activity.slug for pa in place.place_activities.all() if not pa.is_disputed]


# --- record builders -----------------------------------------------------------------------


def _activity_record(activity):
    # STRICT subset of the reviewed anonymous ActivityCardSerializer: no owner / description /
    # meeting point / member data / login-walled web URL.
    return {
        "id": activity.id,
        "title": activity.title,
        "cohort": activity.cohort,
        "starts_at": _iso_z(activity.starts_at),
        "status": activity.status,
        "activity_type": activity.activity_type.slug if activity.activity_type_id else None,
        "place_id": activity.place_id,
    }


def _place_summary(place):
    """The compact place block embedded in an event record (or None when the event has no place)."""
    if place is None:
        return None
    return {
        "id": place.id,
        "name": place.display_name,
        "city": place.address_city,
        "lat": _lat(place),
        "lon": _lon(place),
    }


def _event_record(event):
    from apps.events.services import event_attribution
    from apps.web.seo import event_path

    return {
        "id": event.id,
        "title": event.title,
        "description": event.description,
        "starts_at": _iso_z(event.starts_at),
        "ends_at": _iso_z(event.ends_at),
        "url": event.url,
        "source": event.source,
        "source_category": event.source_category,
        "lifecycle_status": event.lifecycle_status,
        "source_confidence": event.source_confidence,
        "source_recurrence": event.source_recurrence,
        "source_timezone": event.source_timezone,
        "source_price_min": _decimal_str(event.source_price_min),
        "source_price_max": _decimal_str(event.source_price_max),
        "source_currency": event.source_currency,
        "source_is_free": event.source_is_free,
        "source_availability": event.source_availability,
        "attribution": event.attribution,
        "license_name": event.license_name,
        "provenance_url": event.provenance_url,
        # Same helper EventSerializer.get_attribution_credit uses (dict or None).
        "attribution_credit": event_attribution(event),
        # Slug parity with EventSerializer.activity — the sidecar's ?activity= filter keys on it.
        "activity": event.activity_type.slug if event.activity_type_id else None,
        "place_id": event.place_id,
        "path": event_path(event),
        "place_summary": _place_summary(event.place),
    }


def _place_record(place):
    from apps.web.seo import place_path

    return {
        "id": place.id,
        # display_* so a published crowd correction applies; raw_tags is deliberately EXCLUDED.
        "name": place.display_name,
        "lat": _lat(place),
        "lon": _lon(place),
        "address": place.display_address,
        "city": place.address_city,
        "postcode": place.address_postcode,
        "country": place.address_country,
        "website": place.website,
        "phone": place.phone,
        "opening_hours_text": place.posted_hours_text,
        "activity_types": _place_activity_slugs(place),
        "attribution": place.attribution,
        "license_name": place.license_name,
        "provenance_url": place.provenance_url,
        "path": place_path(place),
    }


# --- querysets (gate-routed, N+1-safe) -----------------------------------------------------


def _activities_qs():
    from apps.social.services import public_activities

    # public_activities already select_related activity_type + place and orders by starts_at.
    return public_activities()


def _events_qs():
    from apps.events.services import upcoming_events

    # upcoming_events select_related place + activity_type; prefetch corrections for display_name
    # (place_summary reads it) and order deterministically for a stable snapshot.
    return upcoming_events().prefetch_related("place__corrections").order_by("starts_at", "id")


def _places_qs():
    from apps.places.services import public_places

    # display_* reads corrections; activity_types reads place_activities → activity. Prefetch both
    # so the up-to-50k-row export stays O(1) queries. Deterministic order.
    return (
        public_places().prefetch_related("corrections", "place_activities__activity").order_by("id")
    )


def _taxonomy_payload():
    from apps.taxonomy.models import ActivityCategory, ActivityType

    categories = [
        {
            "slug": c.slug,
            "name": c.name,
            "parent": c.parent.slug if c.parent_id else None,
        }
        for c in ActivityCategory.objects.select_related("parent").order_by("slug")
    ]
    activity_types = [
        {
            "slug": t.slug,
            "name": t.name,
            "category": t.category.slug if t.category_id else None,
            "parent": t.parent.slug if t.parent_id else None,
            "family_friendly": t.family_friendly,
            "wellness": t.wellness,
        }
        for t in ActivityType.objects.filter(is_active=True)
        .select_related("category", "parent")
        .order_by("slug")
    ]
    return categories, activity_types


# --- write plumbing ------------------------------------------------------------------------


def _write_json(directory, name, payload):
    """Write ``payload`` as JSON to ``<directory>/<name>`` atomically (tmp then os.replace)."""
    tmp = os.path.join(directory, name + ".tmp")
    final = os.path.join(directory, name)
    with open(tmp, "w", encoding="utf-8") as fh:
        json.dump(payload, fh, ensure_ascii=False, separators=(",", ":"))
    os.replace(tmp, final)


def _dataset_payload(records, generated_at):
    return {
        "schema_version": SCHEMA_VERSION,
        "generated_at": generated_at,
        "count": len(records),
        "records": records,
    }


def _collect_capped(qs, cap, label):
    """Return ``(records_slice_helper, total, truncated)``. Truncation is never silent: we
    count first, log a warning, and flag it in the manifest so nothing drops unseen."""
    total = qs.count()
    truncated = total > cap
    if truncated:
        logger.warning(
            "agent snapshot: %s dataset (%d rows) exceeds cap %d — truncating", label, total, cap
        )
    return qs[:cap], truncated


def export_snapshot(directory) -> dict:
    """Export all gate-filtered datasets to ``directory`` and return per-dataset counts.

    Files are written data-first, ``manifest.json`` LAST (the sidecar reloads on manifest change).
    Creates the directory if missing. Returns a dict with the counts + a ``truncated`` flag."""
    from apps.web.seo import site_base_url

    os.makedirs(directory, exist_ok=True)
    generated_at = _iso_z(timezone.now())

    events_slice, events_trunc = _collect_capped(_events_qs(), EVENTS_CAP, "events")
    event_records = [_event_record(e) for e in events_slice]

    places_slice, places_trunc = _collect_capped(_places_qs(), PLACES_CAP, "places")
    place_records = [_place_record(p) for p in places_slice]

    acts_slice, acts_trunc = _collect_capped(_activities_qs(), ACTIVITIES_CAP, "activities")
    activity_records = [_activity_record(a) for a in acts_slice]

    categories, activity_types = _taxonomy_payload()
    truncated = events_trunc or places_trunc or acts_trunc

    _write_json(directory, EVENTS_FILE, _dataset_payload(event_records, generated_at))
    _write_json(directory, PLACES_FILE, _dataset_payload(place_records, generated_at))
    _write_json(directory, ACTIVITIES_FILE, _dataset_payload(activity_records, generated_at))
    _write_json(
        directory,
        TAXONOMY_FILE,
        {
            "schema_version": SCHEMA_VERSION,
            "generated_at": generated_at,
            "categories": categories,
            "activity_types": activity_types,
        },
    )

    # Distinct {license_name, attribution} credit pairs across places + events, license_name
    # non-empty only (a blank licence is not a credit worth surfacing).
    licenses = []
    seen = set()
    for rec in place_records + event_records:
        name = (rec.get("license_name") or "").strip()
        if not name:
            continue
        attribution = (rec.get("attribution") or "").strip()
        key = (name, attribution)
        if key in seen:
            continue
        seen.add(key)
        licenses.append({"license_name": name, "attribution": attribution})

    manifest = {
        "schema_version": SCHEMA_VERSION,
        "generated_at": generated_at,
        "site": site_base_url(None) or "",
        "datasets": {
            "events": {"file": EVENTS_FILE, "count": len(event_records)},
            "places": {"file": PLACES_FILE, "count": len(place_records)},
            "activities": {"file": ACTIVITIES_FILE, "count": len(activity_records)},
            # Total entities in the file (categories + activity types), so the manifest
            # count matches the file contents like every other dataset's count does.
            "taxonomy": {
                "file": TAXONOMY_FILE,
                "count": len(categories) + len(activity_types),
            },
        },
        "licenses": licenses,
        "truncated": truncated,
    }
    _write_json(directory, MANIFEST_FILE, manifest)

    return {
        "events": len(event_records),
        "places": len(place_records),
        "activities": len(activity_records),
        "categories": len(categories),
        "activity_types": len(activity_types),
        "truncated": truncated,
    }
