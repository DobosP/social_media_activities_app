"""F33 — pre-send safety nudge: the shared ruleset + the soft, non-blocking server policy."""

import re

import pytest

from apps.chat.policy import BasicMessagePolicy, NudgeMessagePolicy, ProcessedMessage
from apps.chat.presend import PRESEND_RULES, client_ruleset, scan_text

# Representative messages that SHOULD trip the nudge (RO + EN), keyed by the expected rule.
RISKY = {
    "phone": [
        "call me on 0712 345 678",
        "my number is +40712345678",
        "sună-mă la 0264-123-456",
    ],
    "email": [
        "email me at ana.pop@gmail.com",
        "scrie-mi pe ionut@example.ro ok?",
    ],
    "address": [
        "I live at 12 Oak Street",
        "ne vedem pe Strada Memorandumului 28",
        "vino pe Calea Turzii nr 5",
    ],
    "meet_alone": [
        "let's meet alone after",
        "just the two of us is fine",
        "come over to my place",
        "vino singur te rog",
        "hai la mine după",
        "ne putem vedea acasă la mine",
    ],
}

# Ordinary, healthy meetup logistics that must NOT trip it — a false alarm here would train
# people to dismiss the nudge, defeating the point. Includes the false-positive classes the
# adversarial review surfaced: multi-number score lists / PINs, jersey & ranking numbers, and
# "<street-word> <noun>" compound nouns — all common in sport/board-game threads.
CLEAN = [
    "Let's meet at the north gate at 6pm, bring water.",
    "Ne vedem la parc la ora 18, aduceți o minge.",
    "Great game today, see everyone next week!",
    "The library closes at 20:00 so let's start by 18:30.",
    "I scored 21 points haha",
    "scores were 21 22 23 24 25 wins",
    "the pin for the gate is 12345 6789",
    "nr 7 jersey is mine, you take nr. 9",
    "winners are nr. 1 and nr. 2",
    "12 people on street level for the indoor court",
    "we got 5 new street lamps near the pitch",
    "there are 3 main avenue trees giving shade",
    "5 minutes drive away, down the road",
]


@pytest.mark.parametrize("rule_key,samples", RISKY.items())
def test_scan_flags_risky_messages(rule_key, samples):
    for text in samples:
        assert rule_key in scan_text(text), f"expected {rule_key!r} to match: {text!r}"


@pytest.mark.parametrize("text", CLEAN)
def test_scan_passes_clean_logistics(text):
    assert scan_text(text) == [], f"unexpected nudge on clean message: {text!r}"


def test_scan_empty_is_quiet():
    assert scan_text("") == []
    assert scan_text(None) == []


def test_scan_returns_deterministic_deduped_keys():
    # Multiple distinct kinds in one message -> each key once, in PRESEND_RULES order.
    text = "call 0712345678 or email me at a@b.co, then come to my place"
    hits = scan_text(text)
    assert hits == ["phone", "email", "meet_alone"]
    assert len(hits) == len(set(hits))


def test_client_ruleset_mirrors_the_source_of_truth():
    rules = client_ruleset()
    assert [r["key"] for r in rules] == [r["key"] for r in PRESEND_RULES]
    for r in rules:
        # The emitted shape is exactly what static/js/presend-nudge.js feeds to `new RegExp`.
        assert set(r) == {"key", "pattern", "flags"}
        assert isinstance(r["pattern"], str) and r["pattern"]
        # Every emitted pattern must compile in Python too (parity guard against a bad edit).
        re.compile(r["pattern"], re.IGNORECASE if "i" in r["flags"] else 0)


# Constructs that parse in Python re but break (or behave differently in) the JS RegExp engine
# compiled without the `u` flag — see static/js/presend-nudge.js. \d and \w are forbidden because
# they are Unicode-aware in Python but ASCII-only in JS (the launch-market diacritic divergence);
# the rest are Python-only or need the u flag. This is the cheap, Node-free parity guard.
JS_INCOMPATIBLE = [
    r"\d",
    r"\D",
    r"\w",
    r"\W",
    r"(?P<",
    r"(?P=",
    r"(?#",
    r"\p{",
    r"\P{",
    r"(?<=",
    r"(?<!",
]


@pytest.mark.parametrize("rule", PRESEND_RULES, ids=lambda r: r["key"])
def test_patterns_use_only_the_shared_python_js_subset(rule):
    pat = rule["pattern"]
    for bad in JS_INCOMPATIBLE:
        assert bad not in pat, f"{rule['key']!r} uses JS-incompatible construct {bad!r}: {pat!r}"
    # No numbered backreferences (\1..\9) — unsupported as authored across both engines here.
    assert not re.search(r"\\[1-9]", pat), (
        f"{rule['key']!r} appears to use a backreference: {pat!r}"
    )
    assert rule["flags"] in ("", "i")


def test_diacritic_street_name_is_a_documented_parity_miss():
    # A number-first Romanian street name with diacritics is matched by NEITHER engine (the ASCII
    # [A-Za-z] class can't span "ă") — they AGREE, which is the point: no Python<->JS drift. The
    # keyword-first form is still caught (see below), so RO addresses aren't lost in practice.
    assert "address" not in scan_text("5 Bună street tonight")
    assert "address" in scan_text("ne vedem pe Strada Bună Ziua")


# --- the server policy half: a SOFT, non-blocking signal -------------------------------------


def test_nudge_policy_flags_a_leak_without_blocking():
    result = NudgeMessagePolicy().process(author=None, thread=None, body="call me on 0712 345 678")
    assert isinstance(result, ProcessedMessage)
    assert result.allowed is True  # never blocks
    assert result.body == "call me on 0712 345 678"  # never alters the body
    assert result.redacted is False  # never redacts
    assert "phone" in result.nudge_hits


def test_nudge_policy_is_silent_on_clean_text():
    result = NudgeMessagePolicy().process(author=None, thread=None, body="see you at 6pm")
    assert result.allowed is True
    assert result.nudge_hits == ()


def test_nudge_policy_keeps_basic_posture():
    # Inherited BasicMessagePolicy behaviour is unchanged: empty rejected, body trimmed.
    empty = NudgeMessagePolicy().process(author=None, thread=None, body="   ")
    assert empty.allowed is False and empty.nudge_hits == ()
    trimmed = NudgeMessagePolicy().process(author=None, thread=None, body="  hi there  ")
    assert trimmed.allowed is True and trimmed.body == "hi there"


def test_basic_policy_never_sets_nudge_hits():
    # The plain policy must not gain the signal (it stays the no-op baseline).
    result = BasicMessagePolicy().process(author=None, thread=None, body="email a@b.co")
    assert result.nudge_hits == ()
