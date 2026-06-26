"""Public city×activity "things to do" landing pages — open-data SEO surfaces.

These pages target the real queries people type ("football in Cluj-Napoca") by composing the
existing F25 visibility chokepoints — ``public_places()`` and ``upcoming_events()`` — narrowed
to one ``Area`` (city) and one ``ActivityType``. No new data is written; nothing here can
surface a cohort activity, a minor, or a pending venue (those never pass the chokepoints).
"""

from apps.communities.services import _area_place_q


def landing_supply(area, activity_type):
    """Return ``(places_qs, events_qs)`` of PUBLIC venues + UPCOMING events for this combo.

    Places match the Area's city directly (``address_city``) and carry a non-disputed edge to
    the type; events route through the same ``_area_place_q`` predicate the events list uses.
    """
    from apps.events.services import upcoming_events
    from apps.places.services import public_places

    places = (
        public_places()
        .filter(
            address_city__iexact=area.city,
            place_activities__activity=activity_type,
            place_activities__is_disputed=False,
        )
        .distinct()
        .order_by("name")
    )
    events = (
        upcoming_events()
        .filter(_area_place_q(area), activity_type=activity_type)
        .select_related("place", "activity_type")
        .order_by("starts_at")
    )
    return places, events


def available_landings():
    """Every ``(area, activity_type)`` combo that currently has real supply.

    Bounded: two ``distinct().values_list`` scans (one over public-place edges, one over
    upcoming events) mapped onto active Areas/Types. Used by the index page + sitemap so we
    never advertise an empty (thin-content) landing page.
    """
    from apps.communities.models import Area
    from apps.events.services import upcoming_events
    from apps.places.services import public_places
    from apps.taxonomy.models import ActivityType

    city_to_area = {a.city.casefold(): a for a in Area.objects.filter(is_active=True)}
    types = {t.id: t for t in ActivityType.objects.filter(is_active=True)}

    place_rows = (
        public_places()
        .filter(place_activities__is_disputed=False)
        .values_list("address_city", "place_activities__activity_id")
        .distinct()
    )
    event_rows = (
        upcoming_events()
        .exclude(activity_type__isnull=True)
        .exclude(place__isnull=True)
        .values_list("place__address_city", "activity_type_id")
        .distinct()
    )

    pairs = set()
    for city, type_id in list(place_rows) + list(event_rows):
        area = city_to_area.get((city or "").casefold())
        activity_type = types.get(type_id)
        if area and activity_type:
            pairs.add((area, activity_type))
    return sorted(pairs, key=lambda p: (p[0].slug, p[1].slug))


def _area_for_city(city):
    """The single active Area whose city matches ``city`` (case-insensitively), or None."""
    from apps.communities.models import Area

    if not city:
        return None
    return Area.objects.filter(city__iexact=city, is_active=True).first()


def related_landings_for_place(place):
    """``(city_crumb, type_links)`` of "things to do" landings relevant to this venue.

    Internal links that deepen the crawl graph and add topical context. Every link is filtered
    through ``available_landings()`` so it never 404s, and the per-activity links are narrowed to
    the types THIS venue actually supports (its non-disputed edges). Returns ``(None, [])`` when
    the venue's city has no active Area or no landing supply. URLs are canonical reverse()s.
    """
    from django.urls import reverse

    area = _area_for_city(place.address_city)
    if area is None:
        return None, []
    city_types = [t for a, t in available_landings() if a.pk == area.pk]
    if not city_types:
        return None, []
    # place_activities is prefetched on place_detail — no extra query.
    supported = {pa.activity_id for pa in place.place_activities.all() if not pa.is_disputed}
    type_links = [
        {"name": t.name, "url": reverse("things_to_do", args=[area.slug, t.slug])}
        for t in city_types
        if t.id in supported
    ]
    city_crumb = {"name": area.name, "url": reverse("things_to_do_city", args=[area.slug])}
    return city_crumb, type_links


def landing_for_event(event):
    """The ``{"name","url"}`` "things to do" landing this PUBLIC event belongs to, or None.

    The event itself is the landing's supply, so the link is always live (no ``available_landings``
    scan needed). Only resolves when the event has a place + active type and the place's city maps
    to an active Area. Call only for a public event (its place must be public for the landing).
    """
    from django.urls import reverse

    if not (event.place_id and event.activity_type_id):
        return None
    if not event.activity_type.is_active:
        return None
    area = _area_for_city(event.place.address_city)
    if area is None:
        return None
    return {
        "name": f"{event.activity_type.name} — {area.name}",
        "url": reverse("things_to_do", args=[area.slug, event.activity_type.slug]),
    }
