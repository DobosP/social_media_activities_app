"""W2-F34 (web): the calm "who can see this" line renders above the composer for a member, with
the explicit negatives — and never for a non-member (the composer itself is member-gated)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="f34-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="f34-run", defaults={"name": "Running", "category": cat}
    )
    place = Place.objects.create(
        name="Park", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Run",
        starts_at=timezone.now() + timedelta(hours=2),
    )


def test_member_sees_audience_line_with_negatives():
    owner = _user("f34owner")
    member = _user("f34member")
    activity = _activity(owner)
    activity.memberships.create(
        user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    c = Client()
    c.force_login(member)
    page = c.get(f"/activities/{activity.id}/").content.decode()
    assert "Visible to members of this activity only." in page
    assert "Never public, never indexed, never shown to other cohorts." in page
    assert "other person can see this." in page  # an adult viewer sees the (suppressible) count


def test_non_member_never_sees_the_audience_line():
    owner = _user("f34owner2")
    outsider = _user("f34outsider")
    activity = _activity(owner)
    c = Client()
    c.force_login(outsider)
    page = c.get(f"/activities/{activity.id}/").content.decode()
    assert "Never public, never indexed" not in page  # gated with the member-only composer
