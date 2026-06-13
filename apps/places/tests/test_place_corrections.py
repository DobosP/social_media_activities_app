"""F20 — crowd-corrected venue name & address (quorum edit overlay).

Clones the F25 user-place quorum. Applied OSM-first at read time via Place.display_name /
display_address — NEVER written back to Place. Counts-only; proposer excluded from confirming.
"""

import pytest
from django.contrib.gis.geos import Point
from django.db import transaction
from django.db.utils import IntegrityError

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Role, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place, PlaceCorrection
from apps.places.services import (
    InvalidState,
    NotEligible,
    PlacesError,
    confirm_place_correction,
    pending_corrections,
    propose_place_correction,
    staff_publish_correction,
    staff_reject_correction,
)

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)
F = PlaceCorrection.Field


def _user(name, role=Role.USER, is_staff=False):
    u = User.objects.create_user(
        username=name, password="pw", display_name=name, role=role, is_staff=is_staff
    )
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _staff(name):
    return _user(name, role=Role.ADMIN, is_staff=True)


def _place(**kw):
    kw.setdefault("name", "Old Name")
    kw.setdefault("source", Place.Source.OSM)
    return Place.objects.create(location=PT, **kw)


def _publish_name(place, value="New Name"):
    proposer = _user(f"p-{place.id}")
    c = propose_place_correction(proposer, place, field=F.NAME, proposed_value=value)
    for i in range(3):
        confirm_place_correction(_user(f"c-{place.id}-{i}"), c)
    c.refresh_from_db()
    return c


# --- display properties ---------------------------------------------------------------


def test_display_name_falls_back_to_osm():
    place = _place(name="Library X")
    assert place.display_name == "Library X"


def test_published_name_correction_applies_at_read_time():
    place = _place(name="Old Name")
    c = _publish_name(place, "Corrected Name")
    assert c.status == PlaceCorrection.Status.PUBLISHED
    place.refresh_from_db()
    assert place.display_name == "Corrected Name"
    # The raw OSM name is NEVER overwritten (re-ingest safe).
    assert place.name == "Old Name"


def test_pending_correction_does_not_apply():
    place = _place(name="Old Name")
    proposer = _user("pa")
    propose_place_correction(proposer, place, field=F.NAME, proposed_value="Premature")
    place.refresh_from_db()
    assert place.display_name == "Old Name"  # not published -> not applied


def test_display_address_composes_and_overrides():
    place = _place(address_street="Str. Veche", address_housenumber="5", address_city="Cluj")
    assert place.display_address == "Str. Veche 5, Cluj"
    proposer = _user("pad")
    c = propose_place_correction(
        proposer, place, field=F.ADDRESS, proposed_value="Str. Noua 9, Cluj"
    )
    for i in range(3):
        confirm_place_correction(_user(f"cad{i}"), c)
    place.refresh_from_db()
    assert place.display_address == "Str. Noua 9, Cluj"


# --- quorum mechanics -----------------------------------------------------------------


def test_proposer_cannot_confirm_own_correction():
    place = _place()
    proposer = _user("self")
    c = propose_place_correction(proposer, place, field=F.NAME, proposed_value="x")
    with pytest.raises(InvalidState):
        confirm_place_correction(proposer, c)


def test_confirm_is_idempotent_per_user():
    place = _place()
    c = propose_place_correction(_user("pi"), place, field=F.NAME, proposed_value="x")
    u = _user("ci")
    confirm_place_correction(u, c)
    confirm_place_correction(u, c)  # repeat -> still one confirmation
    assert c.confirmations.count() == 1


def test_staff_can_fast_publish_and_reject():
    place = _place(name="Old")
    c = propose_place_correction(_user("sp"), place, field=F.NAME, proposed_value="Staff New")
    staff_publish_correction(_staff("mod"), c)
    place.refresh_from_db()
    assert place.display_name == "Staff New"

    place2 = _place(name="Keep")
    c2 = propose_place_correction(_user("sr"), place2, field=F.NAME, proposed_value="Bad")
    staff_reject_correction(_staff("mod2"), c2, reason="spam")
    place2.refresh_from_db()
    assert place2.display_name == "Keep"  # rejected -> never applied


