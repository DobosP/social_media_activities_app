"""Wave 9 — F25 (user-place visibility chokepoint), F26 (crowd confirm/dispute of
place<->activity edges), F28 (open-now accuracy reports). All three are ingest-safe overlays
that must survive a place re-ingest and must never leak a still-pending user place."""

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.test import APIClient

from apps.places import edges as edge_svc
from apps.places import services as place_svc
from apps.places.models import ActivityEdgeVote, OpenNowReport, Place, PlaceActivity
from apps.places.serializers import PlaceSerializer
from apps.social.models import UserPlaceProposal

from .conftest import make_user

# --- shared fixtures -----------------------------------------------------------------------


@pytest.fixture
def user_place(db):
    """A user-proposed venue (source=USER) with a still-PENDING co-creation proposal."""
    proposer = make_user("proposer1")
    place = Place.objects.create(
        name="Backyard Pitch", location=Point(23.61, 46.77, srid=4326), source=Place.Source.USER
    )
    proposal = UserPlaceProposal.objects.create(
        place=place, proposer=proposer, status=UserPlaceProposal.Status.PENDING
    )
    return place, proposal, proposer


@pytest.fixture
def edge(place, activity_type):
    """An INFERRED OSM-sourced edge on a public place — the auto-promote/auto-hide target."""
    return PlaceActivity.objects.create(
        place=place, activity=activity_type, origin=PlaceActivity.Origin.INFERRED
    )


# --- F25: visibility chokepoint ------------------------------------------------------------


@pytest.mark.django_db
def test_pending_user_place_hidden_then_visible_on_publish(user_place):
    place, proposal, _ = user_place
    assert not place_svc.public_places().filter(pk=place.pk).exists()
    proposal.status = UserPlaceProposal.Status.PUBLISHED
    proposal.save(update_fields=["status"])
    assert place_svc.public_places().filter(pk=place.pk).exists()


@pytest.mark.django_db
def test_user_place_without_any_proposal_is_hidden():
    # The positive keep-filter bug: a USER place with NO proposal row must NOT be public
    # (NULL IN (...) is not TRUE, so a naive exclude() would have leaked it).
    place = Place.objects.create(
        name="Orphan", location=Point(23.6, 46.77, srid=4326), source=Place.Source.USER
    )
    assert not place_svc.public_places().filter(pk=place.pk).exists()


@pytest.mark.django_db
def test_osm_place_is_always_public(place):
    assert place_svc.public_places().filter(pk=place.pk).exists()


@pytest.mark.django_db
def test_api_list_excludes_pending_user_place(user_place):
    place, _, _ = user_place
    body = APIClient().get("/api/places/").json()
    ids = [f["id"] for f in body["features"]]
    assert place.id not in ids


@pytest.mark.django_db
def test_place_detail_web_404_for_stranger_200_for_proposer(client, user_place):
    place, _, proposer = user_place
    stranger = make_user("stranger1")
    client.force_login(stranger)
    assert client.get(f"/places/{place.id}/").status_code == 404
    client.force_login(proposer)
    assert client.get(f"/places/{place.id}/").status_code == 200


# --- F26: crowd confirm / dispute of edges -------------------------------------------------


@pytest.mark.django_db
def test_quorum_of_confirms_promotes_inferred_edge(edge):
    for i in range(3):
        edge_svc.vote_on_edge(make_user(f"c{i}"), edge, "confirm")
    edge.refresh_from_db()
    assert edge.origin == PlaceActivity.Origin.CONFIRMED
    assert edge.is_disputed is False


@pytest.mark.django_db
def test_quorum_of_disputes_hides_inferred_edge(edge):
    for i in range(3):
        edge_svc.vote_on_edge(make_user(f"d{i}"), edge, "dispute")
    edge.refresh_from_db()
    assert edge.is_disputed is True
    # ...and a disputed edge disappears from the public serializer + discovery card.
    data = PlaceSerializer(edge.place).data
    assert data["properties"]["activities"] == []


@pytest.mark.django_db
def test_below_quorum_does_not_change_edge(edge):
    edge_svc.vote_on_edge(make_user("c0"), edge, "confirm")
    edge_svc.vote_on_edge(make_user("c1"), edge, "confirm")
    edge.refresh_from_db()
    assert edge.origin == PlaceActivity.Origin.INFERRED
    assert edge.is_disputed is False


@pytest.mark.django_db
def test_confirmed_edge_is_not_crowd_hideable(edge):
    # Promote, then pile on disputes: a CONFIRMED (ingest-protected) edge is NOT auto-hidden —
    # only a moderator can reverse it (no crowd-griefing of a confirmed fact).
    for i in range(3):
        edge_svc.vote_on_edge(make_user(f"c{i}"), edge, "confirm")
    for i in range(3):
        edge_svc.vote_on_edge(make_user(f"x{i}"), edge, "dispute")
    edge.refresh_from_db()
    assert edge.origin == PlaceActivity.Origin.CONFIRMED
    assert edge.is_disputed is False


