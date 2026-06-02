"""REST API for the user-place co-creation quorum (F25) — previously web + admin only.
Pins: propose creates a pending proposal; participation gate; duplicate handling (hard + soft);
the pending list + confirm flow; and the COUNT-ONLY rule (no proposer/confirmer identity)."""

import pytest
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand
from apps.social.models import UserPlaceProposal

from .conftest import make_user

pytestmark = pytest.mark.django_db

PROPOSALS = "/api/social/place-proposals/"


def _client(user):
    c = APIClient()
    c.force_authenticate(user)
    return c


def _propose(
    client, activity_type, *, name="Riverside Court", lon=23.71, lat=46.81, allow_nearby=False
):
    return client.post(
        PROPOSALS,
        {
            "name": name,
            "lon": lon,
            "lat": lat,
            "activity_type": activity_type.id,
            "allow_nearby": allow_nearby,
        },
        format="json",
    )


def test_propose_creates_pending_proposal(adult, activity_type):
    resp = _propose(_client(adult), activity_type)
    assert resp.status_code == 201, resp.content
    body = resp.json()
    assert body["status"] == UserPlaceProposal.Status.PENDING
    assert body["confirmations_count"] == 0
    assert body["place_name"] == "Riverside Court"
    # COUNT-ONLY: never the proposer's identity.
    assert "proposer" not in body and "proposer_id" not in body


def test_propose_requires_participation(activity_type):
    # A non-consented under-16 cannot participate -> 403 (no place is orphaned).
    minor = make_user("ppapi_minor", AgeBand.UNDER_16, consented=False)
    resp = _propose(_client(minor), activity_type)
    assert resp.status_code == 403
    assert UserPlaceProposal.objects.count() == 0


def test_duplicate_place_is_rejected_with_reference(adult, adult2, activity_type):
    first = _propose(_client(adult), activity_type, name="Twin Hall", lon=23.72, lat=46.82)
    assert first.status_code == 201
    # Same name + same coordinate -> HARD duplicate, surfaced with the existing place reference.
    dup = _propose(_client(adult2), activity_type, name="Twin Hall", lon=23.72, lat=46.82)
    assert dup.status_code == 400
    body = dup.json()
    assert body["duplicate_place_id"] == first.json()["place_id"]
    assert body["soft"] is False


def test_soft_duplicate_can_be_overridden_with_allow_nearby(adult, adult2, activity_type):
    first = _propose(_client(adult), activity_type, name="Alpha Field", lon=23.73, lat=46.83)
    assert first.status_code == 201
    # A clearly DIFFERENT name at the same spot -> not a name dup, but a soft location dup
    # (something is very close), rejected unless allow_nearby...
    soft = _propose(_client(adult2), activity_type, name="Zephyr Plaza", lon=23.73, lat=46.83)
    assert soft.status_code == 400 and soft.json()["soft"] is True
    # Web parity: a SOFT duplicate exposes only the name, never the bare id of the (possibly
    # hidden) nearby place — the id is only disclosed for a HARD (same-name) duplicate.
    assert "duplicate_place_id" not in soft.json()
    # ...and the proposer can override it.
    ok = _propose(
        _client(adult2), activity_type, name="Zephyr Plaza", lon=23.73, lat=46.83, allow_nearby=True
    )
    assert ok.status_code == 201


def test_pending_list_is_count_only_and_excludes_own(adult, adult2, activity_type):
    _propose(_client(adult), activity_type, name="Lakeside Field", lon=23.74, lat=46.84)
    # The proposer does NOT see their own proposal in the confirmable list.
    assert _client(adult).get(PROPOSALS).json() == []
    # Another verified user sees it — count-only, no proposer identity.
    rows = _client(adult2).get(PROPOSALS).json()
    assert len(rows) == 1
    assert rows[0]["confirmations_count"] == 0
    assert "proposer" not in rows[0] and "proposer_id" not in rows[0]


def test_confirm_increments_and_proposer_cannot_confirm_own(adult, adult2, activity_type):
    created = _propose(_client(adult), activity_type, name="Hilltop Court", lon=23.75, lat=46.85)
    pid = created.json()["id"]
    # The proposer cannot confirm their own proposal.
    assert _client(adult).post(f"{PROPOSALS}{pid}/confirm/").status_code == 400
    # A different verified user can — and the count goes up (quorum is 3, so still pending).
    resp = _client(adult2).post(f"{PROPOSALS}{pid}/confirm/")
    assert resp.status_code == 200
    assert resp.json()["confirmations_count"] == 1
    assert resp.json()["status"] == UserPlaceProposal.Status.PENDING


def test_confirm_with_non_integer_pk_404s_not_500(adult):
    # A non-numeric pk must 404 at routing (lookup_value_regex), never reach .filter(pk="abc")
    # and 500. APIClient by default re-raises server exceptions, so a 500 would surface as one.
    resp = _client(adult).post(f"{PROPOSALS}not-a-number/confirm/")
    assert resp.status_code == 404


def test_confirm_quorum_publishes(adult, adult2, activity_type):
    created = _propose(_client(adult), activity_type, name="Quorum Park", lon=23.76, lat=46.86)
    pid = created.json()["id"]
    last = None
    for i in range(3):  # DEFAULT_PLACE_QUORUM = 3 independent confirmers
        confirmer = make_user(f"ppapi_conf_{i}", AgeBand.ADULT)
        last = _client(confirmer).post(f"{PROPOSALS}{pid}/confirm/")
    assert last.json()["status"] == UserPlaceProposal.Status.PUBLISHED
