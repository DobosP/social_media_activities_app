"""F18 — mirror meetup logistics (meeting point, end time, getting-home note) onto the
read-only guardian manifest for CHILD wards only (teens self-manage), and show the new
getting_home_note in the member-gated logistics card.

The manifest stays read-only (no reply channel = no adult↔minor contact path) and keyed on
the ACTIVE GuardianRelationship the wards query already uses.
"""

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
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name, band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _type(slug="f18-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="f18-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _place(name="Court"):
    return Place.objects.create(
        name=name, location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _activity(owner, band_slug, **kw):
    now = timezone.now()
    return create_activity(
        owner,
        place=_place(kw.pop("place_name", "Court")),
        activity_type=_type(band_slug),
        title="Pickup game",
        starts_at=now + timedelta(days=1),
        ends_at=now + timedelta(days=1, hours=2),
        meeting_point="North gate by the fountain",
        getting_home_note="Bus 25 home; parent pickup at 8pm",
        **kw,
    )


def test_child_ward_manifest_mirrors_logistics():
    guardian = _user("f18_g")
    ward = _user("f18_child", AgeBand.UNDER_16, consented=True)
    owner = _user("f18_cowner", AgeBand.UNDER_16, consented=True)  # same CHILD cohort
    link_guardian(guardian, ward)
    activity = _activity(owner, "f18-child-type")
    _member(activity, ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert "North gate by the fountain" in body  # meeting_point mirrored
    assert "Bus 25 home; parent pickup at 8pm" in body  # getting_home_note mirrored
    assert "Ends:" in body  # ends_at mirrored


def test_teen_ward_manifest_omits_extra_logistics():
    guardian = _user("f18_gt")
    ward = _user("f18_teen", AgeBand.AGE_16_17)
    owner = _user("f18_towner", AgeBand.AGE_16_17)  # same TEEN cohort
    link_guardian(guardian, ward)
    activity = _activity(owner, "f18-teen-type")
    _member(activity, ward)

    body = _client(guardian).get("/wards/").content.decode()
    # The basic meetup line still shows (type/place)...
    assert "Basketball" in body
    # ...but teens self-manage: the extra owner-curated logistics are NOT mirrored.
    assert "North gate by the fountain" not in body
    assert "Bus 25 home; parent pickup at 8pm" not in body


def test_non_guardian_does_not_see_ward_logistics():
    guardian = _user("f18_realg")
    stranger = _user("f18_stranger")
    ward = _user("f18_child2", AgeBand.UNDER_16, consented=True)
    owner = _user("f18_cowner2", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, ward)  # only `guardian` is linked
    activity = _activity(owner, "f18-child-type2")
    _member(activity, ward)

    body = _client(stranger).get("/wards/").content.decode()
    assert "Bus 25 home; parent pickup at 8pm" not in body  # not this user's ward


def test_member_sees_getting_home_note_on_activity_detail():
    owner = _user("f18_aowner")
    member = _user("f18_amember")
    outsider = _user("f18_aoutsider")
    activity = _activity(owner, "f18-adult-type")
    _member(activity, member)

    member_body = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert "Bus 25 home; parent pickup at 8pm" in member_body
    # A non-member doesn't get the member-gated logistics card.
    outsider_body = _client(outsider).get(f"/activities/{activity.id}/").content.decode()
    assert "Bus 25 home; parent pickup at 8pm" not in outsider_body


def test_create_form_seeds_getting_home_note_for_minor():
    minor = _user("f18_minor_org", AgeBand.UNDER_16, consented=True)
    atype = _type("f18-seed-type")
    place = _place("Central Park")
    resp = _client(minor).get(f"/activities/new/?place={place.id}&activity_type={atype.id}")
    assert resp.status_code == 200
    assert resp.context["form"].initial.get("getting_home_note")  # seeded for a minor organiser


def test_create_form_no_getting_home_seed_for_adult():
    adult = _user("f18_adult_org")
    atype = _type("f18-seed-type2")
    place = _place("Central Park")
    resp = _client(adult).get(f"/activities/new/?place={place.id}&activity_type={atype.id}")
    assert resp.status_code == 200
    assert not resp.context["form"].initial.get("getting_home_note")
