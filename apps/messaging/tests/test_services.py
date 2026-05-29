import pytest

from apps.accounts.models import Cohort
from apps.messaging import services
from apps.messaging.models import Message, MessageKey, Participant, PublicKey
from apps.safety.services import block_user

from .conftest import PUBLIC_JWK, keys_for

pytestmark = pytest.mark.django_db


# --- key registry ---
def test_register_public_key_rejects_private_material(adult_a):
    with pytest.raises(services.MessagingError):
        services.register_public_key(adult_a, {**PUBLIC_JWK, "d": "PRIVATE"})


def test_register_public_key_rejects_garbage(adult_a):
    with pytest.raises(services.MessagingError):
        services.register_public_key(adult_a, {"kty": "EC"})  # no x


def test_register_public_key_rotates_keeping_one_active(adult_a):
    services.register_public_key(adult_a, PUBLIC_JWK)
    services.register_public_key(adult_a, {**PUBLIC_JWK, "x": "TkVX"})
    assert PublicKey.objects.filter(user=adult_a, active=True).count() == 1
    assert PublicKey.objects.filter(user=adult_a).count() == 2
    assert services.public_key_for(adult_a).public_jwk["x"] == "TkVX"


def test_register_accepts_opaque_backup_blob(adult_a):
    key = services.register_public_key(
        adult_a, PUBLIC_JWK, wrapped_private_jwk={"ct": "opaque", "iv": "x"}
    )
    assert key.wrapped_private_jwk == {"ct": "opaque", "iv": "x"}


# --- the anti-grooming contact gate ---
def test_can_message_same_cohort(adult_a, adult_b):
    assert services.can_message(adult_a, adult_b) is True


def test_cannot_message_across_cohorts(adult_a, child, teen):
    assert services.can_message(adult_a, child) is False
    assert services.can_message(child, adult_a) is False
    assert services.can_message(teen, adult_a) is False
    assert services.can_message(teen, child) is False


def test_unassigned_cohort_cannot_message(adult_a, unverified):
    assert services.can_message(unverified, adult_a) is False
    assert services.can_message(adult_a, unverified) is False


def test_blocked_users_cannot_message(adult_a, adult_b):
    block_user(adult_a, adult_b)
    assert services.can_message(adult_a, adult_b) is False
    assert services.can_message(adult_b, adult_a) is False


def test_cannot_message_self(adult_a):
    assert services.can_message(adult_a, adult_a) is False


