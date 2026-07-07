"""F18 — mirror child meetup logistics onto the read-only guardian manifest.

The manifest stays read-only (no reply channel = no adult<->minor contact path), keyed on the
ACTIVE GuardianRelationship the wards query already uses, and mirrors an activity ONLY when its
cohort still matches the ward and it is not moderator-hidden. ADR-0019 removed getting-home and
Plan-B rows from this product surface.
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian, revoke_guardian
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"
MEETING = "North gate by the fountain"
HOME = "Bus 25 home; parent pickup at 8pm"


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


def _activity(owner, slug, *, meeting=MEETING, home=HOME, **kw):
    now = timezone.now()
    return create_activity(
        owner,
        place=_place(kw.pop("place_name", "Court")),
        activity_type=_type(slug),
        title="Pickup game",
        starts_at=now + timedelta(days=1),
        ends_at=now + timedelta(days=1, hours=2),
        meeting_point=meeting,
        getting_home_note=home,
        **kw,
    )


def test_child_ward_manifest_mirrors_logistics():
    guardian = _user("f18_g")
    ward = _user("f18_child", AgeBand.UNDER_16, consented=True)
    owner = _user("f18_cowner", AgeBand.UNDER_16, consented=True)  # same CHILD cohort
    link_guardian(guardian, ward)
    _member(_activity(owner, "f18-child-type"), ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert MEETING in body  # meeting_point mirrored
    assert HOME not in body  # retired row is no longer mirrored
    assert "Ends:" in body  # ends_at mirrored


def test_teen_ward_manifest_omits_extra_logistics():
    guardian = _user("f18_gt")
    ward = _user("f18_teen", AgeBand.AGE_16_17)
    owner = _user("f18_towner", AgeBand.AGE_16_17)  # same TEEN cohort
    link_guardian(guardian, ward)
    plan_b = "Teen plan B: the side entrance"
    _member(_activity(owner, "f18-teen-type", fallback_meeting_point=plan_b), ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert "Basketball" in body  # the basic meetup line still shows (type/place)
    assert MEETING not in body  # ...but teens self-manage: extra logistics not mirrored
    assert HOME not in body
    assert plan_b not in body  # W3-F8: the plan-B spot is CHILD-only, never mirrored to a teen


def test_cross_ward_guardian_does_not_see_other_wards_logistics():
    # Load-bearing isolation: a guardian of ward B (with their OWN child meetup) must not see
    # ward A's retired getting-home row. Exercises the per-ward mirror loop, not an
    # empty-loop no-op.
    g_a = _user("f18_ga")
    ward_a = _user("f18_wa", AgeBand.UNDER_16, consented=True)
    owner_a = _user("f18_oa", AgeBand.UNDER_16, consented=True)
    link_guardian(g_a, ward_a)
    _member(_activity(owner_a, "f18-a", home=HOME), ward_a)

    g_b = _user("f18_gb")
    ward_b = _user("f18_wb", AgeBand.UNDER_16, consented=True)
    owner_b = _user("f18_ob", AgeBand.UNDER_16, consented=True)
    link_guardian(g_b, ward_b)
    _member(_activity(owner_b, "f18-b", home="Different note: walk with neighbour"), ward_b)

    body = _client(g_b).get("/wards/").content.decode()
    assert "Different note: walk with neighbour" not in body  # retired row is hidden
    assert HOME not in body  # the other guardian's ward's note does NOT


def test_revoked_guardian_loses_ward_logistics():
    # SAFETY: visibility keys on an ACTIVE GuardianRelationship — revocation removes the mirror.
    guardian = _user("f18_revg")
    ward = _user("f18_revchild", AgeBand.UNDER_16, consented=True)
    owner = _user("f18_revowner", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, ward)
    _member(_activity(owner, "f18-rev-type"), ward)

    assert MEETING in _client(guardian).get("/wards/").content.decode()  # active: visible
    revoke_guardian(guardian, ward)
    body = _client(guardian).get("/wards/").content.decode()
    assert HOME not in body
    assert MEETING not in body
    assert ward.display_name not in body  # ward off the manifest entirely


def test_stale_cross_cohort_membership_not_mirrored():
    # An ADULT joins an adult meetup (with logistics), then is re-verified to CHILD, leaving a
    # stale MEMBER row on the immutable-cohort ADULT activity. The manifest must NOT mirror it.
    guardian = _user("f18_scg")
    ward = _user("f18_scward")  # starts ADULT
    owner = _user("f18_scowner")  # ADULT activity owner
    activity = _activity(owner, "f18-stale-type")
    _member(activity, ward)
    # Re-verify the ward down to CHILD; the stale ADULT membership row survives the cohort change.
    apply_assurance(ward, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ward.refresh_from_db()
    assert ward.cohort == Cohort.CHILD
    ParentalConsent.objects.create(
        minor=ward, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    link_guardian(guardian, ward)

    body = _client(guardian).get("/wards/").content.decode()
    assert HOME not in body  # cross-cohort activity is walled off
    assert MEETING not in body


def test_hidden_activity_not_mirrored():
    guardian = _user("f18_hg")
    ward = _user("f18_hward", AgeBand.UNDER_16, consented=True)
    owner = _user("f18_howner", AgeBand.UNDER_16, consented=True)
    link_guardian(guardian, ward)
    activity = _activity(owner, "f18-hidden-type")
    _member(activity, ward)
    activity.is_hidden = True  # a moderator REMOVE hides it from every member-facing surface
    activity.save(update_fields=["is_hidden"])

    body = _client(guardian).get("/wards/").content.decode()
    assert HOME not in body
    assert MEETING not in body


def test_non_guardian_sees_no_manifest():
    stranger = _user("f18_stranger")
    body = _client(stranger).get("/wards/").content.decode()
    assert HOME not in body  # has no wards at all


# --- member-gated logistics card -----------------------------------------------------


def test_member_logistics_card_omits_retired_fields():
    owner = _user("f18_aowner")
    member = _user("f18_amember")
    outsider = _user("f18_aoutsider")
    activity = _activity(
        owner,
        "f18-adult-type",
        fallback_meeting_point="Covered pavilion if the court is wet",
    )
    _member(activity, member)

    member_body = _client(member).get(f"/activities/{activity.id}/").content.decode()
    assert MEETING in member_body
    assert HOME not in member_body
    assert "Covered pavilion" not in member_body
    # A non-member still does not get the member-gated logistics card.
    assert MEETING not in _client(outsider).get(f"/activities/{activity.id}/").content.decode()


def test_activity_edit_form_does_not_render_retired_getting_home_field():
    owner = _user("f18_eowner")
    activity = _activity(owner, "f18-edit-type")

    resp = _client(owner).get(f"/activities/{activity.id}/edit/")

    assert "getting_home_note" not in resp.context["form"].fields
    assert HOME not in resp.content.decode()
