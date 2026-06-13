"""F11 — the deterministic RO/EN contact-detail keyword scan (pure, no DB)."""

import pytest

from apps.safety.triage_keywords import contact_hint_terms, has_contact_hint


@pytest.mark.parametrize(
    "text",
    [
        "let's chat on WhatsApp instead",
        "add me on telegram",
        "what's your phone number?",
        "call me later ok",
        "dm me",
        "this is our secret, don't tell anyone",
        "my number is 0712 345 678",
        "reach me at someone@example.com",
        "hai sa vorbim pe whatsapp",  # RO: let's talk on whatsapp
        "care e numarul tau de telefon",  # RO: what's your phone number
        "scrie-mi pe privat",  # RO: write to me privately (scrie-mi)
        "e secretul nostru, nu spune nimanui",  # RO: our secret, don't tell
    ],
)
def test_flags_contact_solicitations(text):
    assert has_contact_hint(text) is True
    assert contact_hint_terms(text)  # non-empty


@pytest.mark.parametrize(
    "text",
    [
        "Great game today, see you all at the park next week!",
        "Bring water and comfortable shoes.",
        "Ne vedem la biblioteca sambata.",  # RO: see you at the library Saturday
        "",
    ],
)
def test_does_not_flag_innocuous_text(text):
    assert has_contact_hint(text) is False
    assert contact_hint_terms(text) == []


def test_diacritic_insensitive():
    # With and without Romanian diacritics both match.
    assert has_contact_hint("numărul tău de telefon") is True
    assert has_contact_hint("numarul tau de telefon") is True


def test_deterministic_sorted_unique():
    text = "whatsapp whatsapp telegram, call me, my number 0712345678"
    a = contact_hint_terms(text)
    b = contact_hint_terms(text)
    assert a == b  # deterministic
    assert a == sorted(set(a))  # sorted + de-duplicated


def test_short_digit_runs_are_not_phone_numbers():
    assert "phone-number" not in contact_hint_terms("we are 5 people, room 12")


@pytest.mark.parametrize(
    "text",
    [
        "add message to the board",  # 'add me' must not match inside 'add message'
        "I sent a snapshot of the field",  # 'snap' must not match inside 'snapshot'
        "reply instantly please",  # 'insta' must not match inside 'instantly'
        "that was a discordant note",  # 'discord' must not match inside 'discordant'
        "the coach was signaling us",  # 'signal' must not match inside 'signaling'
        "a short history of telegraphy",  # 'telegram' must not match inside 'telegraphy'
        "call message center tomorrow",  # 'call me' must not match across 'call message'
    ],
)
def test_word_boundary_suppresses_substring_false_positives(text):
    assert has_contact_hint(text) is False


def test_dotted_version_numbers_are_not_phone_numbers():
    assert "phone-number" not in contact_hint_terms("upgrade to version 2.5.4.2.1.6.7 today")
