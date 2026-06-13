"""F11 — deterministic, advisory RO/EN keyword scan for the staff moderation triage queue.

Pure Python (no Django, no ML, no network) so it can be unit-tested in isolation and is fully
deterministic. It flags a reported post body that contains common **contact-detail / move-this-
off-platform** solicitations — the classic grooming red flag of pulling a child to an unmonitored
channel. This is the LOWEST-WEIGHT advisory signal in triage_summary and MUST NEVER be the sole
sort key: it only breaks ties among reports already ranked by reason severity + child involvement
+ duplicate-report count. It takes no automated action and is never shown to a reported user.
"""

import re

# Off-platform app / channel names (EN + RO share these brand names).
_APP_TERMS = (
    "whatsapp",
    "telegram",
    "signal",
    "snapchat",
    "snap",
    "instagram",
    "insta",
    "discord",
    "messenger",
    "viber",
    "tiktok",
    "kik",
)

# Solicitation phrases — EN.
_EN_PHRASES = (
    "phone number",
    "your number",
    "my number",
    "call me",
    "text me",
    "dm me",
    "add me",
    "message me privately",
    "meet me alone",
    "come alone",
    "don't tell",
    "dont tell",
    "do not tell",
    "our secret",
    "keep it secret",
    "off the app",
    "off this app",
)

# Solicitation phrases — RO (diacritic-insensitive; we strip diacritics before matching).
_RO_PHRASES = (
    "numar de telefon",
    "numarul tau",
    "numarul meu",
    "suna-ma",
    "suna ma",
    "scrie-mi",
    "scrie mi",
    "adauga-ma",
    "adauga ma",
    "vino singur",
    "vino singura",
    "ne vedem singuri",
    "nu spune",
    "secretul nostru",
    "pastreaza secret",
)

# A run of 7+ digits separated only by single spaces/dashes — a likely phone number. Dots are
# NOT separators, so dotted version/part numbers ("2.5.4.2.1.6.7") don't false-positive.
_PHONE_RE = re.compile(r"(?:\d[ \-]?){7,}")
# A bare email address.
_EMAIL_RE = re.compile(r"[^\s@]+@[^\s@]+\.[^\s@]+")

_DIACRITICS = str.maketrans("ăâîșşțţ", "aaisstt")


def _normalize(text: str) -> str:
    return (text or "").casefold().translate(_DIACRITICS)


# Word-boundary matchers (compiled once). Boundaries stop short app names from matching inside
# innocuous words ("snap" in "snapshot", "insta" in "instant", "add me" in "add message").
_TERM_PATTERNS = {
    term: re.compile(rf"\b{re.escape(_normalize(term))}\b")
    for term in (*_APP_TERMS, *_EN_PHRASES, *_RO_PHRASES)
}


def contact_hint_terms(text: str) -> list[str]:
    """Return the sorted, de-duplicated list of contact/off-platform signals found in `text`.
    Deterministic; empty when nothing matches. Used by triage_summary (advisory only). Matching is
    word-boundary-anchored so short brand names don't match inside ordinary words."""
    norm = _normalize(text)
    hits = {term for term, pattern in _TERM_PATTERNS.items() if pattern.search(norm)}
    if _PHONE_RE.search(norm):
        hits.add("phone-number")
    if _EMAIL_RE.search(text or ""):
        hits.add("email-address")
    return sorted(hits)


def has_contact_hint(text: str) -> bool:
    return bool(contact_hint_terms(text))
