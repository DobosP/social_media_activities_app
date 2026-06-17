"""Accessibility facts (F15), derived at READ time from a venue's OSM tags, plus the per-user
stated access preference. Facts are derived from ``Place.raw_tags`` and never written back:
``ingest_places`` overwrites the whole raw_tags dict on every re-ingest, so anything stored
there (or on Place) would be clobbered. Deriving on read keeps the facts in sync for free and
honest — never claiming a venue is accessible when the tag is missing."""

from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Count, Q
from django.utils import timezone

from .models import (
    DEFAULT_CORRECTION_QUORUM,
    DEFAULT_FACT_QUORUM,
    AccessPreference,
    ApprovedChildVenue,
    ChildVenueClass,
    OpenNowReport,
    Partner,
    Place,
    PlaceClosureReport,
    PlaceCorrection,
    PlaceCorrectionConfirmation,
    PlaceFactVote,
)


class PlacesError(Exception):
    """Expected, user-facing places-domain error (F28)."""


class NotEligible(PlacesError):
    """User fails the verified+consented gate."""


class InvalidState(PlacesError):
    """The target isn't in a state that permits the action (e.g. a correction already resolved)."""


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
    ).exclude(
        # W3-F13: hide a venue a quorum of members reports as gone/permanently closed, so discovery
        # stops sending groups there AND the create_activity write-gate (which routes through this
        # same chokepoint) refuses it. A SELF-CONTAINED correlated subquery, NOT a named annotation,
        # so the ~20 callers that use public_places() as a .values('id') subquery or a
        # .filter(pk=...).exists() check inherit the block with no cooperation.
        pk__in=_closed_place_ids()
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
        # F32: a couple more honest yes/no OSM keys. hearing_loop is unambiguously binary; for
        # automatic_door the binary values map cleanly and any other value (button/motion/...)
        # falls to 'unknown' (fail-closed), never a false 'true'.
        "hearing_loop": _tristate(raw.get("hearing_loop")),
        "automatic_door": _tristate(raw.get("automatic_door")),
    }


# Ordered, low-literacy labels for the template (logic stays out of the template).
_FACT_LABELS = (
    ("step_free", "Step-free access"),
    ("accessible_toilet", "Accessible toilet"),
    ("changing_table", "Baby changing table"),
    ("tactile_paving", "Tactile paving"),
    ("hearing_loop", "Hearing loop"),
    ("automatic_door", "Automatic door"),
)


def accessibility_facts_display(place) -> list:
    """An ordered list of {key, label, state} rows. 'unknown' is rendered honestly by the
    template as 'not recorded' — never asserted as accessible."""
    facts = accessibility_facts(place)
    return [{"key": key, "label": label, "state": facts[key]} for key, label in _FACT_LABELS]


# W3-F15: plain-language state words for the read-aloud venue brief (honest about the unknowns).
_PLAIN_STATE = {
    FACT_TRUE: "yes",
    FACT_FALSE: "no",
    FACT_LIMITED: "limited",
    FACT_UNKNOWN: "not recorded",
}


