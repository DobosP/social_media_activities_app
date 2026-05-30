"""F35 (catch-up digest) + F39 (first-timer welcome) + F36 (draft helper) at the service layer."""

from datetime import datetime

import pytest

from apps.accounts.models import Cohort
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import (
    add_guardian,
    create_activity,
    draft_activity_text,
    owner_admit,
    post_announcement,
    post_to_thread,
    request_to_join,
    thread_digest,
)
from apps.taxonomy.models import ActivityCategory, ActivityType

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, place, activity_type, now, **kw):
    return create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=now, **kw
    )


# --- F35: catch-up digest --------------------------------------------------------------


def test_digest_surfaces_content_and_is_conservative(adult, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    post_announcement(adult, activity, "Bring water!")
    post_to_thread(adult, activity, "The meeting point moved to the north gate.")
    post_to_thread(adult, activity, "had a great time last week")  # NOT logistical
    digest = thread_digest(activity)
    assert any("Bring water" in p.body for p in digest["announcements"])
    # The logistics post is surfaced somewhere (here, among the most-recent).
    assert any("north gate" in p.body for p in digest["recent"] + digest["logistical"])
    # Conservatism: the casual post is NEVER classified as logistical (no change/move word).
    assert not any("great time" in p.body for p in digest["logistical"])
    assert digest["has_content"] is True


def test_digest_logistical_surfaces_older_keyword_post(adult, place, activity_type, now):
    import datetime as _dt

    from apps.social.models import Post

    activity = _activity(adult, place, activity_type, now)
    posts = [
        post_to_thread(adult, activity, "We rescheduled to 5pm."),  # oldest, logistical
        post_to_thread(adult, activity, "ok"),
        post_to_thread(adult, activity, "cool"),
        post_to_thread(adult, activity, "nice"),
    ]
    # Force a deterministic chronology so the keyword post falls outside the recent-3 window.
    for i, p in enumerate(posts):
        Post.objects.filter(pk=p.pk).update(created_at=now + _dt.timedelta(minutes=i))
    digest = thread_digest(activity)
    assert any("rescheduled" in p.body for p in digest["logistical"])  # surfaced via keyword
    assert {p.body for p in digest["recent"]} == {"ok", "cool", "nice"}


def test_digest_empty_thread_has_no_content(adult, place, activity_type, now):
    digest = thread_digest(_activity(adult, place, activity_type, now))
    assert digest["has_content"] is False


def test_digest_counts_exclude_guardian_from_total(child, place, activity_type, now):
    guardian = make_user("digestguardian")
    from apps.accounts.services import link_guardian

    link_guardian(guardian, child)
    activity = _activity(child, place, activity_type, now, guardian_accompanied=True)
    add_guardian(child, activity, guardian)
    digest = thread_digest(activity)
    assert digest["member_count"] == 2  # owner + guardian (current_members)
    assert digest["total"] == 1  # participants only (voting_members, guardian excluded)


# --- F39: first-timer welcome ----------------------------------------------------------


def _join_and_admit(owner, joiner, activity):
    m = request_to_join(joiner, activity)
    owner_admit(owner, activity.memberships.get(pk=m.pk))
    m.refresh_from_db()
    return m


def test_first_timer_is_welcomed(adult, adult2, place, activity_type, now):
    activity = _activity(adult, place, activity_type, now)
    m = _join_and_admit(adult, adult2, activity)
    assert m.welcomed_at is not None
    note = Notification.objects.filter(
        recipient=adult2, kind=Notification.Kind.JOIN_APPROVED
    ).latest("id")
    assert "New here" in note.body


def test_returning_member_is_not_welcomed(adult, adult2, place, activity_type, now):
    first = _activity(adult, place, activity_type, now)
    first.memberships.create(
        user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )  # adult2 already has a membership elsewhere
    second = _activity(adult, place, activity_type, now)
    m = _join_and_admit(adult, adult2, second)
    assert m.welcomed_at is None
    note = Notification.objects.filter(
        recipient=adult2, kind=Notification.Kind.JOIN_APPROVED
    ).latest("id")
    assert "New here" not in note.body


# --- F36: draft helper -----------------------------------------------------------------


def _named(type_name="Basketball", place_name="Central Park"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="w6-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="w6-bball", defaults={"name": type_name, "category": cat}
    )
    from django.contrib.gis.geos import Point

    place = Place.objects.create(
        name=place_name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return atype, place


def test_draft_title_and_description():
    atype, place = _named()
    draft = draft_activity_text(
        activity_type=atype,
        place=place,
        starts_at=datetime(2031, 6, 15, 12, 30),
        cohort=Cohort.ADULT,
    )
    assert draft["title"] == "Basketball at Central Park"
    assert "A Basketball meetup at Central Park" in draft["description"]
    assert "Safety:" not in draft["description"]  # adult organiser → no reminder


def test_draft_for_minor_adds_safety_reminder():
    atype, place = _named()
    draft = draft_activity_text(activity_type=atype, place=place, cohort=Cohort.CHILD)
    assert "Safety:" in draft["description"]


def test_draft_without_place_name_uses_type_only_title():
    atype, place = _named(place_name="")  # unnamed/OSM place
    draft = draft_activity_text(activity_type=atype, place=place, cohort=Cohort.ADULT)
    assert draft["title"] == "Basketball"
