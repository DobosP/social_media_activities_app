"""W4-F5: "set up another like this" — clone the organiser's OWN past meetup into a new create
form (prefill only). The ownership gate lives in draft_from_activity (a tampered ?from= injects
nothing); starts_at is never copied; create_activity re-validates everything on submit.
"""

from datetime import timedelta

import pytest
from django.test import Client

from apps.social.models import Activity
from apps.social.services import create_activity, draft_from_activity

pytestmark = pytest.mark.django_db


def _source(owner, place, activity_type, now, **kw):
    return create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Tuesday football",
        starts_at=now + timedelta(days=1),
        meeting_point="North gate by the fountain",
        what_to_bring="Boots",
        beginners_welcome=True,
        capacity=10,
        min_to_go=3,
        **kw,
    )


def test_draft_from_activity_prefills_whitelisted_fields_for_owner(
    adult, place, activity_type, now
):
    source = _source(adult, place, activity_type, now)
    prefill = draft_from_activity(adult, source)
    assert prefill["title"] == "Tuesday football"
    assert prefill["meeting_point"] == "North gate by the fountain"
    assert prefill["what_to_bring"] == "Boots"
    assert prefill["beginners_welcome"] is True
    assert prefill["capacity"] == 10
    assert prefill["min_to_go"] == 3
    assert prefill["place"] == place.id
    assert prefill["activity_type"] == activity_type.id
    # A clone is a NEW occurrence — the organiser picks the time; never copy the old one.
    assert "starts_at" not in prefill


def test_draft_from_activity_returns_empty_for_non_organizer(
    adult, adult2, place, activity_type, now
):
    source = _source(adult, place, activity_type, now)
    # adult2 didn't organise it -> a tampered ?from= pointing here injects nothing.
    assert draft_from_activity(adult2, source) == {}


def test_clone_link_seeds_the_create_form(adult, place, activity_type, now):
    source = _source(adult, place, activity_type, now)
    client = Client()
    client.force_login(adult)
    resp = client.get(f"/activities/new/?from={source.id}")
    assert resp.status_code == 200
    initial = resp.context["form"].initial
    assert initial.get("title") == "Tuesday football"
    assert initial.get("meeting_point") == "North gate by the fountain"
    assert str(initial.get("activity_type")) == str(activity_type.id)
    assert str(initial.get("place")) == str(place.id)
    assert not initial.get("starts_at")  # NOT cloned


def test_clone_from_another_users_activity_injects_nothing(
    adult, adult2, place, activity_type, now
):
    source = create_activity(
        adult,
        place=place,
        activity_type=activity_type,
        title="Secret session",
        starts_at=now + timedelta(days=1),
        meeting_point="Members-only gate",
    )
    client = Client()
    client.force_login(adult2)  # NOT the organiser
    resp = client.get(f"/activities/new/?from={source.id}")
    assert resp.status_code == 200
    assert resp.context["form"].initial.get("meeting_point") != "Members-only gate"
    assert "Secret session" not in resp.content.decode()


def test_clone_does_not_copy_starts_at_into_the_form(adult, place, activity_type, now):
    source = _source(adult, place, activity_type, now)
    client = Client()
    client.force_login(adult)
    initial = client.get(f"/activities/new/?from={source.id}").context["form"].initial
    # The source starts tomorrow; the clone form must not pre-fill that stale time.
    assert not initial.get("starts_at")


def test_cloned_form_still_creates_through_the_full_gate(adult, place, activity_type, now):
    # Prefill is just initial data; submitting still goes through create_activity (a new
    # cohort-pinned Activity), proving the clone path can't bypass the create gate.
    source = _source(adult, place, activity_type, now)
    before = Activity.objects.count()
    client = Client()
    client.force_login(adult)
    initial = client.get(f"/activities/new/?from={source.id}").context["form"].initial
    resp = client.post(
        "/activities/new/",
        {
            "place": initial["place"],
            "activity_type": initial["activity_type"],
            "title": initial["title"],
            "description": initial.get("description", ""),
            "starts_at": (now + timedelta(days=8)).strftime("%Y-%m-%dT%H:%M"),
            "meeting_point": initial.get("meeting_point", ""),
            "what_to_bring": initial.get("what_to_bring", ""),
            "organizer_note": initial.get("organizer_note", ""),
            "getting_home_note": initial.get("getting_home_note", ""),
            "cost_band": initial.get("cost_band", ""),
            "difficulty": initial.get("difficulty", ""),
            "accessibility_notes": initial.get("accessibility_notes", ""),
        },
    )
    assert resp.status_code == 302  # created -> redirect to the new activity
    assert Activity.objects.count() == before + 1


def test_co_organizer_may_clone(adult, adult2, place, activity_type, now):
    # The gate's second arm: a co-organiser (is_organizer True, but not the owner) may also clone.
    from apps.social.models import Membership
    from apps.social.services import grant_co_organizer

    source = _source(adult, place, activity_type, now)
    source.memberships.create(
        user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    grant_co_organizer(adult, source, adult2)
    prefill = draft_from_activity(adult2, source)
    assert prefill["title"] == "Tuesday football"  # a co-organiser may clone, via is_organizer
