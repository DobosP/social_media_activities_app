"""W1 search: free-text activity/event/thread search stays behind the SAME gates as every
other read surface (visible_activities cohort+block wall, can_read_thread, the F25
pending-place gate), is bounded, and never ranks by popularity (soonest-first only).
E2EE direct messages are structurally unsearchable server-side — only conversation
METADATA (title/participants) is filterable, pinned here too."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand
from apps.events.models import Event
from apps.events.services import search_events
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Activity, Membership, UserPlaceProposal

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _activity(owner, *, place, activity_type, title="Pickup basketball", **kw):
    return social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title=title,
        starts_at=kw.pop("starts_at", timezone.now() + timedelta(days=1)),
        **kw,
    )


# --- search_activities ----------------------------------------------------------------


def test_search_matches_title_description_place_and_type(place, activity_type):
    owner = make_user("s-owner")
    a1 = _activity(owner, place=place, activity_type=activity_type, title="Morning run")
    a2 = _activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Casual game",
        description="friendly basketball for beginners",
    )
    assert list(social.search_activities(owner, "morning")) == [a1]
    # description match
    assert a2 in set(social.search_activities(owner, "friendly"))
    # venue-name match ("Community Hall" fixture)
    assert {a1, a2} <= set(social.search_activities(owner, "community hall"))
    # activity-type-name match ("Basketball")
    assert a2 in set(social.search_activities(owner, "basketball"))


def test_search_is_cohort_isolated(place, activity_type):
    adult = make_user("s-adult")
    child = make_user("s-child", AgeBand.UNDER_16, consented=True)
    _activity(adult, place=place, activity_type=activity_type, title="Unmistakable zebra meetup")
    assert list(social.search_activities(child, "zebra")) == []


def test_search_excludes_blocked_owner(place, activity_type):
    from apps.safety.services import block_user

    owner = make_user("s-blockee")
    searcher = make_user("s-blocker")
    _activity(owner, place=place, activity_type=activity_type, title="Quokka picnic")
    block_user(searcher, owner)
    assert list(social.search_activities(searcher, "quokka")) == []


def test_search_min_length_and_lifecycle_filters(place, activity_type):
    owner = make_user("s-life")
    open_future = _activity(owner, place=place, activity_type=activity_type, title="Aardvark walk")
    cancelled = _activity(owner, place=place, activity_type=activity_type, title="Aardvark swim")
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    # 1-char query returns nothing (noise + cheap probe surface)
    assert list(social.search_activities(owner, "a")) == []
    found = list(social.search_activities(owner, "aardvark"))
    assert found == [open_future]


def test_search_composes_with_beginners_filter(place, activity_type):
    owner = make_user("s-beg")
    plain = _activity(owner, place=place, activity_type=activity_type, title="Walrus jog")
    friendly = _activity(owner, place=place, activity_type=activity_type, title="Walrus stroll")
    friendly.beginners_welcome = True
    friendly.save(update_fields=["beginners_welcome"])
    assert list(social.search_activities(owner, "walrus", beginners=True)) == [friendly]
    assert {plain, friendly} == set(social.search_activities(owner, "walrus"))


# --- search_thread_posts ---------------------------------------------------------------


def test_thread_search_member_only_and_hides_hidden(place, activity_type):
    owner = make_user("t-owner")
    member = make_user("t-member")
    outsider = make_user("t-outsider")
    activity = _activity(owner, place=place, activity_type=activity_type)
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    hit = social.post_to_thread(member, activity, "bring the orange cones")
    social.post_to_thread(owner, activity, "see you there")
    hidden = social.post_to_thread(owner, activity, "orange juice afterwards")
    hidden.is_hidden = True
    hidden.save(update_fields=["is_hidden"])

    results = list(social.search_thread_posts(member, activity, "orange"))
    assert results == [hit]
    with pytest.raises(social.NotEligible):
        social.search_thread_posts(outsider, activity, "orange")


# --- events search + the F25 pending-place gate on the web events surface --------------


def _pending_place():
    p = Place.objects.create(
        name="Secret Pending Venue",
        location=Point(23.61, 46.78, srid=4326),
        source=Place.Source.USER,
    )
    UserPlaceProposal.objects.create(
        place=p, proposer=make_user("ev-proposer"), status=UserPlaceProposal.Status.PENDING
    )
    return p


def test_search_events_matches_and_hides_pending_place(place):
    visible = Event.objects.create(
        title="Chess night", starts_at=timezone.now() + timedelta(days=2), place=place
    )
    Event.objects.create(
        title="Chess at the secret venue",
        starts_at=timezone.now() + timedelta(days=2),
        place=_pending_place(),
    )
    found = list(search_events("chess"))
    assert found == [visible]
    # the pending place's name must not be findable through its event either
    assert list(search_events("secret pending")) == []


def test_web_events_list_hides_pending_place_event(client, place):
    user = make_user("ev-web")
    client.force_login(user)
    Event.objects.create(
        title="Open evening", starts_at=timezone.now() + timedelta(days=1), place=place
    )
    Event.objects.create(
        title="Leaky evening",
        starts_at=timezone.now() + timedelta(days=1),
        place=_pending_place(),
    )
    page = client.get("/events/").content.decode()
    assert "Open evening" in page
    assert "Leaky evening" not in page
    assert "Secret Pending Venue" not in page


# --- web + API search bars -------------------------------------------------------------


def test_web_activity_list_search(client, place, activity_type):
    user = make_user("s-web")
    _activity(user, place=place, activity_type=activity_type, title="Lighthouse hike")
    _activity(user, place=place, activity_type=activity_type, title="Couch reading")
    client.force_login(user)
    page = client.get("/activities/", {"q": "lighthouse"}).content.decode()
    assert "Lighthouse hike" in page
    assert "Couch reading" not in page


def test_api_activity_list_search_is_cohort_gated(place, activity_type):
    adult = make_user("s-api-adult")
    child = make_user("s-api-child", AgeBand.UNDER_16, consented=True)
    _activity(adult, place=place, activity_type=activity_type, title="Falcon watching")
    api = APIClient()
    api.force_authenticate(adult)
    results = api.get("/api/social/activities/", {"q": "falcon"}).json()["results"]
    assert [a["title"] for a in results] == ["Falcon watching"]
    api.force_authenticate(child)
    assert api.get("/api/social/activities/", {"q": "falcon"}).json()["results"] == []


def test_api_conversations_metadata_search(place, activity_type):
    from apps.messaging import services as messaging

    a = make_user("conv-a")
    b = make_user("conv-b")
    c = make_user("conv-c")
    messaging.start_group(a, [b], title="Weekend hikers")
    messaging.start_group(a, [c], title="Book club")
    api = APIClient()
    api.force_authenticate(a)
    found = api.get("/api/messaging/conversations/", {"q": "hikers"}).json()
    assert [conv["title"] for conv in found] == ["Weekend hikers"]
