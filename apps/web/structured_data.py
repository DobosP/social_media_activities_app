"""schema.org JSON-LD builders — the highest-impact surface for AI answer engines.

Pure functions that map our real, already-public model fields (Place = a venue, Event = a
venue happening) to schema.org dicts. NOTHING here touches the DB or exposes a person:
- Places/Events are open-data (OSM feeds, venue calendars), never cohort/minor data.
- ``social.Activity`` (which can involve minors and is @login_required) is deliberately
  ABSENT — there is no activity builder, so an activity can never be emitted as JSON-LD.

``ld_json`` is the one safe-embedding seam: it escapes the characters that could break out
of a ``<script type="application/ld+json">`` block (``<``/``>``/``&`` and the U+2028/U+2029
line separators that are valid JSON but illegal in a JS string), then marks the result safe.
"""

import json

from django.utils.safestring import mark_safe

from .seo import absolute_url

# Valid JSON but illegal inside a JS string literal — must be escaped in <script> context.
_JSON_LD_ESCAPES = {
    "<": "\\u003c",
    ">": "\\u003e",
    "&": "\\u0026",
    chr(0x2028): "\\u2028",  # LINE SEPARATOR
    chr(0x2029): "\\u2029",  # PARAGRAPH SEPARATOR
}


def ld_json(obj) -> str:
    """Serialize ``obj`` to a string safe to drop inside a ``<script>`` JSON-LD block."""
    raw = json.dumps(obj, ensure_ascii=False, separators=(",", ":"))
    for needle, escaped in _JSON_LD_ESCAPES.items():
        raw = raw.replace(needle, escaped)
    return mark_safe(raw)  # noqa: S308 — escaped above; this is the documented safe seam


def _geo(place):
    """GeoCoordinates from a PostGIS PointField (SRID 4326: .y=lat, .x=lon)."""
    loc = getattr(place, "location", None)
    if loc is None:
        return None
    return {"@type": "GeoCoordinates", "latitude": loc.y, "longitude": loc.x}


def _postal_address(place):
    parts = {
        "streetAddress": f"{place.address_street} {place.address_housenumber}".strip(),
        "addressLocality": place.address_city,
        "postalCode": place.address_postcode,
        "addressCountry": place.address_country,
    }
    parts = {k: v for k, v in parts.items() if v}
    if not parts:
        return None
    return {"@type": "PostalAddress", **parts}


def place_node(place, request=None) -> dict:
    """A schema.org Place node (nested in Event or standalone on place_detail)."""
    node = {
        "@type": "Place",
        "name": place.display_name,
        "url": absolute_url(f"/places/{place.pk}/", request),
    }
    address = _postal_address(place)
    if address:
        node["address"] = address
    elif place.display_address:
        node["address"] = place.display_address
    geo = _geo(place)
    if geo:
        node["geo"] = geo
    if place.website:
        node["sameAs"] = place.website
    return node


def place_ld(place, request=None) -> dict:
    """Top-level JSON-LD for a venue detail page."""
    node = place_node(place, request)
    node["@context"] = "https://schema.org"
    return node


def event_ld(event, request=None) -> dict:
    """Top-level JSON-LD Event for an event detail page (offline, in-person)."""
    node = {
        "@context": "https://schema.org",
        "@type": "Event",
        "name": event.title,
        "startDate": event.starts_at.isoformat(),
        "url": absolute_url(f"/events/{event.pk}/", request),
        "eventAttendanceMode": "https://schema.org/OfflineEventAttendanceMode",
        "eventStatus": "https://schema.org/EventScheduled",
    }
    if event.description:
        node["description"] = event.description
    if event.ends_at:
        node["endDate"] = event.ends_at.isoformat()
    if event.place_id:
        node["location"] = place_node(event.place, request)
    if event.url:
        node["sameAs"] = event.url
    return node


def site_ld(request=None) -> dict:
    """Organization + WebSite (with a search action) for the home/landing page.

    Helps an answer engine name the site and learn its event/place search entry point.
    """
    home = absolute_url("/", request)
    return {
        "@context": "https://schema.org",
        "@graph": [
            {
                "@type": "Organization",
                "name": "Activities",
                "url": home,
                "description": (
                    "A nonprofit, text-first platform that helps people meet in person for "
                    "real group activities at real places — sport, outdoors, games, reading "
                    "and more. First city: Cluj-Napoca, Romania."
                ),
            },
            {
                "@type": "WebSite",
                "name": "Activities",
                "url": home,
                "potentialAction": {
                    "@type": "SearchAction",
                    "target": {
                        "@type": "EntryPoint",
                        "urlTemplate": absolute_url("/events/?q={query}", request),
                    },
                    "query-input": "required name=query",
                },
            },
        ],
    }
