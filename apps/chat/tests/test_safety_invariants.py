"""Negative child-safety tests for per-activity chat (Wave 0 fixes): consent revocation
cuts access at read/write time (not just at join), and blocking is honoured inside a
shared activity thread. See docs/AUDIT_2026-05.md (SAFE-4, chat-block)."""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.chat import services
from apps.places.models import Place
from apps.safety.services import block_user
from apps.social.models import Membership
from apps.social.services import create_activity
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


def test_revoked_consent_blocks_chat_access():
    owner = _child("cc_owner")
    member = _child("cc_member")
    thread = _activity_with_member(owner, member, "rev").thread
    assert services.can_access_thread(member, thread) is True
    ParentalConsent.objects.filter(minor=member).update(status=ParentalConsent.Status.REVOKED)
    assert services.can_access_thread(member, thread) is False
    with pytest.raises(services.ChatError):
        services.send_message(member, thread, "still here?")


def test_blocked_member_cannot_access_owners_thread():
    owner = _adult("cb_owner")
    member = _adult("cb_member")
    thread = _activity_with_member(owner, member, "blk").thread
    assert services.can_access_thread(member, thread) is True
    block_user(member, owner)
    assert services.can_access_thread(member, thread) is False
