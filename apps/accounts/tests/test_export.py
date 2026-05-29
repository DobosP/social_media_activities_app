"""GDPR Art. 20 (data portability) export: the authenticated user's own data as JSON,
plus the guardian-for-ward variant. Verifies the export is scoped to the requester (or
their ward), discloses no other user's PII, and exposes the expected sections."""

import pytest
from django.contrib.gis.geos import Point
from django.urls import reverse
from rest_framework.test import APIClient

from apps.accounts.export import build_user_export
from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance, grant_parental_consent, link_guardian
from apps.donations.models import Donation
from apps.places.models import Place
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _activity(owner, slug):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"cat-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"at-{slug}", name="Football", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2030-06-01T10:00Z"
    )


def test_build_user_export_has_expected_sections():
    user = _user("exp1")
    export = build_user_export(user)
    assert set(export) == {
        "schema_version",
        "generated_at",
        "profile",
        "age_assurance",
        "consents",
        "guardianships",
        "memberships",
        "owned_activities",
        "donations",
    }
    assert export["profile"]["username"] == "exp1"
    assert export["profile"]["cohort"] == "adult"
    # The proven band is exported, but never a birthdate or other identifying data.
    assert export["profile"]["age_band"] == AgeBand.ADULT
    assert "birth_date" not in str(export)
    assert export["age_assurance"][0]["provider"] == "dev"


def test_build_user_export_includes_activity_membership_and_donations():
    user = _user("exp2")
    _activity(user, "exp2")
    Donation.objects.create(
        donor=user,
        amount_cents=500,
        provider="stripe",
        status=Donation.Status.COMPLETED,
        external_ref="ext-1",
    )
    export = build_user_export(user)

    assert len(export["owned_activities"]) == 1
    assert export["owned_activities"][0]["title"] == "Game"
    # create_activity makes the owner a member.
    assert any(m["role"] == "owner" for m in export["memberships"])
    assert export["donations"]["completed_count"] == 1
    assert export["donations"]["completed_total_cents"] == 500
    # No payment-card data is ever stored, so it cannot leak here.
    assert "card" not in str(export["donations"]).lower()


def test_build_user_export_includes_consent_and_guardianship():
    guardian = _user("g_exp", AgeBand.ADULT)
    ward = _user("w_exp", AgeBand.UNDER_16)
    link_guardian(guardian, ward)
    grant_parental_consent(guardian, ward)

    ward_export = build_user_export(ward)
    assert ward_export["consents"]["as_minor"][0]["status"] == "active"
    assert ward_export["guardianships"]["guarded_by"][0]["guardian_public_id"] == str(
        guardian.public_id
    )

    guardian_export = build_user_export(guardian)
    assert guardian_export["guardianships"]["as_guardian_of"][0]["ward_public_id"] == str(
        ward.public_id
    )


# --- API ---


def test_me_export_requires_auth():
    assert APIClient().get(reverse("me-export")).status_code in (401, 403)


def test_me_export_returns_own_data():
    user = _user("api_exp")
    client = APIClient()
    client.force_authenticate(user)
    resp = client.get(reverse("me-export"))
    assert resp.status_code == 200
    assert resp.json()["profile"]["username"] == "api_exp"


def test_ward_export_allowed_for_guardian():
    guardian = _user("g_api", AgeBand.ADULT)
    ward = _user("w_api", AgeBand.UNDER_16)
    link_guardian(guardian, ward)
    grant_parental_consent(guardian, ward)

    client = APIClient()
    client.force_authenticate(guardian)
    resp = client.get(reverse("ward-export", args=[ward.public_id]))
    assert resp.status_code == 200
    assert resp.json()["profile"]["username"] == "w_api"


def test_ward_export_denied_for_non_guardian():
    stranger = _user("s_api", AgeBand.ADULT)
    ward = _user("w_api2", AgeBand.UNDER_16)

    client = APIClient()
    client.force_authenticate(stranger)
    resp = client.get(reverse("ward-export", args=[ward.public_id]))
    assert resp.status_code == 403


def test_ward_export_unknown_user_is_404():
    import uuid

    guardian = _user("g_api3", AgeBand.ADULT)
    client = APIClient()
    client.force_authenticate(guardian)
    resp = client.get(reverse("ward-export", args=[uuid.uuid4()]))
    assert resp.status_code == 404
