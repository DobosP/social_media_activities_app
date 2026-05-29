"""Negative child-safety tests for the social core (Wave 0 fixes): an accompanying
guardian (an adult) cannot post into a children's activity thread, and a member whose
parental consent is revoked loses posting access. See docs/AUDIT_2026-05.md
(guardian-read-only, SAFE-4)."""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.places.models import Place
from apps.social.services import NotEligible, add_guardian, create_activity, post_to_thread
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


def _child_activity(owner, slug):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"ss-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"ss-at-{slug}", name="Football", category=cat)
    return create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Kids game",
        starts_at="2026-06-01T10:00Z",
        guardian_accompanied=True,
    )


def test_guardian_cannot_post_to_child_thread():
    child = _child("gp_child")
    activity = _child_activity(child, "gp")
    guardian = _adult("gp_guardian")
    link_guardian(guardian, child)
    add_guardian(child, activity, guardian)
    with pytest.raises(NotEligible):
        post_to_thread(guardian, activity, "hi kids")


def test_revoked_consent_blocks_thread_post():
    child = _child("rp_child")
    activity = _child_activity(child, "rp")
    post_to_thread(child, activity, "first post")  # works while consented
    ParentalConsent.objects.filter(minor=child).update(status=ParentalConsent.Status.REVOKED)
    with pytest.raises(NotEligible):
        post_to_thread(child, activity, "after revoke")