def place_plain_brief(place, *, venue_fact_rows=None) -> list:
    """W3-F15: a deterministic, template-only "read it aloud" brief for a VENUE — short labelled
    declarative sentences for screen-reader, low-literacy and elderly users. Returns an ORDERED list
    of (label, sentence) pairs (render as ONE ARIA-landmarked region, no JS), mirroring W2-F27's
    plain_meetup_brief. Pure read, no PII, NO numeric counts.

    Always composed from place.display_name + display_address + the FREE accessibility dict-read
    (accessibility_facts_display). Crowd venue facts are appended only when the caller passes the
    pre-computed ``venue_fact_rows`` (the single-place detail surface). On the up-to-200-row places
    list the caller passes None, so this NEVER triggers a per-row venue_facts() fact_votes query —
    the N+1 the catalog warns of. venue_fact_rows is the {key,label,state} shape; counts ignored."""
    brief = [("Place", str(place.display_name))]
    address = (place.display_address or "").strip()
    if address:
        brief.append(("Where", f"It is at {address}."))
    rows = list(accessibility_facts_display(place))
    if venue_fact_rows:
        rows += list(venue_fact_rows)
    for row in rows:
        brief.append((row["label"], _PLAIN_STATE.get(row["state"], "not recorded")))
    return brief


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
    tags = place.raw_tags if isinstance(place.raw_tags, dict) else {}
    # The allowlist is a tiny, staff-curated table; we DELIBERATELY do not module-cache it (an
    # admin edit to a child-safety allowlist must take effect immediately, not after a restart).
    # It's read at most a couple of times per page with no per-item loop, so the cost is trivial.
    classes = list(ChildVenueClass.objects.filter(is_active=True))
    if place.source in (Place.Source.OSM, Place.Source.USER):
        for c in classes:
            crit = c.osm_match or {}
            # All criteria keys must equal the place's tags (mirrors ingestion._matches);
            # an empty criteria dict never matches (so it can't blanket-allow everything).
            if crit and all(tags.get(k) == v for k, v in crit.items()):
                return CHILD_VENUE_ALLOWED
    elif place.source == Place.Source.OVERTURE:
        # raw_tags is untrusted JSON — guard against a malformed (non-list) alternate so it can't
        # be iterated character-by-character and silently mis-match.
        alternate = tags.get("overture:alternate")
        alternate = alternate if isinstance(alternate, list) else []
        cats = {tags.get("overture:category"), *alternate}
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


# W4-F4: stable keys for the guardian-manifest "why this venue is child-approved" credit. The
# wards template maps each to a humanised label — never the raw OSM tags, so it can't become a
# tag-scraping oracle.
CHILD_VENUE_RATIONALE_STAFF = "staff_verified"
CHILD_VENUE_RATIONALE_RULE = "rule_match"


def child_venue_rationale(place) -> str | None:
    """W4-F4: a short, humanised reason a venue is permitted for CHILD meetups, for the /wards/
    manifest. Returns a stable key — "staff_verified" (a per-place ApprovedChildVenue override) or
    "rule_match" (the place's tags match an active public-venue rule). Returns None when the venue
    does NOT currently read 'allowed' (silent safe omission — never a false 'approved' claim for a
    since-deactivated rule). Reuses the fail-closed public_child_venue_class; emits only the
    'allowed' rationale, never an 'unknown/not-allowed' state."""
    if public_child_venue_class(place) != CHILD_VENUE_ALLOWED:
        return None
    if ApprovedChildVenue.objects.filter(place=place).exists():
        return CHILD_VENUE_RATIONALE_STAFF
    return CHILD_VENUE_RATIONALE_RULE


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
    if getattr(pref, "needs_hearing_loop", False):
        needs.append(facts.get("hearing_loop", FACT_UNKNOWN))
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
    user,
    *,
    needs_step_free=False,
    needs_accessible_toilet=False,
    needs_hearing_loop=False,
    prefers_quiet=False,
) -> AccessPreference:
    pref, _ = AccessPreference.objects.update_or_create(
        user=user,
        defaults={
            "needs_step_free": needs_step_free,
            "needs_accessible_toilet": needs_accessible_toilet,
            "needs_hearing_loop": needs_hearing_loop,
            "prefers_quiet": prefers_quiet,
        },
    )
    return pref


def _has_access_need(pref) -> bool:
    """True iff the viewer has stated at least one need that maps to an OSM-backed fact (so a
    needs-aware sort has something to act on). prefers_quiet is excluded on purpose — it has no
    honest venue source, so it must never reorder results."""
    return bool(
        pref
        and (
            pref.needs_step_free
            or pref.needs_accessible_toilet
            or getattr(pref, "needs_hearing_loop", False)
        )
    )


