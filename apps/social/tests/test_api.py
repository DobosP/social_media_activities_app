from datetime import timedelta

import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand
from apps.social.models import Activity, Membership, Post
from apps.social.serializers import ACTIVITY_DESCRIPTION_MAX_LENGTH, POST_BODY_MAX_LENGTH
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


# --- input-size caps (serializer layer) ---
def test_post_body_too_long_rejected(adult, place, activity_type, now):
    activity = create_activity(
        adult, place=place, activity_type=activity_type, title="Hike", starts_at=now
    )
    resp = _client(adult).post(
        f"/api/social/activities/{activity.id}/posts/",
        {"body": "x" * (POST_BODY_MAX_LENGTH + 1)},
        format="json",
    )
    assert resp.status_code == 400, resp.content
    assert "body" in resp.json()
    # Nothing overlong is persisted.
    assert not Post.objects.filter(thread__activity=activity).exists()

    # A body exactly at the cap is accepted.
    ok = _client(adult).post(
        f"/api/social/activities/{activity.id}/posts/",
        {"body": "y" * POST_BODY_MAX_LENGTH},
        format="json",
    )
    assert ok.status_code == 201, ok.content


def test_activity_description_too_long_rejected(adult, place, activity_type, now):
    resp = _client(adult).post(
        "/api/social/activities/",
        {
            "place": place.id,
            "activity_type": activity_type.id,
            "title": "Evening run",
            "description": "d" * (ACTIVITY_DESCRIPTION_MAX_LENGTH + 1),
            "starts_at": now.isoformat(),
        },
        format="json",
    )
    assert resp.status_code == 400, resp.content
    assert "description" in resp.json()


# --- list bounding ---
def test_thread_posts_list_is_bounded(settings, adult, place, activity_type, now):
    settings.SOCIAL_THREAD_POST_LIMIT = 5
    activity = create_activity(
        adult, place=place, activity_type=activity_type, title="Hike", starts_at=now
    )
    for i in range(12):
        Post.objects.create(thread=activity.thread, author=adult, body=f"post {i}")
    resp = _client(adult).get(f"/api/social/activities/{activity.id}/posts/")
    assert resp.status_code == 200, resp.content
    body = resp.json()
    assert len(body) == 5
    # Newest-N, returned oldest-first for display: the last item is the most recent.
    assert body[-1]["body"] == "post 11"


def test_mine_membership_list_is_bounded(settings, adult, place, activity_type, now):
    settings.SOCIAL_MEMBERSHIP_LIST_LIMIT = 3
    # Each created activity makes `adult` an owner-member, so 7 activities ⇒ 7 rows.
    for i in range(7):
        create_activity(
            adult, place=place, activity_type=activity_type, title=f"A{i}", starts_at=now
        )
    assert Membership.objects.filter(user=adult).count() == 7
    resp = _client(adult).get("/api/social/activities/mine/")
    assert resp.status_code == 200, resp.content
    assert len(resp.json()) == 3


# --- lifecycle / edit over the API (F1/F2) ---


def test_owner_can_edit_activity_via_patch(adult, place, activity_type):
    activity = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Old",
        starts_at=timezone.now() + timedelta(days=1),
    )
    resp = _client(adult).patch(
        f"/api/social/activities/{activity.id}/", {"title": "New name"}, format="json"
    )
    assert resp.status_code == 200, resp.content
    activity.refresh_from_db()
    assert activity.title == "New name"


def test_non_owner_cannot_patch_activity(adult, adult2, place, activity_type):
    activity = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Keep",
        starts_at=timezone.now() + timedelta(days=1),
    )
    resp = _client(adult2).patch(
        f"/api/social/activities/{activity.id}/", {"title": "hijack"}, format="json"
    )
    # adult2 shares the cohort (can see it) but isn't the owner → forbidden.
    assert resp.status_code == 403, resp.content
    activity.refresh_from_db()
    assert activity.title == "Keep"


def test_owner_can_cancel_via_api(adult, place, activity_type, now):
    activity = create_activity(
        adult, place=place, activity_type=activity_type, title="Run", starts_at=now
    )
    resp = _client(adult).post(
        f"/api/social/activities/{activity.id}/cancel/", {"reason": "weather"}, format="json"
    )
    assert resp.status_code == 200, resp.content
    activity.refresh_from_db()
    assert activity.status == Activity.Status.CANCELLED
