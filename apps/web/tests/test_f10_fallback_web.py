"""W2-F10 compatibility plus ADR-0019 web retirement of the fallback route."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner, *, fallback=True):
    cat, _ = ActivityCategory.objects.get_or_create(slug="f10-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="f10-hike", defaults={"name": "Hiking", "category": cat}
    )
    place = Place.objects.create(
        name="Trailhead", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    now = timezone.now()
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Hike",
        starts_at=now + timedelta(hours=2),
        fallback_starts_at=(now + timedelta(hours=5)) if fallback else None,
    )


def test_web_fallback_route_is_gone_and_button_is_hidden():
    owner = _user("f10owner")
    activity = _activity(owner)
    c = Client()
    c.force_login(owner)

    page = c.get(f"/activities/{activity.id}/").content.decode()
    assert "Switch to plan-B time" not in page
    assert c.post(f"/activities/{activity.id}/fallback/").status_code == 404


def test_web_button_hidden_without_a_backup():
    owner = _user("f10owner2")
    activity = _activity(owner, fallback=False)
    c = Client()
    c.force_login(owner)
    page = c.get(f"/activities/{activity.id}/").content.decode()
    assert "Switch to plan-B time" not in page


def test_edit_form_no_longer_exposes_fallback_time():
    from apps.web.forms import ActivityEditForm

    assert "fallback_starts_at" not in ActivityEditForm().fields


def test_drf_fallback_action_moves_time():
    owner = _user("f10api")
    activity = _activity(owner)
    target = activity.fallback_starts_at
    client = APIClient()
    client.force_authenticate(owner)
    resp = client.post(f"/api/social/activities/{activity.id}/fallback/")
    assert resp.status_code == 200, resp.content
    assert resp.data["fallback_starts_at"] is None  # latch consumed in the response
    activity.refresh_from_db()
    assert activity.starts_at == target
