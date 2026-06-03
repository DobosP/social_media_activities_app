"""F2 — activity-create place gate (closes an F25 gap). An activity may only be organised at a
PUBLICLY-visible place; a still-pending/rejected user-proposed venue must be refused. The map
picker is just a UI layer over this — the gate lives in create_activity so it holds identically on
the web form and the DRF surface (both call the same service)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Activity, UserPlaceProposal

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _pending_user_place():
    """A user-proposed venue with a still-PENDING co-creation proposal (not yet public)."""
    place = Place.objects.create(
        name="Backyard Pitch", location=Point(23.61, 46.77, srid=4326), source=Place.Source.USER
    )
    proposal = UserPlaceProposal.objects.create(
        place=place, proposer=make_user("pp_proposer"), status=UserPlaceProposal.Status.PENDING
    )
    return place, proposal


def test_create_at_public_place_succeeds(adult, place, activity_type):
    activity = social.create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Pickup game",
        starts_at=timezone.now() + timedelta(days=1),
    )
    assert activity.place_id == place.id


def test_create_rejects_pending_user_place(adult, activity_type):
    pending, _ = _pending_user_place()
    with pytest.raises(social.InvalidState):
        social.create_activity(
            adult,
            place=pending,
            activity_type=activity_type,
            title="Organising on a pending venue",
            starts_at=timezone.now() + timedelta(days=1),
        )


def test_create_at_published_user_place_succeeds(adult, activity_type):
    pending, proposal = _pending_user_place()
    proposal.status = UserPlaceProposal.Status.PUBLISHED  # the quorum publishes it -> now public
    proposal.save(update_fields=["status"])
    activity = social.create_activity(
        adult,
        place=pending,
        activity_type=activity_type,
        title="Now allowed",
        starts_at=timezone.now() + timedelta(days=1),
    )
    assert activity.place_id == pending.id


def test_drf_create_at_pending_place_is_clean_error_not_500(adult, activity_type):
    """The DRF surface routes through the same gate; a pending-place POST must be a clean 4xx
    (the InvalidState is mapped to PermissionDenied like the sibling mutators) — never a 500."""
    pending, _ = _pending_user_place()
    client = APIClient()
    client.force_authenticate(adult)
    before = Activity.objects.count()
    resp = client.post(
        "/api/social/activities/",
        {
            "place": pending.id,
            "activity_type": activity_type.id,
            "title": "API sneaky",
            "starts_at": (timezone.now() + timedelta(days=1)).isoformat(),
        },
        format="json",
    )
    assert resp.status_code == 403, resp.content  # clean refusal, not an unhandled 500
    assert Activity.objects.count() == before
