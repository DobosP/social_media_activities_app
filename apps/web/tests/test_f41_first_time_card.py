"""F41 — a member-gated "First time here?" card. ONE owner-curated Activity.first_time_note,
routed through the same edit path as the other logistics, rendered behind the is_member wall
(like getting_home_note — deliberately NOT on the cohort-wide ActivitySerializer), with a calm
accent during the F39 welcome window. It lowers the drop-at-the-door barrier without becoming a
public discovery/vanity surface.
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Membership
from apps.social.serializers import ActivitySerializer
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
NOTE = "Look for the folks in red bibs by the north gate; we warm up together first."
HEADING = "First time here?"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="f41-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug="f41-bball", defaults={"name": "Basketball", "category": cat}
    )
    return t


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner, *, note=NOTE):
    return social.create_activity(
        owner,
        place=Place.objects.create(
            name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
        ),
        activity_type=_type(),
        title="Pickup game",
        starts_at=timezone.now() + timedelta(days=1),
        first_time_note=note,
    )


def _member(activity, user, *, welcomed=False):
    m = activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    if welcomed:
        m.welcomed_at = timezone.now()
        m.save(update_fields=["welcomed_at"])
    return m


def test_member_sees_the_first_time_card():
    owner = _user("f41_owner")
    member = _user("f41_member")
    activity = _activity(owner)
    _member(activity, member)
    html = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert HEADING in html and NOTE in html


def test_non_member_does_not_see_the_card():
    owner = _user("f41_owner2")
    outsider = _user("f41_outsider")
    activity = _activity(owner)
    html = _client(outsider).get(f"/activities/{activity.id}/").content.decode()
    assert HEADING not in html and NOTE not in html


def test_card_shows_outside_the_welcome_window_too():
    # Not write-only after 7 days: a member with no (or expired) welcome window still sees it.
    owner = _user("f41_owner3")
    member = _user("f41_member3")
    activity = _activity(owner)
    _member(activity, member, welcomed=False)  # show_welcome is False
    html = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert NOTE in html


def test_empty_note_renders_no_card():
    owner = _user("f41_owner4")
    member = _user("f41_member4")
    activity = _activity(owner, note="")
    _member(activity, member)
    html = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert HEADING not in html


def test_first_time_note_is_member_only_not_on_cohort_read_serializer():
    # MUST mirror getting_home_note: a member-only field, never echoed by the cohort-wide read
    # serializer (so it can't leak to non-members via the API).
    owner = _user("f41_owner5")
    activity = _activity(owner)
    data = ActivitySerializer(activity).data
    assert "first_time_note" not in data
    assert "getting_home_note" not in data  # the precedent it mirrors


def test_owner_can_edit_first_time_note_via_the_edit_path():
    owner = _user("f41_owner6")
    activity = _activity(owner, note="old note")
    social.update_activity(owner, activity, first_time_note="brand new arrival note")
    activity.refresh_from_db()
    assert activity.first_time_note == "brand new arrival note"


def test_serializer_caps_first_time_note_at_logistics_max():
    from apps.social.serializers import LOGISTICS_FIELD_MAX_LENGTH, ActivityUpdateSerializer

    s = ActivityUpdateSerializer(data={"first_time_note": "x" * (LOGISTICS_FIELD_MAX_LENGTH + 1)})
    assert not s.is_valid()
    assert "first_time_note" in s.errors
