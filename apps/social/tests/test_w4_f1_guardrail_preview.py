"""W4-F1 — guardrail dry-run preview: an honest "could join N of the next M upcoming meetups"
read for a guardian, derived from the SAME gate fns enforcement uses (no drift), with already-joined
and capacity-full meetups excluded from the denominator so the count never conflates "your limits
block this" with "already joined / full".
"""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import override_settings
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian, set_guardian_guardrail
from apps.places.models import Place
from apps.social.models import Membership
from apps.social.services import GUARDRAIL_PREVIEW_LIMIT, create_activity, guardrail_preview
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _child_no_consent(name):
    """A CHILD with current age-assurance but NO active parental consent -> can_participate is
    False, so can_join returns False for every meetup (the HIGH-regression case)."""
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _place(slug):
    return Place.objects.create(
        name=f"Hall-{slug}", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _type(slug):
    cat = ActivityCategory.objects.create(slug=f"f1cat-{slug}", name="Sport")
    return ActivityType.objects.create(slug=f"f1at-{slug}", name="Football", category=cat)


def _future_activity(owner, *, slug, guardian_accompanied=False, capacity=None, place=None):
    """A future OPEN CHILD activity owned by `owner`, distinct enough to be its own row."""
    return create_activity(
        owner,
        place=place or _place(slug),
        activity_type=_type(slug),
        title=f"Kids football {slug}",
        starts_at=timezone.now() + timedelta(days=3),
        guardian_accompanied=guardian_accompanied,
        capacity=capacity,
    )


def test_non_child_ward_returns_none():
    adult = _adult("a1")
    assert guardrail_preview(adult) is None


def test_no_guardrail_counts_every_future_meetup():
    owner = _child("o1")
    for i in range(3):
        _future_activity(owner, slug=f"ng{i}")
    ward = _child("w1")
    # No guardrail set -> guardrails block nothing -> eligible == total (the venue gate is off in
    # the test settings, so every cohort-visible future meetup is eligible).
    preview = guardrail_preview(ward)
    assert preview == {"eligible": 3, "total": 3}


def test_supervised_only_narrows_eligible_not_total():
    owner = _child("o2")
    _future_activity(owner, slug="sup-yes", guardian_accompanied=True)
    _future_activity(owner, slug="sup-no1", guardian_accompanied=False)
    _future_activity(owner, slug="sup-no2", guardian_accompanied=False)
    ward = _child("w2")
    guardian = _adult("p2")
    link_guardian(guardian, ward)
    set_guardian_guardrail(guardian, ward, supervised_only=True)
    # The limit narrows ELIGIBLE (only the accompanied meetup) but the denominator still counts all
    # three — that 1-of-3 is exactly the diagnostic signal that the limit is doing the narrowing.
    assert guardrail_preview(ward) == {"eligible": 1, "total": 3}


def test_already_joined_excluded_from_denominator():
    owner = _child("o3")
    a_joined = _future_activity(owner, slug="joined")
    _future_activity(owner, slug="open1")
    _future_activity(owner, slug="open2")
    ward = _child("w3")
    # The ward is already a MEMBER of one meetup -> it must drop from BOTH numerator and
    # denominator (it's not a guardrail block), so the count diagnoses the limits, not membership.
    Membership.objects.create(activity=a_joined, user=ward, state=Membership.State.MEMBER)
    assert guardrail_preview(ward) == {"eligible": 2, "total": 2}


def test_capacity_full_excluded_from_denominator():
    owner = _child("o4")
    full = _future_activity(owner, slug="full", capacity=1)
    _future_activity(owner, slug="room1")
    # Seat ONE other peer (a voting member) so `full` is at capacity -> dropped from the denominator
    # (a full meetup is not a guardrail block).
    other = _child("peer4")
    Membership.objects.create(activity=full, user=other, state=Membership.State.MEMBER)
    ward = _child("w4")
    assert guardrail_preview(ward) == {"eligible": 1, "total": 1}


def test_empty_when_no_upcoming_meetups():
    ward = _child("w5")
    assert guardrail_preview(ward) == {"eligible": 0, "total": 0}


def test_eligible_can_be_zero_while_total_positive():
    # The honest "0 of N" diagnostic: a too-tight limit blocks everything but the meetups still
    # exist, so the guardian sees WHY (not a silently broken app).
    owner = _child("o6")
    _future_activity(owner, slug="z1", guardian_accompanied=False)
    _future_activity(owner, slug="z2", guardian_accompanied=False)
    ward = _child("w6")
    guardian = _adult("p6")
    link_guardian(guardian, ward)
    set_guardian_guardrail(guardian, ward, supervised_only=True)
    assert guardrail_preview(ward) == {"eligible": 0, "total": 2}


def test_venue_gate_mirrors_can_join_when_flag_on():
    # With the public-venue flag ON and unclassified OSM venues, the venue gate (the same one
    # can_join applies) drops every meetup from ELIGIBLE even with no guardrail set. Create with the
    # flag OFF (its default in tests), then flip it on only for the read, so creation isn't blocked.
    owner = _child("o7")
    _future_activity(owner, slug="v1")
    _future_activity(owner, slug="v2")
    ward = _child("w7")
    with override_settings(CHILD_PUBLIC_VENUES_ONLY=True):
        preview = guardrail_preview(ward)
    assert preview["total"] == 2
    assert preview["eligible"] == 0


def test_lapsed_consent_ward_is_eligible_for_nothing():
    # Regression (review HIGH): the preview must mirror can_join's FIRST gate — a ward who cannot
    # participate (no active parental consent) can join nothing, so eligible is 0 even with future
    # meetups and NO guardrail. Without this the panel falsely claims "could join N".
    from apps.social.services import can_join

    owner = _child("oc")
    activities = [_future_activity(owner, slug=f"lc{i}") for i in range(3)]
    ward = _child_no_consent("wc")
    # Sanity-check the real gate agrees the ward can join nothing right now.
    assert all(can_join(ward, a) is False for a in activities)
    assert guardrail_preview(ward) == {"eligible": 0, "total": 3}


def test_scan_is_bounded_by_limit():
    owner = _child("o8")
    for i in range(GUARDRAIL_PREVIEW_LIMIT + 3):
        _future_activity(owner, slug=f"b{i}")
    ward = _child("w8")
    preview = guardrail_preview(ward, limit=GUARDRAIL_PREVIEW_LIMIT)
    # The denominator never exceeds the documented scan bound (the copy promises "the next N").
    assert preview["total"] == GUARDRAIL_PREVIEW_LIMIT
