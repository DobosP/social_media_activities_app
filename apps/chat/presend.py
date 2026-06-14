"""F33 — pre-send safety nudge: the SINGLE source of truth for the contact-leak ruleset.

The nudge is a calm, dismissible "are you sure?" shown CLIENT-side when a thread post looks
about to share a phone number, an email, a street address, or a plan to meet one-on-one /
off-platform. The whole point is to catch the highest-harm leak at *authorship* time —
nothing leaves the device for a message the author abandons.

The ruleset lives here, in ONE place, and is consumed by two halves that must never drift:

* the server (``apps.chat.policy.NudgeMessagePolicy``) runs :func:`scan_text` over an
  already-accepted body for parity/auditing — a SOFT, non-blocking signal only; and
* the browser, which compiles the SAME patterns emitted verbatim by :func:`client_ruleset`
  (``new RegExp(pattern, flags)`` in ``static/js/presend-nudge.js``).

Because the patterns are emitted once and compiled on both sides, the two halves can't drift.
For that to hold the patterns must *behave* identically in Python ``re`` and the JS RegExp
engine — not merely look alike. The two engines differ on Unicode-aware shorthands, so the
patterns deliberately AVOID them:

* ``\\d`` / ``\\w`` are Unicode-aware in Python ``re`` but ASCII-only in JS RegExp (which is
  compiled without the ``u`` flag). We therefore use explicit ASCII classes — ``[0-9]`` and
  ``[A-Za-z]`` — so both engines agree regardless of input. A lint test (test_presend.py)
  forbids ``\\d``/``\\w`` from ever creeping back in.
* ``\\b`` is anchored only on ASCII letters/digits (the keyword arms are ASCII), so the
  word-boundary semantics match in both engines.
* Allowed subset: ``(?:)``, ``|``, lookAHEAD ``(?=)``, ``\\s``/``\\S``, ``\\b``, character
  classes, bounded quantifiers. Forbidden (Python-only or needs the JS ``u`` flag):
  lookBEHIND, named groups/backreferences, ``\\p{}``.

Honest limitation: because the ASCII ``[A-Za-z]`` class can't span a diacritic, a Romanian
street NAME written with diacritics in number-first order ("5 Bună street") is matched by
NEITHER engine — they agree (no drift), and the keyword-first form ("Strada Bună Ziua") is
still caught by the RO arm's ``\\S+`` tail. This is an advisory signal, not a safety wall.

This is a HEURISTIC, not a filter: it never blocks, never redacts, never reports, and some
false positives are acceptable (the user simply confirms and proceeds). The real recourse
for a genuine off-platform-contact concern stays the human-initiated OFF_PLATFORM report.
"""

import re

# Each rule: a stable ``key``, a ``pattern`` authored in the shared Python/JS subset, and the
# regex ``flags`` ("i" => case-insensitive on both sides). Order is stable so scan results are
# deterministic. Adding/refining a rule here updates BOTH the server scan and the client nudge.
PRESEND_RULES = (
    {
        # A telephone-SHAPED run, not just any long digit string: either a trunk prefix
        # (+ / 00) followed by a long run, or a leading-0 national number (RO mobile 07xx xxx xxx
        # / landline 0xxx xxx xxx). Requiring the prefix is what keeps multi-number content that
        # is NOT a phone from tripping it — sports scores ("21 22 23 24 25") and PINs
        # ("12345 6789") have no +/00 and don't start 0+two-digits. Times/dates break on ":"/"/".
        "key": "phone",
        "pattern": r"(?:\+|00)[0-9 .\-]{7,}[0-9]|\b0[0-9]{2}[0-9 .\-]{5,}[0-9]\b",
        "flags": "",
    },
    {
        # An email address. Local/domain parts are length-bounded so the two greedy runs can't
        # backtrack quadratically on a long no-dot tail (the CHAT_MAX_LENGTH cap also bounds it).
        "key": "email",
        "pattern": r"[^\s@]{1,64}@[^\s@]{1,255}\.[^\s@]{2,}",
        "flags": "",
    },
    {
        # A street address in either ordering: RO keyword-first ("Strada Memorandumului 28",
        # "Calea Turzii") or EN number-first ("12 Oak Street"). The EN arm requires the street
        # word to END the phrase (lookahead: punctuation, end, or a following number) so compound
        # nouns like "street level" / "avenue trees" / "street lamps" don't fire. \b keeps "str"
        # out of "construct". "road/lane/drive" stay EXCLUDED — they fire on ordinary speech
        # ("5 minutes drive away", "down the road"). A bare house number ("nr. 5") is NOT a rule
        # on its own — it fired on jersey/ranking numbers — RO addresses carry it under a keyword.
        "key": "address",
        "pattern": (
            r"\b(?:"
            r"(?:strada|str|calea|bulevardul|bulevard|bdul|aleea|soseaua|piata)\b\.?\s+\S+"
            r"|[0-9]{1,4}\s+(?:[A-Za-z]+\s+){0,2}(?:street|avenue|boulevard|blvd)"
            r"(?=[.,]|\s*$|\s+[0-9])"
            r")"
        ),
        "flags": "i",
    },
    {
        # A plan to meet privately / off-platform (RO + EN). ASCII-anchored leads so \b is
        # engine-identical; the [ăa] class matches "acasă"/"acasa" as plain literals.
        "key": "meet_alone",
        "pattern": (
            r"\b(?:(?:come|meet)\s+(?:me\s+|up\s+)?alone|just\s+(?:the\s+)?two\s+of\s+us|"
            r"to\s+my\s+(?:place|house|apartment|flat|home)|at\s+my\s+(?:place|house|apartment|flat)|"
            r"vino\s+singur|ne\s+vedem\s+singur|doar\s+noi\s+doi|(?:vino|hai)\s+la\s+mine|"
            r"acas[ăa]\s+la\s+mine)"
        ),
        "flags": "i",
    },
)


def _compile(rule):
    return re.compile(rule["pattern"], re.IGNORECASE if "i" in rule["flags"] else 0)


# Compiled once at import. A malformed pattern fails loudly here (and in tests), never silently.
_COMPILED = tuple((rule["key"], _compile(rule)) for rule in PRESEND_RULES)


def scan_text(text):
    """Return the keys of every rule that matches ``text`` (deterministic order, deduped).

    Pure and side-effect-free: this NEVER blocks, redacts, or reports — it only reports which
    advisory patterns matched, for the server-side parity signal and for tests.
    """
    if not text:
        return []
    return [key for key, rx in _COMPILED if rx.search(text)]


def client_ruleset():
    """The ruleset emitted verbatim to the browser (compiled there with ``new RegExp``).

    Returns plain dicts (no translated copy — the user-facing confirm message is supplied by
    the view) so the client and server share exactly the same patterns and can't drift.
    """
    return [{"key": r["key"], "pattern": r["pattern"], "flags": r["flags"]} for r in PRESEND_RULES]
