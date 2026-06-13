"""Accessibility facts (F15), derived at READ time from a venue's OSM tags, plus the per-user
stated access preference. Facts are derived from ``Place.raw_tags`` and never written back:
``ingest_places`` overwrites the whole raw_tags dict on every re-ingest, so anything stored
there (or on Place) would be clobbered. Deriving on read keeps the facts in sync for free and
honest — never claiming a venue is accessible when the tag is missing."""

from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Q
from django.utils import timezone

from .models import (
    AccessPreference,
    ApprovedChildVenue,
    ChildVenueClass,
    OpenNowReport,
    Partner,
    Place,
)


class PlacesError(Exception):
    """Expected, user-facing places-domain error (F28)."""


class NotEligible(PlacesError):
    """User fails the verified+consented gate."""


def public_places(qs=None):
    """The SINGLE visibility chokepoint for public Place reads (F25): keep every non-USER
    place, but keep a USER place ONLY once its co-creation proposal is PUBLISHED. A USER place
    with a PENDING/REJECTED proposal — or no proposal row at all — is hidden (a positive
    keep-filter, so a NULL proposal status correctly fails the second disjunct). EVERY
    AllowAny/anonymous Place surface must route through this."""
    from apps.social.models import UserPlaceProposal  # local: places must not import social at top

    qs = Place.objects.all() if qs is None else qs
    return qs.filter(
        ~Q(source=Place.Source.USER) | Q(proposal__status=UserPlaceProposal.Status.PUBLISHED)
    )


def edge_is_publicly_visible(edge) -> bool:
    """True if the edge's place is publicly visible — used to refuse F26 votes on a place that
    is still pending the co-creation quorum."""
    return public_places().filter(pk=edge.place_id).exists()


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


# --- F9: public meetup-place gate for children's activities --------------------------
# A CHILD-cohort meetup may only be set at a known public venue type (a STAFF-curated
# ChildVenueClass) or a per-place staff approval. Derived at READ time from the place's
# existing tags (never written onto Place — re-ingest safe, like accessibility_facts).

CHILD_VENUE_ALLOWED = "allowed"
CHILD_VENUE_UNKNOWN = "unknown"


def public_child_venue_class(place) -> str:
    """Classify a Place for CHILD-cohort meetups: ``"allowed"`` iff a staff per-place approval
    exists OR the place's tags match an ACTIVE ChildVenueClass for its source; else ``"unknown"``
    (fail-closed). We deliberately emit no ``"not_allowed"`` state: with no trustworthy denylist,
    anything not positively allowed is ``"unknown"`` — still fail-closed, but the UI offers a
    staff-approval path rather than asserting the venue is unsafe. Never writes to Place."""
    if place is None:
        return CHILD_VENUE_UNKNOWN
    # Per-place staff override (escape hatch for a legit-but-mistagged / non-OSM venue).
    if ApprovedChildVenue.objects.filter(place=place).exists():
        return CHILD_VENUE_ALLOWED
    tags = place.raw_tags or {}
    classes = list(ChildVenueClass.objects.filter(is_active=True))
    if place.source in (Place.Source.OSM, Place.Source.USER):
        for c in classes:
            crit = c.osm_match or {}
            # All criteria keys must equal the place's tags (mirrors ingestion._matches);
            # an empty criteria dict never matches (so it can't blanket-allow everything).
            if crit and all(tags.get(k) == v for k, v in crit.items()):
                return CHILD_VENUE_ALLOWED
    elif place.source == Place.Source.OVERTURE:
        cats = {tags.get("overture:category"), *(tags.get("overture:alternate") or [])}
        cats.discard(None)
        for c in classes:
            if cats & set(c.overture_categories or []):
                return CHILD_VENUE_ALLOWED
    # GOOGLE / any other source whose tag shape we don't yet resolve -> unknown (fail-closed).
    return CHILD_VENUE_UNKNOWN


def is_child_safe_venue(place) -> bool:
    """True iff `place` is an approved public venue type for a CHILD meetup (see
    public_child_venue_class). The single boolean both gates (create/join) consult."""
    return public_child_venue_class(place) == CHILD_VENUE_ALLOWED


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


def verified_partners():
    """Public list of verified civic partners (F37) — visibility gated by the manager."""
    return Partner.objects.public().select_related("place")


def partner_for_place(place):
    """The verified civic partner stewarding this place, if any (one acknowledgement line)."""
    return Partner.objects.public().filter(place=place).first()


# --- F28: open-now accuracy reports (ingest-safe overlay) -------------------------------


def _open_now_settings():
    return (
        getattr(settings, "OPEN_NOW_REPORT_THRESHOLD", 3),
        getattr(settings, "OPEN_NOW_REPORT_DECAY_SECONDS", 14 * 24 * 3600),
    )


def hours_reliable(place, *, now=None) -> bool:
    """True unless there are >= N independent reports within the decay window (auto-decay: old
    reports stop counting). Prefers a `recent_report_n` annotation when present (avoids a
    per-row query)."""
    threshold, decay = _open_now_settings()
    recent = getattr(place, "recent_report_n", None)
    if recent is None:
        cutoff = (now or timezone.now()) - timedelta(seconds=decay)
        recent = place.open_now_reports.filter(created_at__gte=cutoff).count()
    return recent < threshold


def open_now_status(place, *, now=None):
    """open/closed (bool) from parsed hours, downgraded to the 'unverified' sentinel when recent
    reports say the hours are wrong; None when hours are unknown. Read-time, ingest-safe (F28)."""
    from .enrichment.opening_hours import is_open_at

    base = is_open_at(place.opening_hours, now or timezone.localtime())
    if base is None:
        return None
    return base if hours_reliable(place, now=now) else "unverified"


@transaction.atomic
def file_open_now_report(reporter, place):
    """File one 'actually closed when it said open' report (F28). Idempotent per reporter per
    place per decay window (anti-brigading); rate-limited across venues. Returns the report, or
    None if throttled / already reported this window."""
    from apps.accounts.services import can_participate
    from apps.safety.services import allow_action

    if not can_participate(reporter):
        raise NotEligible("Verified, consented participation is required to report hours.")
    if not allow_action(
        reporter,
        "open_now_report",
        limit=getattr(settings, "OPEN_NOW_REPORT_RATE_LIMIT", 10),
        window_seconds=getattr(settings, "OPEN_NOW_REPORT_RATE_WINDOW_SECONDS", 3600),
    ):
        return None  # over the cross-venue rate limit
    _, decay = _open_now_settings()
    cutoff = timezone.now() - timedelta(seconds=decay)
    if place.open_now_reports.filter(reporter=reporter, created_at__gte=cutoff).exists():
        return None  # one report per reporter per place per window
    return OpenNowReport.objects.create(place=place, reporter=reporter)


@transaction.atomic
def clear_open_now_reports(place, *, moderator=None) -> int:
    """Moderator reset — delete all reports so the parsed hours self-heal on the next read."""
    n, _ = place.open_now_reports.all().delete()
    if moderator is not None:
        from apps.safety.services import record_audit

        record_audit("place.open_now_reports_cleared", actor=moderator, target=place)
    return n
