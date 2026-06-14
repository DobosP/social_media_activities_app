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
The patterns are therefore authored in the regex subset that Python ``re`` and the JS RegExp
engine parse identically (``(?:)``, ``|``, ``\\d``, ``\\s``, ``\\b``, character classes,
quantifiers — no lookbehind, named groups, backreferences, or ``\\p{}``). Keyword anchors
stay ASCII so ``\\b`` behaves the same in both engines (a leading-diacritic Romanian word
like "șoseaua" would break the JS ``\\b``, so we match its ASCII spelling — an honest
limitation for an advisory signal, not a safety wall).

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
        # A run of 9+ digits with single space/dot/hyphen separators — RO mobile/landline
        # (07xx xxx xxx / 0264 xxx xxx) and international (+40 ...). Times/dates (≤8 digits, or
        # broken by ":" / "/") fall under the threshold so they don't trip it.
        "key": "phone",
        "pattern": r"(?:\+|00)?\d(?:[ .\-]?\d){8,}",
        "flags": "",
    },
    {
        "key": "email",
        "pattern": r"[^\s@]+@[^\s@]+\.[^\s@]+",
        "flags": "",
    },
    {
        # A street address in either ordering: RO keyword-first ("Strada Memorandumului 28",
        # "Calea Turzii"), EN number-first ("12 Oak Street"), or a RO house number ("nr. 5").
        # \b keeps "str" out of "construct". "road/lane/drive" are deliberately EXCLUDED — they
        # fire on ordinary speech ("5 minutes drive away", "down the road").
        "key": "address",
        "pattern": (
            r"\b(?:"
            r"(?:strada|str|calea|bulevardul|bulevard|bdul|aleea|soseaua|piata)\b\.?\s+\S+"
            r"|\d+\s+(?:\w+\s+){0,2}(?:street|avenue|boulevard|blvd)\b"
            r"|nr\.?\s*\d+"
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
