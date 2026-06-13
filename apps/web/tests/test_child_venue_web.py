"""F9 (web) — the create-flow must SHOW the staff-approval path (not silently over-block), and
the activity_detail chip marks an approved CHILD venue."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"
PT = Point(23.6, 46.77, srid=4326)


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _place(slug, raw_tags):
    return Place.objects.create(
        name=f"V-{slug}", location=PT, source=Place.Source.OSM, raw_tags=raw_tags
    )


def _type(slug):
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    return ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)


def test_create_at_unapproved_venue_shows_staff_path(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    child = _child("wv1")
    place = _place("bar", {"amenity": "bar"})
    atype = _type("wv1")
    resp = _client(child).post(
        "/activities/new/",
        {
            "place": place.id,
            "activity_type": atype.id,
            "title": "Hang out",
            "description": "",
            "starts_at": "2030-01-01T10:00",
            "ends_at": "",
            "capacity": "",
        },
    )
    # Re-renders the form (200) with an honest message naming the approval path — not a 404.
    assert resp.status_code == 200
    assert b"approved list for children" in resp.content


def test_detail_shows_chip_for_approved_child_venue(settings):
    settings.CHILD_PUBLIC_VENUES_ONLY = True
    child = _child("wv2")
    place = _place("lib", {"amenity": "library"})
    activity = create_activity(
        child,
        place=place,
        activity_type=_type("wv2"),
        title="Reading",
        starts_at="2030-02-01T10:00Z",
    )
    body = _client(child).get(f"/activities/{activity.id}/").content.decode()
    assert "approved public venue" in body