# --- starting conversations ---
def test_start_direct_sets_invite_accept_states(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    a = conv.participants.get(user=adult_a)
    b = conv.participants.get(user=adult_b)
    assert a.state == Participant.State.ACTIVE and a.role == Participant.Role.ADMIN
    assert b.state == Participant.State.INVITED
    assert conv.cohort == Cohort.ADULT


def test_start_direct_cross_cohort_blocked(adult_a, child):
    with pytest.raises(services.MessagingError):
        services.start_direct(adult_a, child)


def test_start_direct_reuses_existing(adult_a, adult_b):
    first = services.start_direct(adult_a, adult_b)
    second = services.start_direct(adult_a, adult_b)
    assert first.id == second.id


def test_start_group_requires_a_member(adult_a):
    with pytest.raises(services.MessagingError):
        services.start_group(adult_a, [])


def test_start_group_invites_all_targets(adult_a, adult_b, adult_c):
    conv = services.start_group(adult_a, [adult_b, adult_c], title="Hikers")
    assert conv.title == "Hikers"
    assert conv.participants.get(user=adult_a).state == Participant.State.ACTIVE
    assert conv.participants.filter(state=Participant.State.INVITED).count() == 2


def test_start_group_rejects_cross_cohort_member(adult_a, adult_b, child):
    with pytest.raises(services.MessagingError):
        services.start_group(adult_a, [adult_b, child])


# --- membership lifecycle ---
def test_accept_decline_leave(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    services.accept_invite(adult_b, conv)
    assert conv.participants.get(user=adult_b).state == Participant.State.ACTIVE
    # Cannot accept twice.
    with pytest.raises(services.MessagingError):
        services.accept_invite(adult_b, conv)
    services.leave(adult_b, conv)
    assert conv.participants.get(user=adult_b).state == Participant.State.LEFT


def test_decline_invite(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    services.decline_invite(adult_b, conv)
    assert conv.participants.get(user=adult_b).state == Participant.State.DECLINED


def test_add_participant_admin_only_and_cohort_locked(adult_a, adult_b, adult_c, child):
    conv = services.start_group(adult_a, [adult_b])
    services.accept_invite(adult_b, conv)
    # Non-admin member cannot add.
    with pytest.raises(services.MessagingError):
        services.add_participant(adult_b, conv, adult_c)
    # Admin can add a same-cohort user.
    services.add_participant(adult_a, conv, adult_c)
    assert conv.participants.get(user=adult_c).state == Participant.State.INVITED
    # Even an admin cannot add across cohorts.
    with pytest.raises(services.MessagingError):
        services.add_participant(adult_a, conv, child)


def test_remove_participant(adult_a, adult_b):
    conv = services.start_group(adult_a, [adult_b])
    services.accept_invite(adult_b, conv)
    services.remove_participant(adult_a, conv, adult_b)
    assert conv.participants.get(user=adult_b).state == Participant.State.REMOVED


# --- the zero-knowledge write/read path ---
def _active_direct(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    services.accept_invite(adult_b, conv)
    return conv


def test_post_message_stores_ciphertext_and_per_recipient_keys(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    msg = services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )
    stored = Message.objects.get(pk=msg.id)
    assert stored.ciphertext == "Y2lwaGVy"
    # One wrapped key per active member (sender included).
    assert MessageKey.objects.filter(message=stored).count() == 2
    assert set(MessageKey.objects.values_list("recipient_id", flat=True)) == {
        adult_a.id,
        adult_b.id,
    }


def test_post_message_requires_active_membership(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)  # b still INVITED
    with pytest.raises(services.MessagingError):
        services.post_message(adult_b, conv, ciphertext="x", iv="y", recipient_keys=keys_for(conv))


def test_post_message_rejects_incomplete_recipient_set(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    only_self = keys_for(conv, users=[adult_a])  # drops adult_b
    with pytest.raises(services.MessagingError):
        services.post_message(adult_a, conv, ciphertext="x", iv="y", recipient_keys=only_self)


def test_post_message_rejects_outsider_key(adult_a, adult_b, adult_c):
    conv = _active_direct(adult_a, adult_b)
    smuggled = keys_for(conv) + keys_for(conv, users=[adult_c])  # adult_c isn't a member
    with pytest.raises(services.MessagingError):
        services.post_message(adult_a, conv, ciphertext="x", iv="y", recipient_keys=smuggled)


def test_post_message_rejects_missing_key_fields(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    bad = keys_for(conv)
    bad[0].pop("wrapped_key")
    with pytest.raises(services.MessagingError):
        services.post_message(adult_a, conv, ciphertext="x", iv="y", recipient_keys=bad)


def test_messages_for_returns_only_callers_own_key(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )
    for_b = services.messages_for(adult_b, conv)
    assert len(for_b) == 1
    assert for_b[0].my_key.recipient_id == adult_b.id


def test_invited_user_sees_no_prior_history(adult_a, adult_b):
    """Messages sent before a user accepts carry no key for them, so first-contact
    content stays unreadable until they opt in."""
    conv = services.start_direct(adult_a, adult_b)  # b INVITED
    services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )  # only adult_a is active -> wrapped to self only
    services.accept_invite(adult_b, conv)
    assert services.messages_for(adult_b, conv) == []


def test_conversations_for_lists_active_and_pending(adult_a, adult_b):
    services.start_direct(adult_a, adult_b)
    assert services.conversations_for(adult_a).count() == 1
    assert services.conversations_for(adult_b).count() == 1  # pending invite shows
    assert services.conversations_for(adult_b, include_pending=False).count() == 0


# --- moderation under E2EE ---
def test_report_message_files_report_with_excerpt(adult_a, adult_b):
    conv = _active_direct(adult_a, adult_b)
    msg = services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )
    report = services.report_message(
        adult_b, msg, reason="harassment", decrypted_excerpt="the mean thing they said"
    )
    assert "the mean thing they said" in report.detail
    assert report.reason == "harassment"


def test_report_requires_membership(adult_a, adult_b, adult_c):
    conv = _active_direct(adult_a, adult_b)
    msg = services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )
    with pytest.raises(services.MessagingError):
        services.report_message(adult_c, msg, reason="spam")
