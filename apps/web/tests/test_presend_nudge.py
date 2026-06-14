"""F33 — the pre-send nudge is wired into the member composer (and only there).

The activity_detail page must emit the shared ruleset (json_script) + the client module for a
member who has the compose form, and must NOT for a non-member (who has no composer at all).
"""

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
PW = "sup3r-secret-pw"
CONFIG_ID = 'id="presend-nudge"'
SCRIPT_SRC = "js/presend-nudge.js"


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="f33-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="f33-bball", defaults={"name": "Basketball", "category": cat}
    )
    return t


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner):
    return create_activity(
        owner,
        place=Place.objects.create(
            name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
        ),
        activity_type=_type(),
        title="Pickup game",
        starts_at=timezone.now(),
    )


def test_member_composer_includes_the_nudge():
    owner = _user("f33_owner")
    member = _user("f33_member")
    activity = _activity(owner)
    activity.memberships.create(
        user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )

    html = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert CONFIG_ID in html  # the shared ruleset is emitted
    assert SCRIPT_SRC in html  # the client module is loaded
    # The ruleset payload reached the page (not an empty blob).
    assert '"key": "phone"' in html or '"key":"phone"' in html


def test_non_member_gets_no_composer_and_no_nudge():
    owner = _user("f33_owner2")
    outsider = _user("f33_outsider")
    activity = _activity(owner)

    html = _client(outsider).get(f"/activities/{activity.id}/").content.decode()
    assert CONFIG_ID not in html
    assert SCRIPT_SRC not in html
