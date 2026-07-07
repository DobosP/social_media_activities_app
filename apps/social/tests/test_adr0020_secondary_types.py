"""ADR-0020 — secondary activity types: caps, envelope gates, discovery matching."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.models import AgeBand
from apps.places.models import Place
from apps.social.services import (
    InvalidState,
    create_activity,
    search_activities,
    update_activity,
)
from apps.social.tests.conftest import make_user
from apps.taxonomy.models import ActivityType

pytestmark = pytest.mark.django_db


@pytest.fixture
def place():
    return Place.objects.create(
        name="Parcul Rozelor", location=Point(23.58, 46.76, srid=4326), source=Place.Source.OSM
    )


@pytest.fixture
def adult():
    return make_user("adr20-adult")


def _types(*slugs):
    return [ActivityType.objects.get(slug=s) for s in slugs]


def _create(owner, place, primary, secondary=None, **over):
    defaults = {
        "place": place,
        "activity_type": primary,
        "title": "Multi-type meetup",
        "starts_at": timezone.now() + timedelta(days=3),
        "secondary_types": secondary,
    }
    defaults.update(over)
    return create_activity(owner, **defaults)


def test_create_sets_deduped_secondary_types_excluding_primary(adult, place):
    basketball, reading, hiking = _types("basketball", "reading", "hiking")
    activity = _create(adult, place, basketball, secondary=[reading, basketball, reading, hiking])
    assert set(activity.secondary_types.all()) == {reading, hiking}


def test_create_caps_secondary_types(adult, place):
    basketball, reading, hiking, cycling = _types("basketball", "reading", "hiking", "cycling")
    with pytest.raises(InvalidState):
        _create(adult, place, basketball, secondary=[reading, hiking, cycling])


def test_search_matches_secondary_type_vocabulary(adult, place):
    basketball, reading = _types("basketball", "reading")
    activity = _create(adult, place, basketball, secondary=[reading])

    hits = list(search_activities(adult, "reading"))

    assert activity in hits


def test_update_replaces_and_clears_secondary_types(adult, place):
    basketball, reading, hiking = _types("basketball", "reading", "hiking")
    activity = _create(adult, place, basketball, secondary=[reading])

    update_activity(adult, activity, secondary_types=[hiking])
    assert set(activity.secondary_types.all()) == {hiking}

    update_activity(adult, activity, secondary_types=[])
    assert activity.secondary_types.count() == 0


def test_child_envelope_applies_to_secondary_types(place, settings):
    """A CHILD whose guardian envelope allows only sport cannot smuggle a reading-category
    type in as a SECONDARY."""
    from apps.accounts.services import link_guardian, set_guardian_guardrail
    from apps.taxonomy.models import ActivityCategory

    sport = ActivityCategory.objects.create(slug="adr20-sport", name="Sport 20")
    reading_cat = ActivityCategory.objects.create(slug="adr20-read", name="Reading 20")
    basketball = ActivityType.objects.create(
        slug="adr20-basketball", name="Basketball 20", category=sport, is_active=True
    )
    bookclub = ActivityType.objects.create(
        slug="adr20-bookclub", name="Book club 20", category=reading_cat, is_active=True
    )
    child = make_user("adr20-child", AgeBand.UNDER_16, consented=True)
    guardian = make_user("adr20-guardian")
    link_guardian(guardian, child)
    set_guardian_guardrail(guardian, child, allowed_categories=["adr20-sport"])
    settings.CHILD_PUBLIC_VENUES_ONLY = False

    with pytest.raises(InvalidState):
        _create(child, place, basketball, secondary=[bookclub])