def test_staff_reject_reverts_published_correction():
    # Rejecting a PUBLISHED correction is a deliberate REVERT (differs from F25's pending-only) —
    # display falls back to OSM. A re-reject of an already-rejected one is a validated no-op error.
    place = _place(name="Old Name")
    c = _publish_name(place, "Bad Published")
    place.refresh_from_db()
    assert place.display_name == "Bad Published"
    staff_reject_correction(_staff("rev"), c)
    place.refresh_from_db()
    assert place.display_name == "Old Name"  # reverted to OSM
    c.refresh_from_db()
    assert c.status == PlaceCorrection.Status.REJECTED and c.published_at is None
    with pytest.raises(InvalidState):
        staff_reject_correction(_staff("rev2"), c)  # already rejected


def test_latest_published_correction_wins():
    place = _place(name="Old Name")
    _publish_name(place, "First")
    place.refresh_from_db()
    assert place.display_name == "First"
    # A second correction can open once the first is published (not pending), then publish too.
    second = propose_place_correction(_user("p2"), place, field=F.NAME, proposed_value="Second")
    for i in range(3):
        confirm_place_correction(_user(f"c2-{i}"), second)
    place.refresh_from_db()
    assert place.display_name == "Second"  # most-recent PUBLISHED wins


def test_place_serializer_renders_display_name_and_address():
    from apps.places.serializers import PlaceSerializer

    place = _place(name="Old Name", address_street="Str. Veche", address_city="Cluj")
    _publish_name(place, "Corrected Name")
    place.refresh_from_db()
    props = PlaceSerializer(place).data["properties"]
    assert props["name"] == "Corrected Name"
    assert props["display_address"] == "Str. Veche, Cluj"


# --- gating ---------------------------------------------------------------------------


def test_requires_participation():
    place = _place()
    unverified = User.objects.create_user(username="u0", password="pw")
    with pytest.raises(NotEligible):
        propose_place_correction(unverified, place, field=F.NAME, proposed_value="x")


def test_rejects_unknown_field_and_empty_value():
    place = _place()
    with pytest.raises(PlacesError):
        propose_place_correction(_user("uf"), place, field="phone", proposed_value="x")
    with pytest.raises(PlacesError):
        propose_place_correction(_user("ev"), place, field=F.NAME, proposed_value="   ")


def test_rejects_nonpublic_place():
    place = _place(source=Place.Source.USER)  # no published proposal -> not public_places()
    with pytest.raises(PlacesError):
        propose_place_correction(_user("np"), place, field=F.NAME, proposed_value="x")


def test_only_one_pending_correction_per_field():
    place = _place()
    propose_place_correction(_user("o1"), place, field=F.NAME, proposed_value="A")
    with pytest.raises(PlacesError):
        propose_place_correction(_user("o2"), place, field=F.NAME, proposed_value="B")


def test_value_is_capped_at_255():
    place = _place()
    c = propose_place_correction(_user("cap"), place, field=F.NAME, proposed_value="z" * 400)
    assert len(c.proposed_value) == 255


# --- pending summary is counts-only ---------------------------------------------------


def test_pending_corrections_counts_only():
    place = _place()
    proposer = _user("pp")
    c = propose_place_correction(proposer, place, field=F.NAME, proposed_value="X")
    confirm_place_correction(_user("pc1"), c)
    viewer = _user("viewer")
    rows = pending_corrections(place, viewer)
    assert len(rows) == 1
    row = rows[0]
    assert row["confirms"] == 1 and row["required"] == 3
    # No proposer/confirmer identity is ever exposed.
    assert "proposer" not in row and "confirmer" not in row and "users" not in row


def test_db_blocks_duplicate_pending_at_constraint_level():
    place = _place()
    proposer = _user("dup")
    PlaceCorrection.objects.create(place=place, proposer=proposer, field=F.NAME, proposed_value="A")
    with pytest.raises(IntegrityError), transaction.atomic():
        PlaceCorrection.objects.create(
            place=place, proposer=proposer, field=F.NAME, proposed_value="B"
        )
