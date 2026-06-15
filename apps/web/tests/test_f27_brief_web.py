"""W2-F27 (web): the read-aloud brief renders in an ARIA-landmarked region on activity_detail,
with member-only logistics gated by the same membership wall as the rest of the page."""

import re
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.models import Activity, Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="f27-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="f27-run", defaults={"name": "Running", "category": cat}
    )
    place = Place.objects.create(
        name="Central Park", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Morning Run",
        starts_at=timezone.now() + timedelta(hours=2),
        cost_band=Activity.CostBand.FREE,
        meeting_point="By the SECRET north gate",
    )


def test_brief_region_renders_and_gates_logistics():
    owner = _user("f27owner")
    member = _user("f27member")
    outsider = _user("f27outsider")
    activity = _activity(owner)
    activity.memberships.create(
        user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )

    # A non-member sees the brief region + cohort-visible facts, but NOT the meeting point.
    c_out = Client()
    c_out.force_login(outsider)
    page = c_out.get(f"/activities/{activity.id}/").content.decode()
    assert 'aria-labelledby="brief-heading"' in page
    assert "At a glance" in page
    assert "SECRET north gate" not in page

    # A member sees the meeting point inside the brief REGION specifically (scoped so the
    # assertion proves the brief renders it, not the separate member-only logistics card).
    c_mem = Client()
    c_mem.force_login(member)
    member_page = c_mem.get(f"/activities/{activity.id}/").content.decode()
    region = re.search(r'aria-labelledby="brief-heading".*?</section>', member_page, re.S)
    assert region and "SECRET north gate" in region.group(0)