def sort_by_access_match(places, pref):
    """F32: a SOFT, needs-aware nudge over an ALREADY-ORDERED list of Place — stably move venues
    that CONFIRM every stated need to the front, leaving everyone else (mismatch AND unknown) in
    their original order. It NEVER hides or filters: an unknown-accessibility venue keeps its
    place, it is just not promoted. A no-op when the viewer stated no need (returns the list
    unchanged). Operates on a materialised list so it composes with the distance/name ordering
    the queryset already applied. Facts come from raw_tags already loaded — no extra query."""
    if not _has_access_need(pref):
        return places
    places = list(places)
    # Python sort is stable, so within "match" and within "the rest" the incoming order is kept.
    # False (match) sorts before True (not a match).
    places.sort(key=lambda p: matches_access_preference(accessibility_facts(p), pref) != "match")
    return places


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
    reports say the hours are wrong; None when hours are unknown. Read-time, ingest-safe (F28).
    W3-F14: reads ``display_opening_hours`` (the published crowd correction's re-parsed dict, else
    the raw ingested dict) so a quorum-corrected venue shows the accurate open/closed state."""
    from .enrichment.opening_hours import is_open_at

    base = is_open_at(place.display_opening_hours, now or timezone.localtime())
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


# --- W3-F13: 'this venue is gone' crowd closure overlay (ingest-safe) -------------------
# Mirrors the F28 open-now overlay: counts-only, identities-free, read-time decay, NEVER written to
# Place. Once a quorum of recent reports accrues, public_places() hides the venue from discovery AND
# the create_activity write-gate. Distinct from F28 (wrong-hours, which downgrades but never hides).


def _closure_settings():
    return (
        getattr(settings, "CLOSURE_REPORT_THRESHOLD", 3),
        getattr(settings, "CLOSURE_REPORT_DECAY_SECONDS", 14 * 24 * 3600),
    )


def _closed_place_ids(*, now=None):
    """The ids of places the closure overlay hides: >= threshold closure reports within the decay
    window. A read-time correlated set (recomputed each call as the window slides), so a venue
    self-heals once reports age out. Baked into public_places() as a subquery; never written to
    Place (re-ingest safe)."""
    threshold, decay = _closure_settings()
    cutoff = (now or timezone.now()) - timedelta(seconds=decay)
    return (
        PlaceClosureReport.objects.filter(created_at__gte=cutoff)
        .values("place")
        .annotate(n=Count("id"))
        .filter(n__gte=threshold)
        .values_list("place", flat=True)
    )


def place_is_closed(place, *, now=None) -> bool:
    """True once >= N independent closure reports accrue within the decay window (auto-decay: old
    reports stop counting). The inverse-shaped sibling of hours_reliable. Prefers a
    `recent_closure_n` annotation, but that fast-path is RESERVED for a future place-list surface:
    hiding happens upstream at the public_places() chokepoint, so no production read calls this per
    row today (which is why no queryset annotates recent_closure_n yet)."""
    threshold, decay = _closure_settings()
    recent = getattr(place, "recent_closure_n", None)
    if recent is None:
        cutoff = (now or timezone.now()) - timedelta(seconds=decay)
        recent = place.closure_reports.filter(created_at__gte=cutoff).count()
    return recent >= threshold


@transaction.atomic
def file_closure_report(reporter, place):
    """File one 'this venue is gone / permanently closed' report (W3-F13). Idempotent per reporter
    per place per decay window (anti-brigading); rate-limited across venues. Returns the report, or
    None if throttled / already reported this window. Mirrors file_open_now_report exactly."""
    from apps.accounts.services import can_participate
    from apps.safety.services import allow_action

    if not can_participate(reporter):
        raise NotEligible("Verified, consented participation is required to report a closed venue.")
    if not allow_action(
        reporter,
        "place_closure_report",
        limit=getattr(settings, "CLOSURE_REPORT_RATE_LIMIT", 10),
        window_seconds=getattr(settings, "CLOSURE_REPORT_RATE_WINDOW_SECONDS", 3600),
    ):
        return None  # over the cross-venue rate limit
    _, decay = _closure_settings()
    cutoff = timezone.now() - timedelta(seconds=decay)
    if place.closure_reports.filter(reporter=reporter, created_at__gte=cutoff).exists():
        return None  # one report per reporter per place per window
    return PlaceClosureReport.objects.create(place=place, reporter=reporter)


@transaction.atomic
def clear_closure_reports(place, *, moderator=None) -> int:
    """Staff reset — delete all closure reports so a wrongly-reported (or reopened) venue re-appears
    in discovery on the next read. Audited when a moderator triggers it."""
    n, _ = place.closure_reports.all().delete()
    if moderator is not None:
        from apps.safety.services import record_audit

        record_audit("place.closure_reports_cleared", actor=moderator, target=place)
    return n


# --- F19: crowd venue facts + kid-suitability facts (ingest-safe overlay) -------------
# Verified members confirm/dispute concrete VENUE facts OSM rarely records. Derived OSM-FIRST at
# read time; crowd votes fill in only where OSM is silent. NEVER a composite "safe for kids" score
# — neutral individual facts only. Counts-only display; never writes Place/raw_tags.


def _fact_quorum() -> int:
    return getattr(settings, "FACT_QUORUM", DEFAULT_FACT_QUORUM)


# OSM-first source per fact: "kv" -> _tristate on raw_tags[key] (yes/no/unknown); "present" ->
# true if the tag equals the value, else UNKNOWN (the ABSENCE of an OSM tag is never a 'no'); None
# -> no OSM source, crowd-only. Top-level OSM keys are safe (enrichment namespaces its sub-keys).
_FACT_OSM = {
    PlaceFactVote.FactKey.DRINKING_WATER: ("kv", "drinking_water"),
    PlaceFactVote.FactKey.TOILETS: ("kv", "toilets"),
    PlaceFactVote.FactKey.LIT_AT_NIGHT: ("kv", "lit"),
    PlaceFactVote.FactKey.PLAYGROUND: ("present", ("leisure", "playground")),
    PlaceFactVote.FactKey.FENCED: ("present", ("barrier", "fence")),
    PlaceFactVote.FactKey.SHADE: ("present", ("natural", "tree")),
    PlaceFactVote.FactKey.INDOOR_SHELTER: (None, None),
    # W2-F22 "getting there". Parking rides the yes/no "kv" path on the venue's OWN sub-tag
    # (OSM convention: parking=yes / bicycle_parking=yes on the feature) — NOT amenity=parking,
    # which is a venue's *primary type* (and isn't ingested for our venues), so it would never
    # fire. _tristate fail-closes any descriptive value (stands/surface/…) to 'unknown', so the
    # crowd overlay still fills the gap. Transit has no venue-level tag -> crowd-only.
    PlaceFactVote.FactKey.BIKE_PARKING: ("kv", "bicycle_parking"),
    PlaceFactVote.FactKey.CAR_PARKING: ("kv", "parking"),
    PlaceFactVote.FactKey.BUS_TRAM_NEARBY: (None, None),
}

# Kid-relevant subset for the SOFT badge (never a composite score; never hides 'unknown').
_KID_FACTS = (
    PlaceFactVote.FactKey.TOILETS,
    PlaceFactVote.FactKey.FENCED,
    PlaceFactVote.FactKey.PLAYGROUND,
    PlaceFactVote.FactKey.DRINKING_WATER,
)
# The same subset as bare key strings, so the web kid-badge filter reuses it instead of
# hand-duplicating the list (single source of truth — F22 added non-kid facts beside it).
KID_FACT_KEYS = frozenset(k.value for k in _KID_FACTS)


def _osm_fact_state(place, fact_key) -> str:
    kind, spec = _FACT_OSM.get(fact_key, (None, None))
    raw = place.raw_tags if isinstance(place.raw_tags, dict) else {}
    if kind == "kv":
        return _tristate(raw.get(spec))
    if kind == "present":
        key, value = spec
        return FACT_TRUE if raw.get(key) == value else FACT_UNKNOWN
    return FACT_UNKNOWN


def _crowd_state(yes: int, no: int) -> str:
    """A quorum on the MAJORITY side decides; a tie or sub-quorum stays unknown."""
    if max(yes, no) < _fact_quorum() or yes == no:
        return FACT_UNKNOWN
    return FACT_TRUE if yes > no else FACT_FALSE


def place_fact_status(place, fact_key) -> str:
    """Tristate for ONE venue fact at read time. OSM is authoritative when present; crowd votes
    fill in only where OSM is silent. Never written back to Place (re-ingest safe)."""
    osm = _osm_fact_state(place, fact_key)
    if osm != FACT_UNKNOWN:
        return osm
    yes = place.fact_votes.filter(fact_key=fact_key, value=True).count()
    no = place.fact_votes.filter(fact_key=fact_key, value=False).count()
    return _crowd_state(yes, no)


def venue_facts(place) -> list:
    """Ordered [{key, label, state}] for every venue fact (OSM-first, crowd overlay). ONE grouped
    query for the crowd tallies — no per-fact N+1."""
    from django.db.models import Count

    tally = {
        (row["fact_key"], row["value"]): row["n"]
        for row in place.fact_votes.values("fact_key", "value").annotate(n=Count("id"))
    }
    out = []
    for key in PlaceFactVote.FactKey:
        osm = _osm_fact_state(place, key)
        if osm != FACT_UNKNOWN:
            state = osm
        else:
            state = _crowd_state(tally.get((key.value, True), 0), tally.get((key.value, False), 0))
        out.append({"key": key.value, "label": key.label, "state": state})
    return out


def venue_facts_detail(place, viewer=None) -> list:
    """venue_facts + per-fact yes/no counts + the viewer's OWN vote, in BOUNDED queries (one
    grouped tally, one viewer-votes lookup) — for the place_detail surface. `osm_sourced` marks a
    fact decided by map data (the crowd vote form is hidden for those — crowd only fills gaps)."""
    from django.db.models import Count

    tally = {
        (row["fact_key"], row["value"]): row["n"]
        for row in place.fact_votes.values("fact_key", "value").annotate(n=Count("id"))
    }
    my = {}
    if viewer is not None and getattr(viewer, "is_authenticated", False):
        my = {row.fact_key: row.value for row in place.fact_votes.filter(user=viewer)}
    out = []
    for key in PlaceFactVote.FactKey:
        osm = _osm_fact_state(place, key)
        yes = tally.get((key.value, True), 0)
        no = tally.get((key.value, False), 0)
        out.append(
            {
                "key": key.value,
                "label": key.label,
                "state": osm if osm != FACT_UNKNOWN else _crowd_state(yes, no),
                "yes": yes,
                "no": no,
                "required": _fact_quorum(),
                "my_vote": my.get(key.value),
                "osm_sourced": osm != FACT_UNKNOWN,
            }
        )
    return out


def has_kid_facts(place) -> bool:
    """SOFT badge signal (F15 rule): True iff at least one KID-relevant fact is confirmed 'true'.
    Never used to HIDE a place (an unknown-fact venue is never excluded) — only to mark one."""
    return any(place_fact_status(place, key) == FACT_TRUE for key in _KID_FACTS)


def fact_vote_summary(place, fact_key, viewer=None) -> dict:
    """Counts + the viewer's OWN vote only (never a voter list) + the derived state for one fact."""
    yes = place.fact_votes.filter(fact_key=fact_key, value=True).count()
    no = place.fact_votes.filter(fact_key=fact_key, value=False).count()
    my_vote = None
    if viewer is not None and getattr(viewer, "is_authenticated", False):
        row = place.fact_votes.filter(user=viewer, fact_key=fact_key).first()
        my_vote = row.value if row else None
    return {
        "yes": yes,
        "no": no,
        "required": _fact_quorum(),
        "state": place_fact_status(place, fact_key),
        "my_vote": my_vote,
    }


