"""F2 — web map/nearby place picker on the activity-create form. The map is a progressive
enhancement over the place dropdown; the dropdown itself is narrowed to public_places() (F25), so a
pending user-proposed venue is neither offered nor accepted on POST. The picker reuses the existing
/api/places/ proximity API and request-only geolocation (no stored location)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.models import Activity, UserPlaceProposal
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="pp-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="pp-bball", defaults={"name": "Basketball", "category": cat}
    )
    return t


def _public_place():
    return Place.objects.create(
        name="Public Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _pending_place():
    place = Place.objects.create(
        name="Backyard Pitch", location=Point(23.61, 46.77, srid=4326), source=Place.Source.USER
    )
    UserPlaceProposal.objects.create(
        place=place, proposer=_user("pp_prop"), status=UserPlaceProposal.Status.PENDING
    )
    return place


def test_create_page_renders_map_picker():
    _public_place()
    body = _client(_user("pp1")).get("/activities/new/").content.decode()
    assert 'id="map"' in body  # the Leaflet map container
    assert 'id="place-near-me"' in body  # the request-only "find places near me" control
    assert "/api/places/" in body  # reuses the existing public-place proximity API
    assert "never stored" in body  # the privacy promise is shown to the organiser


def test_place_dropdown_excludes_pending_user_place():
    public = _public_place()
    pending = _pending_place()
    resp = _client(_user("pp2")).get("/activities/new/")
    qs = resp.context["form"].fields["place"].queryset
    ids = set(qs.values_list("id", flat=True))
    assert public.id in ids
    assert pending.id not in ids  # F25: a pending venue is never offered


def test_post_with_pending_place_is_rejected():
    pending = _pending_place()
    atype = _type()
    before = Activity.objects.count()
    resp = _client(_user("pp3")).post(
        "/activities/new/",
        {
            "place": str(pending.id),  # tampered: a pending venue id not in the dropdown
            "activity_type": str(atype.id),
            "title": "Sneaky",
            "starts_at": (timezone.now() + timedelta(days=1)).strftime("%Y-%m-%dT%H:%M"),
        },
    )
    assert resp.status_code == 200  # re-rendered with errors, not a redirect
    assert Activity.objects.count() == before  # nothing created at a pending venue


def test_post_with_public_place_creates_activity():
    public = _public_place()
    atype = _type()
    resp = _client(_user("pp4")).post(
        "/activities/new/",
        {
            "place": str(public.id),
            "activity_type": str(atype.id),
            "title": "Pickup game",
            "starts_at": (timezone.now() + timedelta(days=1)).strftime("%Y-%m-%dT%H:%M"),
        },
    )
    assert resp.status_code == 302  # redirect to the new activity
    assert Activity.objects.filter(title="Pickup game", place=public).exists()
