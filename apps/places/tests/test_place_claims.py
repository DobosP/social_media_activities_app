"""ADR-0019 §6 — venue claims: eligibility gates, staff decisions, official partner."""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.places.models import Partner, Place, PlaceClaim
from apps.places.services import (
    ClaimError,
    approve_place_claim,
    file_place_claim,
    official_partner_for_place,
    reject_place_claim,
)

pytestmark = pytest.mark.django_db


@pytest.fixture
def place():
    return Place.objects.create(
        name="Sala Polivalentă",
        location=Point(23.6, 46.76, srid=4326),
        source=Place.Source.OSM,
    )


@pytest.fixture
def adult(django_user_model):
    user = django_user_model.objects.create_user(username="owner-ana", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return user


@pytest.fixture
def staff(django_user_model):
    return django_user_model.objects.create_user(username="mod", password="pw", is_staff=True)


def _claim(user, place, **over):
    data = {"org_name": "SC Sala Polivalentă SRL", "official_website": "https://sala.example/"}
    data.update(over)
    return file_place_claim(user, place, **data)


def test_adult_can_file_claim_and_audit_runs(adult, place):
    claim = _claim(adult, place, cui="RO123456")
    assert claim.status == PlaceClaim.Status.PENDING
    assert claim.kind == "business"
    assert claim.cui == "RO123456"


def test_teen_cannot_file_claim(django_user_model, place):
    teen = django_user_model.objects.create_user(username="teen", password="pw")
    apply_assurance(teen, AssuranceResult(age_band=AgeBand.AGE_16_17, provider="dev"))
    with pytest.raises(ClaimError):
        _claim(teen, place)


def test_second_pending_claim_by_same_user_is_refused(adult, place):
    _claim(adult, place)
    with pytest.raises(ClaimError):
        _claim(adult, place)


def test_approve_creates_verified_business_partner_and_backfills_website(adult, staff, place):
    claim = _claim(adult, place)

    approve_place_claim(staff, claim)

    claim.refresh_from_db()
    place.refresh_from_db()
    assert claim.status == PlaceClaim.Status.APPROVED
    partner = claim.partner
    assert partner is not None and partner.is_verified and partner.is_active
    assert partner.kind == Partner.Kind.BUSINESS
    assert partner.place_id == place.pk
    assert place.website == "https://sala.example/"
    assert official_partner_for_place(place) == partner
    note = Notification.objects.filter(recipient=adult, kind="system").latest("created_at")
    assert "approved" in note.title


def test_approve_requires_staff_and_pending_state(adult, staff, place):
    claim = _claim(adult, place)
    with pytest.raises(ClaimError):
        approve_place_claim(adult, claim)
    approve_place_claim(staff, claim)
    with pytest.raises(ClaimError):
        approve_place_claim(staff, claim)


def test_reject_notifies_with_reason(adult, staff, place):
    claim = _claim(adult, place)

    reject_place_claim(staff, claim, reason="Domain email didn't match")

    claim.refresh_from_db()
    assert claim.status == PlaceClaim.Status.REJECTED
    note = Notification.objects.filter(recipient=adult, kind="system").latest("created_at")
    assert "not approved" in note.title
    assert "Domain email" in note.body


def test_official_partner_ignores_civic_kinds(adult, staff, place):
    Partner.objects.create(
        name="Biblioteca Județeană",
        kind=Partner.Kind.LIBRARY,
        place=place,
        is_verified=True,
        is_active=True,
    )
    assert official_partner_for_place(place) is None


def test_claim_web_flow(client, adult, place):
    client.force_login(adult)
    resp = client.get(f"/places/{place.pk}/claim/")
    assert resp.status_code == 200
    resp = client.post(
        f"/places/{place.pk}/claim/",
        {
            "org_name": "SC Sala SRL",
            "kind": "business",
            "official_website": "https://sala.example/",
            "contact_email": "",
            "cui": "",
            "evidence": "",
        },
    )
    assert resp.status_code == 302
    assert PlaceClaim.objects.filter(place=place, claimant=adult).exists()
