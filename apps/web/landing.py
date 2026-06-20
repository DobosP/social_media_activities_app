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
