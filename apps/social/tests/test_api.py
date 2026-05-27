import pytest
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand
from apps.social.models import Membership
from apps.social.services import create_activity

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _client(user):
    client = APIClient()
    client.force_authenticate(user)
    return client


def test_activities_require_auth():
    assert APIClient().get("/api/social/activities/").status_code in (401, 403)


def test_create_and_list_activity(adult, place, activity_type, now):
    client = _client(adult)
    resp = client.post(
        "/api/social/activities/",
        {
            "place": place.id,
            "activity_type": activity_type.id,
            "title": "Evening run",
            "starts_at": now.isoformat(),
        },
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert resp.json()["cohort"] == adult.cohort

    listed = client.get("/api/social/activities/").json()["results"]
    assert len(listed) == 1


def test_list_is_cohort_scoped(adult, child, place, activity_type, now):
    create_activity(
        adult, place=place, activity_type=activity_type, title="Adults only", starts_at=now
    )
    # Child is in a different cohort and must not see the adult activity.
    results = _client(child).get("/api/social/activities/").json()["results"]
    assert results == []


def test_join_and_vote_flow(adult, adult2, place, activity_type, now):
    activity = create_activity(
        adult, place=place, activity_type=activity_type, title="Board games", starts_at=now
    )
    m2 = activity.memberships.create(
        user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    assert m2.state == Membership.State.MEMBER

    joiner = make_user("apijoiner", AgeBand.ADULT)
    resp = _client(joiner).post(f"/api/social/activities/{activity.id}/join/")
    assert resp.status_code == 201, resp.content
    membership_id = resp.json()["id"]

    # Both current members approve → admitted (2/2 >= 2/3).
    for voter in (adult, adult2):
        r = _client(voter).post(
            f"/api/social/memberships/{membership_id}/vote/", {"approve": True}, format="json"
        )
        assert r.status_code == 200, r.content

    assert Membership.objects.get(id=membership_id).state == Membership.State.MEMBER


def test_post_requires_membership(adult, place, activity_type, now):
    activity = create_activity(
        adult, place=place, activity_type=activity_type, title="Hike", starts_at=now
    )
    outsider = make_user("apilurker", AgeBand.ADULT)
    resp = _client(outsider).post(
        f"/api/social/activities/{activity.id}/posts/", {"body": "hi"}, format="json"
    )
    assert resp.status_code == 403

    ok = _client(adult).post(
        f"/api/social/activities/{activity.id}/posts/", {"body": "Meet at 6"}, format="json"
    )
    assert ok.status_code == 201, ok.content