@transaction.atomic
def vote_on_fact(user, place, fact_key, value) -> PlaceFactVote:
    """Record (or change) one member's yes/no vote on a venue fact. Gated like the other crowd
    overlays: verified+consented, public place only, a fixed fact_key, rate-limited; idempotent via
    update_or_create (one row per (place,user,fact_key) — a mind-change updates, never stacks)."""
    from apps.accounts.services import can_participate
    from apps.safety.services import allow_action

    if fact_key not in PlaceFactVote.FactKey.values:
        raise PlacesError("Unknown venue fact.")
    if not can_participate(user):
        raise NotEligible("Verified, consented participation is required to vote on a place.")
    if not public_places().filter(pk=place.pk).exists():
        raise PlacesError("This place isn't published yet.")
    if not allow_action(
        user,
        "place_fact_vote",
        limit=getattr(settings, "FACT_VOTE_RATE_LIMIT", 40),
        window_seconds=getattr(settings, "FACT_VOTE_RATE_WINDOW_SECONDS", 3600),
    ):
        raise PlacesError("You're voting too quickly; please try again later.")
    vote, _ = PlaceFactVote.objects.update_or_create(
        place=place, user=user, fact_key=fact_key, defaults={"value": bool(value)}
    )
    return vote


# --- F20: crowd-corrected venue name & address (quorum edit overlay) ------------------
# A member proposes a corrected name/address behind the SAME N-confirmer quorum as a user-proposed
# place (F25). Applied OSM-first at read time via Place.display_name/display_address — never written
# back to Place (re-ingest safe). Counts-only pending UI; proposer excluded from confirming.


