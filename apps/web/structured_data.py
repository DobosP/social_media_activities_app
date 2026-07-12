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

from .seo import absolute_url, event_path, place_path

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
        "url": absolute_url(place_path(place), request),
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


def _event_node(event, request=None) -> dict:
    """A compact nested Event node (name + start + canonical url) for a Place's event list."""
    node = {
        "@type": "Event",
        "name": event.title,
        "startDate": event.starts_at.isoformat(),
        "url": absolute_url(event_path(event), request),
    }
    if event.ends_at:
        node["endDate"] = event.ends_at.isoformat()
    return node


def place_ld(place, request=None, events=None) -> dict:
    """Top-level JSON-LD for a venue detail page.

    ``events`` (the venue's already-public upcoming events, as on place_detail) are embedded as
    nested ``Event`` nodes so an answer engine can resolve "what's on at <venue>" from one page.
    Capped so the block stays lean; built only from public ``Event`` rows — no people, no cohorts.
    """
    node = place_node(place, request)
    node["@context"] = "https://schema.org"
    if events:
        nodes = [_event_node(e, request) for e in list(events)[:10]]
        if nodes:
            node["event"] = nodes
    return node


# NOTE: schema.org ``offers``/``isAccessibleForFree`` enrichment from source price facts is
# deliberately absent on this branch — the Event price/availability fields live in the
# in-flight v_2 scraper/data-server lane and the enrichment lands once that schema is on main.
def event_ld(event, request=None) -> dict:
    """Top-level JSON-LD Event for an event detail page (offline, in-person)."""
    node = {
        "@context": "https://schema.org",
        "@type": "Event",
        "name": event.title,
        "startDate": event.starts_at.isoformat(),
        "url": absolute_url(event_path(event), request),
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


def breadcrumb_ld(crumbs, request=None) -> dict:
    """schema.org BreadcrumbList from an ordered list of ``{"name", "url"}`` dicts.

    Earns the breadcrumb trail in search results and clarifies site structure for crawlers.
    A crumb without a ``url`` (the current page) omits ``item``.
    """
    items = []
    for i, crumb in enumerate(crumbs, start=1):
        item = {"@type": "ListItem", "position": i, "name": crumb["name"]}
        if crumb.get("url"):
            item["item"] = absolute_url(crumb["url"], request)
        items.append(item)
    return {
        "@context": "https://schema.org",
        "@type": "BreadcrumbList",
        "itemListElement": items,
    }


def itemlist_ld(entries, request=None) -> dict:
    """schema.org ItemList from ordered ``{"name", "url"}`` entries (url is a root-relative path).

    Lets an answer engine extract a page's list directly (events on a landing page, venues on the
    places list, events on the events list), ordered as shown. Callers build the entries only from
    already-public ``upcoming_events()`` / ``public_places()`` rows — no people, no cohort data.
    """
    items = []
    for i, entry in enumerate(entries, start=1):
        items.append(
            {
                "@type": "ListItem",
                "position": i,
                "name": entry["name"],
                "url": absolute_url(entry["url"], request),
            }
        )
    return {
        "@context": "https://schema.org",
        "@type": "ItemList",
        "itemListElement": items,
    }


def event_entries(events):
    """``{"name","url"}`` entries for ``itemlist_ld`` from Event rows (canonical slugged paths)."""
    return [{"name": e.title, "url": event_path(e)} for e in events]


def place_entries(places):
    """``{"name","url"}`` entries for ``itemlist_ld`` from Place rows (canonical slugged paths)."""
    return [{"name": p.display_name, "url": place_path(p)} for p in places]


def _organization_node(request=None) -> dict:
    """The Organization node shared by ``site_ld`` (the home page's ``@graph``) and
    ``dataset_ld`` (the open-data page's ``creator``) — one shape, two call sites."""
    from django.conf import settings

    org = {
        "@type": ["Organization", "NGO"],
        "name": "Activities",
        "url": absolute_url("/", request),
        "description": (
            "A nonprofit, text-first platform that helps people meet in person for "
            "real group activities at real places — sport, outdoors, games, reading "
            "and more. First city: Cluj-Napoca, Romania."
        ),
    }
    # Entity-resolution hints — only emitted when the operator configures them (env).
    same_as = [u for u in getattr(settings, "SITE_SAMEAS", []) if u]
    if same_as:
        org["sameAs"] = same_as
    area = getattr(settings, "SITE_AREA_SERVED", "")
    if area:
        org["areaServed"] = area
    email = getattr(settings, "SITE_CONTACT_EMAIL", "")
    if email:
        org["email"] = email
    return org


def site_ld(request=None) -> dict:
    """Organization + WebSite (with a search action) for the home/landing page.

    Helps an answer engine name the site and learn its event/place search entry point.
    """
    home = absolute_url("/", request)
    return {
        "@context": "https://schema.org",
        "@graph": [
            _organization_node(request),
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


def dataset_ld(request=None, *, include_snapshot=True) -> dict:
    """schema.org Dataset for /open-data/: the public venues/events/taxonomy dataset for
    Cluj-Napoca plus public adult opt-in activity cards, and its machine-access points.

    ``creator`` reuses the exact same Organization node ``site_ld`` puts on the home page.
    ``distribution`` links the RSS/Atom feeds, the public events JSON API, and — only when
    the operator has configured AGENT_SNAPSHOT_DIR (``include_snapshot``) — the snapshot
    manifest, so the markup never advertises a download that would 404. All are
    already-public, gate-filtered surfaces (see apps/web/agent_snapshot.py and
    apps.events.views.EventViewSet's AllowAny docstring)."""
    from django.urls import reverse

    node = {
        "@context": "https://schema.org",
        "@type": "Dataset",
        "name": "Activities open dataset — Cluj-Napoca venues & events",
        "description": (
            "Public venues (parks, libraries, sports venues) and events for Cluj-Napoca, "
            "Romania, plus public adult opt-in activity cards — open data, updated "
            "periodically from open sources (OSM, Overture, RO-EDU) and venue calendars."
        ),
        "url": absolute_url("/open-data/", request),
        "isAccessibleForFree": True,
        "license": absolute_url("/open-data/#licensing", request),
        "creator": _organization_node(request),
        "spatialCoverage": {"@type": "Place", "name": "Cluj-Napoca, Romania"},
        "distribution": [
            {
                "@type": "DataDownload",
                "name": "Events feed (RSS)",
                "encodingFormat": "application/rss+xml",
                "contentUrl": absolute_url(reverse("events_feed"), request),
            },
            {
                "@type": "DataDownload",
                "name": "Events feed (Atom)",
                "encodingFormat": "application/atom+xml",
                "contentUrl": absolute_url(reverse("events_feed_atom"), request),
            },
            {
                "@type": "DataDownload",
                "name": "Events JSON API",
                "encodingFormat": "application/json",
                "contentUrl": absolute_url("/api/v1/events/", request),
            },
        ],
    }
    if include_snapshot:
        node["distribution"].append(
            {
                "@type": "DataDownload",
                "name": "Snapshot manifest",
                "encodingFormat": "application/json",
                "contentUrl": absolute_url(
                    reverse("open_data_snapshot", args=["manifest.json"]), request
                ),
            }
        )
    return node
