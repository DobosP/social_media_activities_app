import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.events.models import Event
from apps.places.models import Place, PlaceActivity
from apps.taxonomy.models import ActivityCategory, ActivityType

# Cluj-Napoca-ish coordinates.
CENTER = (23.6, 46.77)
FAR = (26.1, 44.43)  # Bucharest — well outside a small radius


def _type(slug, *, wellness=False, family_friendly=False):
    cat, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    obj, _ = ActivityType.objects.get_or_create(
        slug=slug,
        defaults={
            "name": slug.title(),
            "category": cat,
            "wellness": wellness,
            "family_friendly": family_friendly,
        },
    )
    return obj


def _place(name, lon, lat, *, website="", activities=()):
    place = Place.objects.create(
        name=name,
        location=Point(lon, lat, srid=4326),
        source=Place.Source.OSM,
        address_city="Cluj-Napoca",
        website=website,
    )
    for at in activities:
        PlaceActivity.objects.create(place=place, activity=at)
    return place


@pytest.fixture
def seed(db):
    # Unique test-only slugs so traits are deterministic (seeded slugs already exist
    # with their own wellness/family_friendly values).
    sport = _type("disc_sport", family_friendly=True, wellness=False)
    calm = _type("disc_calm", wellness=True, family_friendly=False)
    near_court = _place(
        "Central Court", *CENTER, website="https://book.example/court", activities=[sport]
    )
    calm_studio = _place("Calm Studio", 23.61, 46.771, activities=[calm])
    far_court = _place("Far Court", *FAR, activities=[sport])
    event = Event.objects.create(
        title="Pickup game",
        place=near_court,
        activity_type=sport,
        starts_at=timezone.now() + timezone.timedelta(days=2),
    )
    return {
        "sport": sport,
        "calm": calm,
        "near_court": near_court,
        "calm_studio": calm_studio,
        "far_court": far_court,
        "event": event,
    }