def _correction_quorum() -> int:
    return getattr(settings, "CORRECTION_QUORUM", DEFAULT_CORRECTION_QUORUM)


@transaction.atomic
def propose_place_correction(proposer, place, *, field, proposed_value) -> PlaceCorrection:
    """Open a correction proposal for a venue's name/address. Verified+consented, public place only,
    a fixed field, sanitised + capped value; at most one PENDING correction per (place, field)."""
    from apps.accounts.services import can_participate
    from apps.safety.services import record_audit

    if field not in PlaceCorrection.Field.values:
        raise PlacesError("You can only correct the name, address or opening hours.")
    if not can_participate(proposer):
        raise NotEligible("Verified, consented participation is required to suggest a correction.")
    if not public_places().filter(pk=place.pk).exists():
        raise PlacesError("This place isn't published yet.")
    value = (proposed_value or "").strip()[:255]
    if not value:
        raise PlacesError("Enter the corrected value.")
    # W3-F14: a HOURS correction must be machine-parseable so it can feed is_open_at at read time.
    # Validate ON PROPOSE (the 255-char cap can't become a free-text channel) and RE-PARSE on read
    # (see Place.display_opening_hours). The raw OSM string is stored; the dict is derived.
    if field == PlaceCorrection.Field.HOURS:
        from .enrichment.opening_hours import parse_opening_hours

        if parse_opening_hours(value) is None:
            raise PlacesError(
                "Enter valid opening hours, e.g. 'Mo-Fr 09:00-18:00; Sa 10:00-14:00'."
            )
    if place.corrections.filter(field=field, status=PlaceCorrection.Status.PENDING).exists():
        raise PlacesError("There's already an open correction for this field.")
    correction = PlaceCorrection.objects.create(
        place=place,
        proposer=proposer,
        field=field,
        proposed_value=value,
        required_confirmations=_correction_quorum(),
    )
    record_audit("place.correction_proposed", actor=proposer, target=place, field=field)
    return correction


