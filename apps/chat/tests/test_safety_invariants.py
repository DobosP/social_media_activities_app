"""Negative child-safety tests for the unified activity thread: consent revocation cuts
access at read/write time (not just at join), and blocking is honoured inside a shared
activity thread. Ported from the old chat surface onto the single write path
``social.post_to_thread`` + read gate ``social.can_read_thread`` (the "One Thread"
unification). See docs/AUDIT_2026-05.md (SAFE-4, chat-block)."""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import Membership
from apps.social.services import can_read_thread, create_activity, post_to_thread
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity_with_member(owner, member, slug):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"cs-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"cs-at-{slug}", name="Football", category=cat)
    activity = create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2026-06-01T10:00Z"
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return activity


def test_revoked_consent_blocks_thread_access():
    owner = _child("cc_owner")
    member = _child("cc_member")
    activity = _activity_with_member(owner, member, "rev")
    assert can_read_thread(member, activity) is True
    ParentalConsent.objects.filter(minor=member).update(status=ParentalConsent.Status.REVOKED)
    assert can_read_thread(member, activity) is False
    with pytest.raises(social.NotEligible):
        post_to_thread(member, activity, "still here?")


def test_blocked_member_cannot_access_owners_thread():
    owner = _adult("cb_owner")
    member = _adult("cb_member")
    activity = _activity_with_member(owner, member, "blk")
    assert can_read_thread(member, activity) is True
    block_user(member, owner)
    assert can_read_thread(member, activity) is False
    # ...and the write path refuses too (the gate holds on read AND write).
    with pytest.raises(social.InvalidState):
        post_to_thread(member, activity, "let me in")
