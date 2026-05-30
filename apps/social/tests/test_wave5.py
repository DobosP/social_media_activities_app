"""F8 (what-to-expect fields) + F17 (beginners-welcome flag) at the service/API layer."""

from datetime import timedelta

import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.social.models import Activity
from apps.social.services import create_activity, update_activity

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=timezone.now() + timedelta(days=1),
        **kw,
    )


def test_create_with_what_to_expect_fields(adult, place, activity_type):
    a = _activity(
        adult,
        place,
        activity_type,
        cost_band=Activity.CostBand.PAID,
        difficulty=Activity.Difficulty.EASY,
        accessibility_notes="Step-free access.",
        beginners_welcome=True,
    )
    assert a.cost_band == "paid"
    assert a.difficulty == "easy"
    assert a.accessibility_notes == "Step-free access."
    assert a.beginners_welcome is True


def test_create_defaults_to_unspecified_not_blank(adult, place, activity_type):
    a = _activity(adult, place, activity_type)
    # The sentinel, never "" — an empty string would bypass the model's choices validation.
    assert a.cost_band == Activity.CostBand.UNSPECIFIED
    assert a.difficulty == Activity.Difficulty.UNSPECIFIED
    assert a.beginners_welcome is False


def test_update_edits_fields_but_keeps_place_locked(adult, place, activity_type):
    a = _activity(adult, place, activity_type)
    other = type(place).objects.create(name="Other", location=place.location, source=place.source)
    update_activity(adult, a, cost_band=Activity.CostBand.FREE, beginners_welcome=True, place=other)
    a.refresh_from_db()
    assert a.cost_band == "free"
    assert a.beginners_welcome is True
    assert a.place_id == place.id  # place is not editable — change dropped


def _api(user):
    c = APIClient()
    c.force_authenticate(user)
    return c


def test_patch_omitting_beginners_does_not_reset_it(adult, place, activity_type):
    a = _activity(adult, place, activity_type, beginners_welcome=True)
    resp = _api(adult).patch(f"/api/social/activities/{a.id}/", {"title": "Renamed"}, format="json")
    assert resp.status_code == 200, resp.content
    a.refresh_from_db()
    assert a.title == "Renamed"
    assert a.beginners_welcome is True  # absent from PATCH → must NOT be reset to False


def test_create_serializer_rejects_bad_choice(adult, place, activity_type):
    resp = _api(adult).post(
        "/api/social/activities/",
        {
            "place": place.id,
            "activity_type": activity_type.id,
            "title": "Run",
            "starts_at": timezone.now().isoformat(),
            "difficulty": "extreme",  # not a valid choice
        },
        format="json",
    )
    assert resp.status_code == 400, resp.content
    assert "difficulty" in resp.json()
