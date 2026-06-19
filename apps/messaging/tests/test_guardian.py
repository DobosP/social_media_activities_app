import pytest
from rest_framework.test import APIClient

from apps.accounts.models import AgeBand, GuardianRelationship
from apps.messaging import services
from apps.messaging.models import Participant

from .conftest import PUBLIC_JWK, keys_for, make_user

pytestmark = pytest.mark.django_db


def client_for(user):
    client = APIClient()
    client.force_authenticate(user=user)
    return client


@pytest.fixture
def child1(db):
    return make_user("ward_one", age_band=AgeBand.UNDER_16)


@pytest.fixture
def child2(db):
    return make_user("ward_two", age_band=AgeBand.UNDER_16)


@pytest.fixture
def guardian(db):
    g = make_user("the_guardian", age_band=AgeBand.ADULT)
    services.register_public_key(g, PUBLIC_JWK)
    return g


@pytest.fixture
def kids_chat(child1, child2):
    conv = services.start_direct(child1, child2)
    services.accept_invite(child2, conv)
    return conv


def _link(guardian, ward):
    return GuardianRelationship.objects.create(guardian=guardian, ward=ward)


# --- enrollment rules ---
def test_guardian_can_observe_wards_conversation(guardian, child1, kids_chat):
    _link(guardian, child1)
    p = services.add_guardian_observer(guardian, kids_chat)
    assert p.role == Participant.Role.GUARDIAN
    assert p.state == Participant.State.ACTIVE
    # Transparent: the guardian is now a visible active member.
    assert services.is_active_participant(guardian, kids_chat)


def test_non_guardian_cannot_observe(adult_a, kids_chat):
    services.register_public_key(adult_a, PUBLIC_JWK)
    with pytest.raises(services.MessagingError):
        services.add_guardian_observer(adult_a, kids_chat)


def test_guardian_of_uninvolved_child_cannot_observe(guardian, child1, child2):
    # child1 is the ward, but the conversation is between two *other* children.
    other_a = make_user("kid_a", age_band=AgeBand.UNDER_16)
    other_b = make_user("kid_b", age_band=AgeBand.UNDER_16)
    conv = services.start_direct(other_a, other_b)
    services.accept_invite(other_b, conv)
    _link(guardian, child1)
    with pytest.raises(services.MessagingError):
        services.add_guardian_observer(guardian, conv)


def test_guardian_needs_a_key(child1, kids_chat):
    g = make_user("keyless_guardian", age_band=AgeBand.ADULT)  # no key registered
    _link(g, child1)
    with pytest.raises(services.MessagingError):
        services.add_guardian_observer(g, kids_chat)


def test_teen_ward_not_observable(guardian, kids_chat):
    # A 16-17 ward is past the consent-gated cohort; no oversight.
    teen = make_user("teen_ward", age_band=AgeBand.AGE_16_17)
    other = make_user("teen_peer", age_band=AgeBand.AGE_16_17)
    conv = services.start_direct(teen, other)
    services.accept_invite(other, conv)
    _link(guardian, teen)
    with pytest.raises(services.MessagingError):
        services.add_guardian_observer(guardian, conv)


# --- read-only + protection ---
def test_guardian_can_read_but_not_send(guardian, child1, child2, kids_chat):
    _link(guardian, child1)
    services.add_guardian_observer(guardian, kids_chat)
    # A child sends; keys_for now includes the guardian, so it's wrapped to them.
    services.post_message(
        child1, kids_chat, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(kids_chat)
    )
    # Guardian receives a decryptable copy...
    msgs = services.messages_for(guardian, kids_chat)
    assert len(msgs) == 1 and msgs[0].my_key is not None
    # ...but cannot send into a children's conversation.
    with pytest.raises(services.MessagingError):
        services.post_message(
            guardian, kids_chat, ciphertext="x", iv="y", recipient_keys=keys_for(kids_chat)
        )


def test_guardian_cannot_be_removed_by_member(guardian, child1, kids_chat):
    _link(guardian, child1)
    services.add_guardian_observer(guardian, kids_chat)
    # child1 is the conversation admin (creator); still can't evict oversight.
    with pytest.raises(services.MessagingError):
        services.remove_participant(child1, kids_chat, guardian)


def test_guardian_can_leave(guardian, child1, kids_chat):
    _link(guardian, child1)
    services.add_guardian_observer(guardian, kids_chat)
    services.leave(guardian, kids_chat)
    assert not services.is_active_participant(guardian, kids_chat)


# --- participant keys (cross-cohort, membership-scoped) ---
def test_participant_keys_includes_guardian(guardian, child1, child2, kids_chat):
    services.register_public_key(child1, PUBLIC_JWK)
    services.register_public_key(child2, PUBLIC_JWK)
    _link(guardian, child1)
    services.add_guardian_observer(guardian, kids_chat)
    keys = services.participant_keys(child1, kids_chat)
    roles = {k["username"]: k["role"] for k in keys}
    assert roles[guardian.username] == "guardian"
    assert len(keys) == 3  # child1, child2, guardian


def test_participant_keys_is_not_n_plus_one(
    guardian, child1, child2, kids_chat, django_assert_max_num_queries
):
    """PERF-4: public keys are fetched in ONE batched query, not one per participant — so the
    query count stays a small CONSTANT (a per-participant N+1 here amplified on group chats)."""
    services.register_public_key(child1, PUBLIC_JWK)
    services.register_public_key(child2, PUBLIC_JWK)
    _link(guardian, child1)
    services.add_guardian_observer(guardian, kids_chat)
    with django_assert_max_num_queries(4):  # is_active + participants + ONE keys query
        services.participant_keys(child1, kids_chat)


# --- API ---
def test_guardian_discovery_and_enroll_endpoints(guardian, child1, kids_chat):
    _link(guardian, child1)
    disco = client_for(guardian).get("/api/messaging/guardian/conversations/")
    assert disco.status_code == 200
    assert any(c["id"] == kids_chat.id for c in disco.data)

    enroll = client_for(guardian).post(f"/api/messaging/conversations/{kids_chat.id}/guardian/")
    assert enroll.status_code == 201
    roles = [p["role"] for p in enroll.data["participants"]]
    assert "guardian" in roles


def test_conversation_keys_endpoint(child1, child2, kids_chat):
    services.register_public_key(child1, PUBLIC_JWK)
    services.register_public_key(child2, PUBLIC_JWK)
    resp = client_for(child1).get(f"/api/messaging/conversations/{kids_chat.id}/keys/")
    assert resp.status_code == 200
    assert {k["username"] for k in resp.data} == {child1.username, child2.username}


def test_conversation_keys_forbidden_for_outsider(child1, child2, kids_chat):
    outsider = make_user("nosy", age_band=AgeBand.UNDER_16)
    resp = client_for(outsider).get(f"/api/messaging/conversations/{kids_chat.id}/keys/")
    assert resp.status_code == 403
