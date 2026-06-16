"""W2-F5: the organizer console — a self-scoped, read-only digest of everything the viewer runs,
each item tagged with the concrete action it needs now (pending joins, supervisor gap, blank
meeting point near start). Functional flags only — never a per-organizer vanity score."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone
from rest_framework.test import APIClient

from apps.communities.models import Area
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import ActivitySeries, Membership

from .conftest import make_user

pytestmark = pytest.mark.django_db


def _place():
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _activity(owner, activity_type, *, title="Game", days=3, meeting_point=""):
    return social.create_activity(
        owner,
        place=_place(),
        activity_type=activity_type,
        title=title,
        starts_at=timezone.now() + timedelta(days=days),
        meeting_point=meeting_point,
    )


def test_console_lists_owned_activities_with_pending_join_flag(adult, adult2, activity_type):
    a = _activity(adult, activity_type, title="Owned game", meeting_point="Gate")
    a.memberships.create(user=adult2, role=Membership.Role.MEMBER, state=Membership.State.REQUESTED)
    console = social.organizer_console(adult)
    row = next(r for r in console["activities"] if r["activity"].id == a.id)
    assert row["pending_joins"] == 1
    assert row["needs_supervisor"] is False
    assert row["missing_meeting_point"] is False  # has a meeting point


def test_console_includes_co_organized_excludes_others(adult, adult2, activity_type):
    mine = _activity(adult, activity_type, title="Mine", meeting_point="x")
    theirs = _activity(adult2, activity_type, title="Theirs", meeting_point="x")
    coorg = _activity(adult2, activity_type, title="Co-org", meeting_point="x")
    coorg.memberships.create(
        user=adult, role=Membership.Role.CO_ORGANIZER, state=Membership.State.MEMBER
    )
    ids = {r["activity"].id for r in social.organizer_console(adult)["activities"]}
    assert mine.id in ids  # owned
    assert coorg.id in ids  # co-organised (is_organizer)
    assert theirs.id not in ids  # someone else's — never surfaced


def test_missing_meeting_point_flag_only_within_window(adult, activity_type):
    soon_blank = _activity(adult, activity_type, title="Soon blank", days=1, meeting_point="")
    soon_set = _activity(adult, activity_type, title="Soon set", days=1, meeting_point="North gate")
    far_blank = _activity(adult, activity_type, title="Far blank", days=10, meeting_point="")
    flags = {
        r["activity"].id: r["missing_meeting_point"]
        for r in social.organizer_console(adult)["activities"]
    }
    assert flags[soon_blank.id] is True
    assert flags[soon_set.id] is False  # has a meeting point
    assert flags[far_blank.id] is False  # blank but starts beyond the 48h prep window


def test_needs_supervisor_flag(adult, activity_type):
    a = _activity(adult, activity_type, title="Sup", meeting_point="x")
    a.supervised = True  # an adult owner has no guardian => supervision can never be satisfied
    a.save(update_fields=["supervised"])
    row = next(r for r in social.organizer_console(adult)["activities"] if r["activity"].id == a.id)
    assert row["needs_supervisor"] is True


def test_console_lists_owned_series_and_groups(adult, activity_type):
    series = social.create_series(
        adult,
        place=_place(),
        activity_type=activity_type,
        title="Weekly run",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=timezone.now() + timedelta(days=2),
    )
    area = Area.objects.create(city="Cluj-Napoca", slug="cluj-w2f5", name="Cluj-Napoca")
    staff = make_user("w2f5_staff")
    staff.is_staff = True
    staff.save(update_fields=["is_staff"])
    group = social.create_group(staff, area=area, title="Runners", activity_type=activity_type)

    assert series.id in {s.id for s in social.organizer_console(adult)["series"]}
    assert group.id in {g.id for g in social.organizer_console(staff)["groups"]}
    # adult does not own the group => it's not on their console (staff sees ALL via visible_groups,
    # but the owner=user filter narrows it back to their own — pins the staff-no-dump guard).
    assert group.id not in {g.id for g in social.organizer_console(adult)["groups"]}


def test_console_empty_for_anonymous():
    from django.contrib.auth.models import AnonymousUser

    console = social.organizer_console(AnonymousUser())
    assert console == {"activities": [], "series": [], "groups": []}


def test_web_organize_renders_and_requires_login(adult, activity_type):
    _activity(adult, activity_type, title="Console game", meeting_point="x")
    assert Client().get("/organize/").status_code in (301, 302)  # login required
    c = Client()
    c.force_login(adult)
    html = c.get("/organize/").content.decode()
    assert "Run my meetups" in html and "Console game" in html


def test_drf_organizer_console_parity(adult, adult2, activity_type):
    a = _activity(adult, activity_type, title="Api game", meeting_point="x")
    a.memberships.create(user=adult2, role=Membership.Role.MEMBER, state=Membership.State.REQUESTED)
    assert APIClient().get("/api/social/organizer-console/").status_code in (401, 403)
    api = APIClient()
    api.force_authenticate(adult)
    data = api.get("/api/social/organizer-console/").json()
    row = next(r for r in data["activities"] if r["id"] == a.id)
    assert row["pending_joins"] == 1 and row["title"] == "Api game"


def test_console_excludes_cancelled_completed_and_past_activities(adult, activity_type):
    from apps.social.models import Activity

    live = _activity(adult, activity_type, title="Live", meeting_point="x")
    cancelled = _activity(adult, activity_type, title="Cancelled", meeting_point="x")
    cancelled.status = Activity.Status.CANCELLED
    cancelled.save(update_fields=["status"])
    completed = _activity(adult, activity_type, title="Completed", meeting_point="x")
    completed.status = Activity.Status.COMPLETED
    completed.save(update_fields=["status"])
    past = _activity(adult, activity_type, title="Past", meeting_point="x")
    Activity.objects.filter(pk=past.pk).update(starts_at=timezone.now() - timedelta(hours=1))

    ids = {r["activity"].id for r in social.organizer_console(adult)["activities"]}
    assert live.id in ids
    assert cancelled.id not in ids  # only OPEN activities are surfaced
    assert completed.id not in ids
    assert past.id not in ids  # only upcoming


# --- W3-F5: prep readiness + quorum line + venue-health flag ---------------------------


def test_readiness_what_to_bring_flag(adult, activity_type):
    blank = _activity(adult, activity_type, title="Blank", meeting_point="x")
    filled = social.create_activity(
        adult,
        place=_place(),
        activity_type=activity_type,
        title="Filled",
        starts_at=timezone.now() + timedelta(days=3),
        meeting_point="x",
        what_to_bring="Water and trainers",
    )
    rows = {r["activity"].id: r for r in social.organizer_console(adult)["activities"]}
    assert rows[blank.id]["readiness"]["missing_what_to_bring"] is True
    assert rows[filled.id]["readiness"]["missing_what_to_bring"] is False


def test_readiness_near_capacity(adult, adult2, activity_type):
    full = social.create_activity(
        adult,
        place=_place(),
        activity_type=activity_type,
        title="Full",
        starts_at=timezone.now() + timedelta(days=3),
        meeting_point="x",
        capacity=2,  # owner is auto-seated (1); one more fills it
    )
    full.memberships.create(user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER)
    roomy = social.create_activity(
        adult,
        place=_place(),
        activity_type=activity_type,
        title="Roomy",
        starts_at=timezone.now() + timedelta(days=3),
        meeting_point="x",
        capacity=5,  # owner only (1) -> not near capacity
    )
    rows = {r["activity"].id: r for r in social.organizer_console(adult)["activities"]}
    assert rows[full.id]["readiness"]["near_capacity"] is True
    assert rows[roomy.id]["readiness"]["near_capacity"] is False


def test_readiness_missing_getting_home_is_child_only(adult, activity_type):
    from apps.accounts.models import Cohort

    child_act = _activity(adult, activity_type, title="Child act", meeting_point="x")
    child_act.cohort = Cohort.CHILD  # exercise the CHILD branch without full minor onboarding
    child_act.save(update_fields=["cohort"])
    adult_act = _activity(adult, activity_type, title="Adult act", meeting_point="x")
    rows = {r["activity"].id: r for r in social.organizer_console(adult)["activities"]}
    assert rows[child_act.id]["readiness"]["missing_getting_home"] is True  # CHILD + blank
    assert rows[adult_act.id]["readiness"]["missing_getting_home"] is False  # not a CHILD meetup


def test_quorum_line_needs_n_more(adult, adult2, activity_type):
    a = social.create_activity(
        adult,
        place=_place(),
        activity_type=activity_type,
        title="Quorum",
        starts_at=timezone.now() + timedelta(days=3),
        meeting_point="x",
        min_to_go=3,
    )
    a.memberships.create(user=adult2, role=Membership.Role.MEMBER, state=Membership.State.MEMBER)
    social.set_attendance_intent(adult2, a, Membership.AttendanceIntent.GOING)
    row = next(r for r in social.organizer_console(adult)["activities"] if r["activity"].id == a.id)
    q = row["quorum"]
    assert q["going"] == 1 and q["min_to_go"] == 3
    assert q["remaining_needed"] == 2  # one going, needs 3 -> 2 more
    assert q["met_minimum"] is False


def test_quorum_line_none_when_no_minimum(adult, activity_type):
    a = _activity(adult, activity_type, title="No quorum", meeting_point="x")  # min_to_go unset
    row = next(r for r in social.organizer_console(adult)["activities"] if r["activity"].id == a.id)
    assert row["quorum"]["remaining_needed"] is None  # None-safe, no lying chip
    assert row["quorum"]["min_to_go"] is None


def test_venue_flag_when_hours_unreliable(adult, activity_type):
    from apps.places.models import OpenNowReport

    flagged = _activity(adult, activity_type, title="Flagged", meeting_point="x")
    # The default threshold is 3 recent reports -> hours unreliable -> a "check this venue" task.
    for i in range(3):
        OpenNowReport.objects.create(
            place=flagged.place, reporter=make_user(f"rep_{flagged.id}_{i}")
        )
    clean = _activity(adult, activity_type, title="Clean", meeting_point="x")  # no reports
    rows = {r["activity"].id: r for r in social.organizer_console(adult)["activities"]}
    assert rows[flagged.id]["venue_flag"] is True
    assert rows[clean.id]["venue_flag"] is False


def test_console_query_count_constant_regardless_of_size(adult, activity_type):
    # The load-bearing N+1 guard: every per-row read (GOING/total counts + venue report count) is a
    # batched annotation, so adding activities adds NO queries. A per-row attendance_summary() or
    # hours_reliable() query would make the larger console cost strictly more.
    from django.db import connection
    from django.test.utils import CaptureQueriesContext

    from apps.places.models import OpenNowReport

    def populate(count):
        for _ in range(count):
            a = _activity(adult, activity_type, title="Q", meeting_point="x")
            m = make_user(f"qm_{a.id}")
            a.memberships.create(user=m, role=Membership.Role.MEMBER, state=Membership.State.MEMBER)
            OpenNowReport.objects.create(place=a.place, reporter=m)

    populate(2)
    with CaptureQueriesContext(connection) as small:
        social.organizer_console(adult)
    populate(4)  # 6 activities total
    with CaptureQueriesContext(connection) as large:
        social.organizer_console(adult)
    assert len(large.captured_queries) == len(small.captured_queries)


def test_drf_console_includes_readiness_quorum_venue(adult, activity_type):
    a = _activity(adult, activity_type, title="Api row", meeting_point="x")
    api = APIClient()
    api.force_authenticate(adult)
    row = next(
        r for r in api.get("/api/social/organizer-console/").json()["activities"] if r["id"] == a.id
    )
    assert "readiness" in row and "quorum" in row and "venue_flag" in row
    assert "missing_what_to_bring" in row["readiness"]
    assert "remaining_needed" in row["quorum"]


def test_console_excludes_ended_series_keeps_active(adult, activity_type):
    active = social.create_series(
        adult,
        place=_place(),
        activity_type=activity_type,
        title="Active series",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=timezone.now() + timedelta(days=2),
    )
    ended = social.create_series(
        adult,
        place=_place(),
        activity_type=activity_type,
        title="Ended series",
        cadence=ActivitySeries.Cadence.WEEKLY,
        first_starts_at=timezone.now() + timedelta(days=2),
    )
    ended.status = ActivitySeries.Status.ENDED
    ended.save(update_fields=["status"])
    ids = {s.id for s in social.organizer_console(adult)["series"]}
    assert active.id in ids
    assert ended.id not in ids  # a dead series needs nothing — off the console
