import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.events.models import Event
from apps.social.models import Activity
from apps.taxonomy.models import ActivityType

pytestmark = pytest.mark.django_db

NEAR = {"near_lon": 23.6, "near_lat": 46.77, "radius_m": 5000}


def test_near_me_proximity_filters_out_far_places(seed):
    resp = APIClient().get("/api/discovery/near-me/", NEAR)
    assert resp.status_code == 200
    names = [c["name"] for c in resp.data]
    assert "Central Court" in names
    assert "Far Court" not in names  # outside the 5km radius
    # Nearest first.
    assert resp.data[0]["distance_m"] is not None


def test_near_me_activity_and_trait_filters(seed):
    by_activity = APIClient().get("/api/discovery/near-me/", {"activity": "disc_calm"})
    assert [c["name"] for c in by_activity.data] == ["Calm Studio"]

    wellness = APIClient().get("/api/discovery/near-me/", {"wellness": "true"})
    assert {c["name"] for c in wellness.data} == {"Calm Studio"}

    bookable = APIClient().get("/api/discovery/near-me/", {"bookable": "true"})
    assert all(c["is_bookable"] for c in bookable.data)
    assert "Central Court" in [c["name"] for c in bookable.data]


def test_near_me_has_events_filter(seed):
    resp = APIClient().get("/api/discovery/near-me/", {"has_events": "true"})
    assert [c["name"] for c in resp.data] == ["Central Court"]


def test_happening_lists_upcoming_events(seed):
    resp = APIClient().get("/api/discovery/happening/")
    assert resp.status_code == 200
    assert [e["title"] for e in resp.data] == ["Pickup game"]
    assert resp.data[0]["activity_type"] == "disc_sport"


def test_happening_proximity(seed):
    near = APIClient().get("/api/discovery/happening/", NEAR)
    assert [e["title"] for e in near.data] == ["Pickup game"]
    assert near.data[0]["distance_m"] is not None


def test_source_cancelled_event_is_absent_from_happening_and_has_events(seed):
    seed["event"].lifecycle_status = Event.LifecycleStatus.CANCELLED
    seed["event"].save(update_fields=["lifecycle_status"])

    assert APIClient().get("/api/discovery/happening/").data == []
    assert APIClient().get("/api/discovery/near-me/", {"has_events": "true"}).data == []


def test_activities_feed_requires_auth(seed):
    # 401 (not 403) since W10: TokenAuthentication is the first authenticator, so an
    # unauthenticated API call gets a proper challenge instead of a bare forbidden.
    assert APIClient().get("/api/discovery/activities/").status_code == 401


def test_activities_feed_is_cohort_scoped(seed):
    adult = User.objects.create_user(username="a", password="pw")
    apply_assurance(adult, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    teen = User.objects.create_user(username="t", password="pw")
    apply_assurance(teen, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))

    Activity.objects.create(
        owner=adult,
        place=seed["near_court"],
        activity_type=ActivityType.objects.get(slug="disc_sport"),
        title="Adults game",
        starts_at=timezone.now() + timezone.timedelta(days=1),
        cohort=adult.cohort,
    )
    client = APIClient()
    client.force_authenticate(adult)
    assert [a["title"] for a in client.get("/api/discovery/activities/").data] == ["Adults game"]

    client.force_authenticate(teen)
    assert client.get("/api/discovery/activities/").data == []  # different cohort
