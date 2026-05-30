"""Accessibility facts (F15), derived at READ time from a venue's OSM tags, plus the per-user
stated access preference. Facts are derived from ``Place.raw_tags`` and never written back:
``ingest_places`` overwrites the whole raw_tags dict on every re-ingest, so anything stored
there (or on Place) would be clobbered. Deriving on read keeps the facts in sync for free and
honest — never claiming a venue is accessible when the tag is missing."""

from django.db import transaction

from .models import AccessPreference

FACT_TRUE = "true"
FACT_FALSE = "false"
FACT_LIMITED = "limited"
FACT_UNKNOWN = "unknown"


def _tristate(value, *, allow_limited=False) -> str:
    """Exact-match OSM value normaliser (no truthy shortcut): 'yes'->true, 'no'->false,
    optional 'limited'->limited, anything else/missing->unknown."""
    if value is None:
        return FACT_UNKNOWN
    v = str(value).strip().lower()
    if v == "yes":
        return FACT_TRUE
    if v == "no":
        return FACT_FALSE
    if allow_limited and v == "limited":
        return FACT_LIMITED
    return FACT_UNKNOWN


def accessibility_facts(place) -> dict:
    """Map the venue's stored OSM tags to honest accessibility states. Top-level OSM keys are
    safe to read because enrichment namespaces its sub-keys (e.g. raw_tags['google']). Returns
    a fixed-key dict; an empty or enrichment-only raw_tags yields ALL 'unknown'."""
    raw = place.raw_tags or {}
    return {
        "step_free": _tristate(raw.get("wheelchair"), allow_limited=True),
        "accessible_toilet": _tristate(raw.get("toilets:wheelchair")),
        "changing_table": _tristate(raw.get("changing_table")),
        "tactile_paving": _tristate(raw.get("tactile_paving")),
    }


# Ordered, low-literacy labels for the template (logic stays out of the template).
_FACT_LABELS = (
    ("step_free", "Step-free access"),
    ("accessible_toilet", "Accessible toilet"),
    ("changing_table", "Baby changing table"),
    ("tactile_paving", "Tactile paving"),
)


def accessibility_facts_display(place) -> list:
    """An ordered list of {key, label, state} rows. 'unknown' is rendered honestly by the
    template as 'not recorded' — never asserted as accessible."""
    facts = accessibility_facts(place)
    return [{"key": key, "label": label, "state": facts[key]} for key, label in _FACT_LABELS]


def matches_access_preference(facts, pref) -> str:
    """SOFT classifier for the badge — NEVER feeds a filter/exclude. Returns 'match' when every
    NEED the user set is satisfied (state 'true'), 'mismatch' when any set need is explicitly
    'false', else 'unknown' (covers missing data and the no-preference case, so a venue with
    unknown accessibility is never excluded). Only the needs with an OSM tag source participate."""
    if pref is None:
        return FACT_UNKNOWN
    needs = []
    if pref.needs_step_free:
        needs.append(facts.get("step_free", FACT_UNKNOWN))
    if pref.needs_accessible_toilet:
        needs.append(facts.get("accessible_toilet", FACT_UNKNOWN))
    if not needs:
        return FACT_UNKNOWN
    if any(state == FACT_FALSE for state in needs):
        return "mismatch"
    if all(state == FACT_TRUE for state in needs):
        return "match"
    return FACT_UNKNOWN


def get_access_preference(user):
    if not getattr(user, "is_authenticated", False):
        return None
    return AccessPreference.objects.filter(user=user).first()


@transaction.atomic
def set_access_preference(
    user, *, needs_step_free=False, needs_accessible_toilet=False, prefers_quiet=False
) -> AccessPreference:
    pref, _ = AccessPreference.objects.update_or_create(
        user=user,
        defaults={
            "needs_step_free": needs_step_free,
            "needs_accessible_toilet": needs_accessible_toilet,
            "prefers_quiet": prefers_quiet,
        },
    )
    return pref
