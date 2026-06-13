"""F20 (web) — propose/confirm a venue name/address correction; display_* on place_detail."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place, PlaceCorrection

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
PW = "sup3r-secret-pw"
F = PlaceCorrection.Field


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _place(name="Old Name"):
    return Place.objects.create(name=name, location=PT, source=Place.Source.OSM)


def test_member_can_propose_correction():
    place = _place()
    resp = _client(_user("w1")).post(
        f"/places/{place.pk}/corrections/propose/",
        {"field": F.NAME, "proposed_value": "New Name"},
    )
    assert resp.status_code == 302
    assert PlaceCorrection.objects.filter(
        place=place, field="name", proposed_value="New Name"
    ).exists()


def test_detail_shows_pending_count_not_identity():
    place = _place()
    proposer = _user("w2p")
    PlaceCorrection.objects.create(
        place=place, proposer=proposer, field=F.NAME, proposed_value="Fixed"
    )
    body = _client(_user("w2v")).get(f"/places/{place.pk}/").content.decode()
    assert "Fixed" in body  # the proposed value shows
    assert "0/3 confirms" in body
    assert "w2p" not in body  # the proposer's identity is never shown


def test_confirm_publishes_and_applies_at_quorum():
    place = _place(name="Old Name")
    proposer = _user("w3p")
    correction = PlaceCorrection.objects.create(
        place=place, proposer=proposer, field=F.NAME, proposed_value="Corrected"
    )
    for i in range(3):
        resp = _client(_user(f"w3c{i}")).post(
            f"/places/{place.pk}/corrections/{correction.pk}/confirm/"
        )
        assert resp.status_code == 302
    place.refresh_from_db()
    assert place.display_name == "Corrected"
    # The detail title now renders the corrected name.
    body = _client(_user("w3r")).get(f"/places/{place.pk}/").content.decode()
    assert "Corrected" in body


def test_place_detail_prefetches_corrections_no_n_plus_one(django_assert_max_num_queries):
    # A mix of pending corrections (one per field, per the constraint) + a published one.
    place = _place()
    PlaceCorrection.objects.create(
        place=place, proposer=_user("pn"), field=F.NAME, proposed_value="Name?"
    )
    PlaceCorrection.objects.create(
        place=place, proposer=_user("pa"), field=F.ADDRESS, proposed_value="Addr?"
    )
    PlaceCorrection.objects.create(
        place=place,
        proposer=_user("pp"),
        field=F.NAME,
        proposed_value="Was published",
        status=PlaceCorrection.Status.PUBLISHED,
    )
    client = _client(_user("viewer"))
    # place_detail prefetches 'corrections', so rendering is query-bounded regardless of count.
    with django_assert_max_num_queries(30):
        assert client.get(f"/places/{place.pk}/").status_code == 200


def test_proposer_confirm_button_hidden_for_self():
    place = _place()
    proposer = _user("w4")
    PlaceCorrection.objects.create(place=place, proposer=proposer, field=F.NAME, proposed_value="X")
    body = _client(proposer).get(f"/places/{place.pk}/").content.decode()
    # The proposer sees their pending correction but NO confirm button for it.
    assert "X" in body
    assert f"/places/{place.pk}/corrections/" not in body or "/confirm/" not in body
