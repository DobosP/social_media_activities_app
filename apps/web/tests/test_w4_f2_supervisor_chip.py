"""W4-F2: the /wards/ manifest shows the LIVE supervision state of a child's supervised meetup,
not a static flag — so a parent isn't falsely reassured that an adult is present when no
supervisor is actually seated (or after one leaves)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import add_guardian, create_activity, leave_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
SEATED = "guardian-supervised"
NO_SEAT = "no adult seated yet"


def _user(name, band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _type(slug):
    cat, _ = ActivityCategory.objects.get_or_create(slug="w4f2-sport", defaults={"name": "Sport"})
    return ActivityType.objects.get_or_create(
        slug=f"w4f2-{slug}", defaults={"name": "Basketball", "category": cat}
    )[0]


def _place():
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _meetup(owner, slug, *, supervised):
    return create_activity(
        owner,
        place=_place(),
        activity_type=_type(slug),
        title="Kids meetup",
        starts_at=timezone.now() + timedelta(days=1),
        supervised=supervised,
    )


def test_supervised_without_a_seated_supervisor_shows_honest_chip():
    guardian = _user("f2_g")
    ward = _user("f2_w", AgeBand.UNDER_16, consented=True)
    owner = _user("f2_o", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, ward)
    _member(_meetup(owner, "a", supervised=True), ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert NO_SEAT in body  # the honest "no adult seated yet" state
    assert SEATED not in body  # NOT the falsely-reassuring static chip


def test_supervised_with_a_seated_supervisor_shows_supervised_chip():
    guardian = _user("f2_g2")
    ward = _user("f2_w2", AgeBand.UNDER_16, consented=True)
    owner = _user("f2_o2", AgeBand.UNDER_16, consented=True)
    owner_guardian = _user("f2_og2")  # ADULT guardian OF THE OWNER (the eligible supervisor)
    link_guardian(guardian, ward)
    link_guardian(owner_guardian, owner)
    activity = _meetup(owner, "b", supervised=True)
    _member(activity, ward)
    add_guardian(owner, activity, owner_guardian)  # seat the supervisor -> live state flips true

    body = _client(guardian).get("/wards/").content.decode()
    assert SEATED in body
    assert NO_SEAT not in body


def test_chip_flips_back_when_the_supervisor_leaves():
    # The whole point: the chip is render-time live, so it can never keep claiming "supervised"
    # after the seated guardian leaves.
    guardian = _user("f2_g3")
    ward = _user("f2_w3", AgeBand.UNDER_16, consented=True)
    owner = _user("f2_o3", AgeBand.UNDER_16, consented=True)
    owner_guardian = _user("f2_og3")
    link_guardian(guardian, ward)
    link_guardian(owner_guardian, owner)
    activity = _meetup(owner, "c", supervised=True)
    _member(activity, ward)
    add_guardian(owner, activity, owner_guardian)
    assert SEATED in _client(guardian).get("/wards/").content.decode()

    leave_activity(owner_guardian, activity)  # supervisor leaves
    body = _client(guardian).get("/wards/").content.decode()
    assert NO_SEAT in body
    assert SEATED not in body


def test_non_supervised_meetup_shows_no_supervision_chip():
    guardian = _user("f2_g4")
    ward = _user("f2_w4", AgeBand.UNDER_16, consented=True)
    owner = _user("f2_o4", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, ward)
    _member(_meetup(owner, "d", supervised=False), ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert SEATED not in body
    assert NO_SEAT not in body