def _publish_correction(correction: PlaceCorrection) -> None:
    """Mark a correction PUBLISHED (applied at read time via Place.display_*). W3-F14: when a HOURS
    correction publishes, clear the F28 open-now reports — they were about the OLD, now-superseded
    hours, so leaving them would make the freshly-corrected venue read 'unverified' at the same
    time. NAME/ADDRESS corrections don't touch open_now_status, so they skip the clear."""
    correction.status = PlaceCorrection.Status.PUBLISHED
    correction.published_at = timezone.now()
    correction.save(update_fields=["status", "published_at"])
    if correction.field == PlaceCorrection.Field.HOURS:
        clear_open_now_reports(correction.place)


@transaction.atomic
def confirm_place_correction(user, correction: PlaceCorrection) -> PlaceCorrection:
    """An independent member confirms a correction; a quorum publishes it (applied at read time)."""
    from apps.accounts.services import can_participate

    if correction.status != PlaceCorrection.Status.PENDING:
        raise InvalidState("This correction is no longer open for confirmation.")
    if correction.proposer_id == user.id:
        raise InvalidState("The proposer cannot confirm their own correction.")
    if not can_participate(user):
        raise NotEligible("Verified, consented participation is required to confirm a correction.")
    PlaceCorrectionConfirmation.objects.get_or_create(correction=correction, user=user)
    if correction.confirmations.count() >= correction.required_confirmations:
        _publish_correction(correction)
    return correction