@pytest.mark.django_db
def test_one_vote_per_user_mind_change(edge):
    voter = make_user("mind")
    edge_svc.vote_on_edge(voter, edge, "confirm")
    edge_svc.vote_on_edge(voter, edge, "dispute")
    assert ActivityEdgeVote.objects.filter(edge=edge, user=voter).count() == 1
    summary = edge_svc.edge_vote_summary(edge, voter)
    assert summary["confirms"] == 0
    assert summary["disputes"] == 1
    assert summary["my_vote"] == "dispute"


@pytest.mark.django_db
def test_vote_requires_verified_participation(edge):
    unverified = make_user("nope")
    unverified.is_identity_verified = False  # strip the dev assurance
    unverified.save(update_fields=["is_identity_verified"])
    with pytest.raises(edge_svc.NotEligible):
        edge_svc.vote_on_edge(unverified, edge, "confirm")


@pytest.mark.django_db
def test_vote_refused_on_unpublished_place(user_place, activity_type):
    place, _, proposer = user_place
    pending_edge = PlaceActivity.objects.create(
        place=place, activity=activity_type, origin=PlaceActivity.Origin.INFERRED
    )
    with pytest.raises(edge_svc.InvalidEdge):
        edge_svc.vote_on_edge(make_user("voter9"), pending_edge, "confirm")


@pytest.mark.django_db
def test_moderator_demote_wipes_votes_and_resets_origin(edge):
    for i in range(3):
        edge_svc.vote_on_edge(make_user(f"c{i}"), edge, "confirm")
    edge.refresh_from_db()
    assert edge.origin == PlaceActivity.Origin.CONFIRMED
    mod = make_user("mod1")
    mod.is_staff = True
    mod.save(update_fields=["is_staff"])
    edge_svc.moderator_reverse_edge(mod, edge, action="demote")
    edge.refresh_from_db()
    assert edge.origin == PlaceActivity.Origin.INFERRED
    assert edge.edge_votes.count() == 0


# --- F28: open-now accuracy reports --------------------------------------------------------


@pytest.fixture
def open_place(db):
    from apps.places.enrichment.opening_hours import parse_opening_hours

    return Place.objects.create(
        name="Always Open",
        location=Point(23.6, 46.77, srid=4326),
        source=Place.Source.OSM,
        opening_hours_raw="24/7",
        opening_hours=parse_opening_hours("24/7"),
    )


@pytest.mark.django_db
def test_open_now_true_with_no_reports(open_place):
    assert place_svc.open_now_status(open_place) is True


@pytest.mark.django_db
def test_quorum_of_reports_downgrades_to_unverified(open_place):
    for i in range(3):
        place_svc.file_open_now_report(make_user(f"r{i}"), open_place)
    assert place_svc.open_now_status(open_place) == "unverified"


@pytest.mark.django_db
def test_report_idempotent_per_reporter_per_window(open_place):
    reporter = make_user("dup")
    first = place_svc.file_open_now_report(reporter, open_place)
    second = place_svc.file_open_now_report(reporter, open_place)
    assert first is not None
    assert second is None
    assert open_place.open_now_reports.count() == 1


@pytest.mark.django_db
def test_old_reports_decay_and_stop_counting(open_place, settings):
    settings.OPEN_NOW_REPORT_DECAY_SECONDS = 3600
    for i in range(3):
        place_svc.file_open_now_report(make_user(f"r{i}"), open_place)
    # Age every report past the decay window — they must stop counting (hours self-heal).
    old = timezone.now() - timezone.timedelta(hours=2)
    OpenNowReport.objects.filter(place=open_place).update(created_at=old)
    assert place_svc.open_now_status(open_place) is True


@pytest.mark.django_db
def test_report_requires_verified_participation(open_place):
    unverified = make_user("nope2")
    unverified.is_identity_verified = False
    unverified.save(update_fields=["is_identity_verified"])
    with pytest.raises(place_svc.NotEligible):
        place_svc.file_open_now_report(unverified, open_place)


@pytest.mark.django_db
def test_moderator_clear_reports_self_heals(open_place):
    for i in range(3):
        place_svc.file_open_now_report(make_user(f"r{i}"), open_place)
    assert place_svc.open_now_status(open_place) == "unverified"
    mod = make_user("mod2")
    mod.is_staff = True
    mod.save(update_fields=["is_staff"])
    cleared = place_svc.clear_open_now_reports(open_place, moderator=mod)
    assert cleared == 3
    assert place_svc.open_now_status(open_place) is True


@pytest.mark.django_db
def test_api_recent_report_annotation_drives_unverified(open_place):
    for i in range(3):
        place_svc.file_open_now_report(make_user(f"r{i}"), open_place)
    body = APIClient().get("/api/places/").json()
    feature = next(f for f in body["features"] if f["id"] == open_place.id)
    assert feature["properties"]["open_now"] == "unverified"
