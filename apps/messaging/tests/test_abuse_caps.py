"""Abuse / size caps on the zero-knowledge send path (W2-12).

These guard the relay against denial-of-service via oversized ciphertext or huge
recipient fan-out, and assert the rate-limit check fires before any per-recipient
work so a throttled sender can't make the server do work first.
"""

import pytest

from apps.messaging import services
from apps.messaging.models import Message, MessageKey

from .conftest import keys_for

pytestmark = pytest.mark.django_db


def _active_direct(adult_a, adult_b):
    conv = services.start_direct(adult_a, adult_b)
    services.accept_invite(adult_b, conv)
    return conv


# --- ciphertext size cap ---
def test_post_message_rejects_oversized_ciphertext(adult_a, adult_b, settings):
    settings.MESSAGING_MAX_CIPHERTEXT_BYTES = 16
    conv = _active_direct(adult_a, adult_b)
    too_big = "A" * 17  # 17 bytes > 16-byte cap
    with pytest.raises(services.MessagingError):
        services.post_message(
            adult_a, conv, ciphertext=too_big, iv="aXY=", recipient_keys=keys_for(conv)
        )
    # Nothing was stored for the rejected message.
    assert Message.objects.count() == 0
    assert MessageKey.objects.count() == 0


def test_post_message_accepts_ciphertext_at_cap(adult_a, adult_b, settings):
    settings.MESSAGING_MAX_CIPHERTEXT_BYTES = 16
    conv = _active_direct(adult_a, adult_b)
    at_cap = "A" * 16  # exactly 16 bytes is allowed
    msg = services.post_message(
        adult_a, conv, ciphertext=at_cap, iv="aXY=", recipient_keys=keys_for(conv)
    )
    assert Message.objects.filter(pk=msg.id).exists()


def test_oversized_ciphertext_uses_byte_length_not_char_count(adult_a, adult_b, settings):
    """The cap is a byte limit: a multibyte character counts as its UTF-8 size, so a
    short string of multibyte chars can still exceed a small byte cap."""
    settings.MESSAGING_MAX_CIPHERTEXT_BYTES = 8
    conv = _active_direct(adult_a, adult_b)
    multibyte = "é" * 5  # 5 chars, 10 bytes in UTF-8 > 8-byte cap
    with pytest.raises(services.MessagingError):
        services.post_message(
            adult_a, conv, ciphertext=multibyte, iv="aXY=", recipient_keys=keys_for(conv)
        )


# --- recipient / participant count cap ---
def test_post_message_rejects_oversized_recipient_list(adult_a, adult_b, settings):
    settings.MESSAGING_MAX_GROUP_MEMBERS = 1  # the direct chat has 2 active members
    conv = _active_direct(adult_a, adult_b)
    with pytest.raises(services.MessagingError):
        services.post_message(
            adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
        )
    assert Message.objects.count() == 0


def test_post_message_within_member_cap_is_stored(adult_a, adult_b, settings):
    settings.MESSAGING_MAX_GROUP_MEMBERS = 2  # exactly the active-member count
    conv = _active_direct(adult_a, adult_b)
    msg = services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )
    assert MessageKey.objects.filter(message=msg).count() == 2


def test_oversized_recipient_list_rejected_before_rate_limit(adult_a, adult_b, settings):
    """A huge recipient list must be rejected before the rate-limit token is consumed,
    so the throttle can't be drained by oversized requests. With the cap at 1, the very
    first oversized send is rejected and a subsequent valid (small) send still succeeds
    even though the send rate limit is 1 per window."""
    settings.MESSAGING_MAX_GROUP_MEMBERS = 1
    settings.MESSAGING_SEND_RATE_LIMIT = 1
    conv = _active_direct(adult_a, adult_b)
    with pytest.raises(services.MessagingError):
        services.post_message(
            adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
        )
    # Raise the cap and send a legitimate message: the earlier rejection must not have
    # burned the single rate-limit token.
    settings.MESSAGING_MAX_GROUP_MEMBERS = 256
    msg = services.post_message(
        adult_a, conv, ciphertext="Y2lwaGVy", iv="aXY=", recipient_keys=keys_for(conv)
    )
    assert Message.objects.filter(pk=msg.id).exists()