@transaction.atomic
def staff_publish_correction(staff_user, correction: PlaceCorrection) -> PlaceCorrection:
    """Moderator fast-publish (single-launch-city escape hatch when a quorum won't form)."""
    if not staff_user.is_staff:
        raise NotEligible("Only staff may publish a correction.")
    if correction.status != PlaceCorrection.Status.PENDING:
        raise InvalidState("This correction is not pending.")
    _publish_correction(correction)
    from apps.safety.services import record_audit

    record_audit("place.correction_published", actor=staff_user, target=correction.place)
    return correction


@transaction.atomic
def staff_reject_correction(
    staff_user, correction: PlaceCorrection, *, reason=""
) -> PlaceCorrection:
    """Moderator close-out of a bad correction. Works on a PENDING one (never applied) OR a
    PUBLISHED one (a deliberate REVERT — display falls back to OSM / an earlier correction); this
    revert capability is why it intentionally differs from F25's PENDING-only staff_reject_proposal
    (a published place can't be 'unpublished' the same way). A re-reject of an already-REJECTED
    correction is a no-op error (validation the review asked for)."""
    if not staff_user.is_staff:
        raise NotEligible("Only staff may reject a correction.")
    if correction.status == PlaceCorrection.Status.REJECTED:
        raise InvalidState("This correction is already rejected.")
    correction.status = PlaceCorrection.Status.REJECTED
    correction.published_at = None  # a reverted correction is no longer applied at read time
    correction.save(update_fields=["status", "published_at"])
    from apps.safety.services import record_audit

    record_audit(
        "place.correction_rejected", actor=staff_user, target=correction.place, reason=reason
    )
    return correction


# W4-F18: stable keys for the self-only venue data-quality digest. The digest template maps each to
# a human, translatable label (places/services has no gettext, and keys keep the helper testable).
VENUE_FLAG_CLOSED = "closed"
VENUE_FLAG_HOURS_UNVERIFIED = "hours_unverified"
VENUE_FLAG_CORRECTION_PENDING = "correction_pending"


def venue_quality_flags(place) -> list[str]:
    """W4-F18: the read-time data-quality concerns for ONE venue, composed from the EXISTING
    ingest-safe overlays — crowd-reported-closed (F13), unverified posted hours (F28), and a pending
    crowd correction (W3-F14). Counts-only / identity-free (inherits each source's allowlist); adds
    no model and persists nothing. Returns stable keys; an empty list means "no known problems"."""
    flags = []
    if place_is_closed(place):
        flags.append(VENUE_FLAG_CLOSED)
    elif open_now_status(place) == "unverified":
        # Only when not already flagged closed — "closed" is the stronger, more actionable signal.
        flags.append(VENUE_FLAG_HOURS_UNVERIFIED)
    if pending_corrections(place):
        flags.append(VENUE_FLAG_CORRECTION_PENDING)
    return flags


def pending_corrections(place, viewer=None) -> list:
    """Open corrections for a place as COUNTS only (never proposer/confirmer identities), plus
    whether the viewer has already confirmed each (so the UI can disable their button)."""
    out = []
    for c in place.corrections.filter(status=PlaceCorrection.Status.PENDING).order_by("field"):
        confirmed_by_me = False
        if viewer is not None and getattr(viewer, "is_authenticated", False):
            confirmed_by_me = c.confirmations.filter(user=viewer).exists()
        out.append(
            {
                "id": c.id,
                "field": c.field,
                "field_label": PlaceCorrection.Field(c.field).label,
                "proposed_value": c.proposed_value,
                "confirms": c.confirmations.count(),
                "required": c.required_confirmations,
                "is_proposer": viewer is not None and c.proposer_id == getattr(viewer, "id", None),
                "confirmed_by_me": confirmed_by_me,
            }
        )
    return out
