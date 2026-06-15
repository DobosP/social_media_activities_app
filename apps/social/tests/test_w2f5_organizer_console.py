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
