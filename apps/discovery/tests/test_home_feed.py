"""W2 unified home feed: one composition (build_home_feed) serves web + API, every
section stays behind its existing gate (visible_activities, visible_groups+membership,
the F25 pending-place event gate), reasons are honest (declared interests only), and
nothing popularity-ranked leaks in."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.communities.models import Area
from apps.discovery.services import group_updates, interest_matched_events
from apps.events.models import Event
from apps.places.models import Place
from apps.recommendations.services import set_interests
from apps.social import services as social
from apps.social.models import UserPlaceProposal
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT, *, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if staff:
        u.is_staff = True
        u.save(update_fields=["is_staff"])
    return u


@pytest.fixture
def basketball():
    cat, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="feed-basketball", defaults={"name": "Basketball", "category": cat}
    )
    return t


@pytest.fixture
def venue(db):
    return Place.objects.create(
        name="Feed Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def test_interest_matched_events_reason_and_fill(basketball, venue):
    user = _user("feed-ev")
    set_interests(user, [basketball.slug])
    matched = Event.objects.create(
        title="Basketball open night",
        starts_at=timezone.now() + timedelta(days=3),
        place=venue,
        activity_type=basketball,
    )
    other = Event.objects.create(
        title="Quiz night", starts_at=timezone.now() + timedelta(days=1), place=venue
    )
    events = interest_matched_events(user, limit=6)
    assert events[0] == matched
    assert "Basketball" in events[0].feed_reason
    filler = next(e for e in events if e.id == other.id)  # same row, fresh instance
    assert filler.feed_reason == ""


def test_interest_matched_events_excludes_pending_place(basketball):
    user = _user("feed-pend")
    set_interests(user, [basketball.slug])
    pending = Place.objects.create(
        name="Pending Gym", location=Point(23.62, 46.78, srid=4326), source=Place.Source.USER
    )
    UserPlaceProposal.objects.create(
        place=pending, proposer=_user("feed-prop"), status=UserPlaceProposal.Status.PENDING
    )
    Event.objects.create(
        title="Hidden game",
        starts_at=timezone.now() + timedelta(days=1),
        place=pending,
        activity_type=basketball,
    )
    assert all(e.title != "Hidden game" for e in interest_matched_events(user))


def test_group_updates_membership_and_hidden_gates(basketball):
    staff = _user("feed-staff", staff=True)
    member = _user("feed-member")
    outsider = _user("feed-out")
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-feed", name="Cluj-Napoca")
    group = social.create_group(staff, area=area, title="Feed Hoops", activity_type=basketball)
    social.join_group(member, group.id)
    ann = social.post_announcement(staff, group, "Court closed next week")
    hidden = social.post_announcement(staff, group, "should vanish")
    hidden.is_hidden = True
    hidden.save(update_fields=["is_hidden"])

    assert [p.id for p in group_updates(member)] == [ann.id]
    assert group_updates(outsider) == []  # not a member → nothing

    group.status = group.Status.ARCHIVED
    group.save(update_fields=["status"])
    assert group_updates(member) == []  # visible_groups gate (ACTIVE only)


def test_feed_api_cohort_isolated_and_shaped(basketball, venue):
    adult = _user("feed-adult")
    organiser = _user("feed-organiser")  # recommendations exclude what you already joined
    child = _user("feed-child", AgeBand.UNDER_16)
    from apps.accounts.models import ParentalConsent

    ParentalConsent.objects.create(
        minor=child, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    social.create_activity(
        organiser,
        place=venue,
        activity_type=basketball,
        title="Adults-only scrimmage",
        starts_at=timezone.now() + timedelta(days=1),
    )
    api = APIClient()
    api.force_authenticate(adult)
    data = api.get("/api/discovery/feed/").json()
    assert set(data) == {"recommended", "beginners", "events", "group_updates"}  # W3-F11 strip
    assert any(a["title"] == "Adults-only scrimmage" for a in data["recommended"])
    # no member counts / popularity fields on any feed card
    assert all("member_n" not in a and "member_count" not in a for a in data["recommended"])

    api.force_authenticate(child)
    child_data = api.get("/api/discovery/feed/").json()
    assert all(a["title"] != "Adults-only scrimmage" for a in child_data["recommended"])


def test_beginner_friendly_filters_orders_and_dedups(basketball, venue):
    from apps.discovery.services import beginner_friendly

    org = _user("bf-org")
    user = _user("bf-user")  # a newcomer (non-member) viewing the cohort's feed
    now = timezone.now()

    def _mk(title, starts_in, *, beginners):
        return social.create_activity(
            org,
            place=venue,
            activity_type=basketball,
            title=title,
            starts_at=now + starts_in,
            beginners_welcome=beginners,
        )

    later = _mk("Beginners later", timedelta(days=3), beginners=True)
    soon = _mk("Beginners soon", timedelta(days=1), beginners=True)
    _mk("Not for beginners", timedelta(hours=2), beginners=False)

    # beginners-welcome only, soonest-first — never the non-beginners (sooner) activity.
    assert [a.id for a in beginner_friendly(user)] == [soon.id, later.id]
    # exclude_ids dedups against cards already shown elsewhere (recommended/upcoming).
    assert [a.id for a in beginner_friendly(user, exclude_ids=[soon.id])] == [later.id]


def test_build_home_feed_beginners_disjoint_from_recommended(basketball, venue):
    from apps.discovery.services import build_home_feed

    org = _user("bf-feed-org")
    user = _user("bf-feed-user")
    social.create_activity(
        org,
        place=venue,
        activity_type=basketball,
        title="Beginners meetup",
        starts_at=timezone.now() + timedelta(days=1),
        beginners_welcome=True,
    )
    feed = build_home_feed(user)
    assert "beginners" in feed  # the W3-F11 section exists on both surfaces
    # The strip is never a second copy of a recommended card.
    rec_ids = {a.id for a in feed["recommended"]}
    beg_ids = {a.id for a in feed["beginners"]}
    assert rec_ids.isdisjoint(beg_ids)


def test_web_home_beginners_strip_shows_and_dedups_upcoming(client, basketball, venue):
    # The beginners-welcome meetup lands in the W3-F11 strip (8 sooner fillers push it out of the
    # recommended(8) block), and is promoted there exactly ONCE — excluded from "Upcoming" below.
    org = _user("bf-web-org")
    user = _user("bf-web-user")  # cold-start newcomer
    now = timezone.now()
    for i in range(8):
        social.create_activity(
            org,
            place=venue,
            activity_type=basketball,
            title=f"Filler {i}",
            starts_at=now + timedelta(hours=i + 1),
            beginners_welcome=False,
        )
    social.create_activity(
        org,
        place=venue,
        activity_type=basketball,
        title="Welcoming hoops",
        starts_at=now + timedelta(days=2),  # rank 9 by start -> out of recommended(8)
        beginners_welcome=True,
    )
    client.force_login(user)
    page = client.get("/").content.decode()
    assert "New here? These welcome beginners" in page
    assert page.count("Welcoming hoops") == 1  # promoted to the strip, not repeated in Upcoming


def test_web_home_renders_feed_sections(client, basketball, venue):
    staff = _user("feed-web-staff", staff=True)
    user = _user("feed-web")
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-feedweb", name="Cluj-Napoca")
    group = social.create_group(staff, area=area, title="Webfeed Hoops", activity_type=basketball)
    social.join_group(user, group.id)
    social.post_announcement(staff, group, "Bring water bottles to practice")
    set_interests(user, [basketball.slug])
    Event.objects.create(
        title="Open basketball evening",
        starts_at=timezone.now() + timedelta(days=2),
        place=venue,
        activity_type=basketball,
    )
    client.force_login(user)
    page = client.get("/").content.decode()
    assert "From your groups" in page
    assert "Bring water bottles" in page
    assert "Open basketball evening" in page
    assert "matches your interest in Basketball" in page
