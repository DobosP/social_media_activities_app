"""Domain logic for the social core: cohort-gated activities, join-by-vote, and the
user-place quorum. Views and admin go through these functions so the safety
invariants (cohort isolation, verified-and-consented participation) live in one place.
"""

import calendar
import logging
import re
from datetime import timedelta

from django.conf import settings
from django.db import transaction
from django.db.models import Count, Q
from django.utils import timezone
from django.utils.translation import gettext_lazy as _

from apps.accounts.models import Cohort, GuardianRelationship
from apps.accounts.services import can_participate, effective_guardrail

from .models import (
    DEFAULT_JOIN_THRESHOLD,
    DEFAULT_PLACE_QUORUM,
    Activity,
    ActivityInterest,
    ActivitySeries,
    Group,
    GroupMembership,
    GroupQuestionPrompt,
    JoinVote,
    Membership,
    PlaceConfirmation,
    Post,
    Thread,
    UserPlaceProposal,
)

logger = logging.getLogger(__name__)

# F3: self-declared arrival ping is only accepted around the start time, and is cleared a
# few hours after start so it never becomes a standing presence record. Overridable via
# settings; sane defaults here.
ARRIVAL_WINDOW_BEFORE_HOURS = 2
ARRIVAL_WINDOW_AFTER_HOURS = 3

# W3-F3: the "heading home" departure window is END-relative — a departure happens near the
# meetup's end, so reusing the start-relative arrival window would leave the button dead exactly
# when a departing child taps it. It opens at the meetup start and closes this many hours after
# it ends (DEPARTURE_FALLBACK_DURATION_HOURS stands in for the meetup length when ends_at is
# open-ended). Overridable via settings.
DEPARTURE_WINDOW_AFTER_HOURS = 3
DEPARTURE_FALLBACK_DURATION_HOURS = 3

# F35 "catch up" digest — deterministic, bounded, no ML. Caps keep the read cheap.
DIGEST_SCAN_LIMIT = 60  # hard cap on non-announcement posts pulled into Python
DIGEST_RECENT_POSTS = 3  # most-recent posts always surfaced
DIGEST_LOGISTICAL_POSTS = 3  # max keyword-matched logistical posts surfaced
DIGEST_MAX_ANNOUNCEMENTS = 2  # latest N announcements
# Conservative, whole-word vocabulary for "this post is about logistics". Deliberately omits
# bare "time" (so "had a great time" never matches); a real time change still trips on
# change/changed/reschedule/moved/postpone. The vocabulary lives only here.
_LOGISTICAL_RE = re.compile(
    r"\b(meet|meeting|change|changed|move|moved|moving|bring|bringing|cancel|"
    r"cancell?ed|cancelling|reschedul\w*|postpon\w*|location|venue)\b",
    re.IGNORECASE,
)


class SocialError(Exception):
    """Base for expected, user-facing social-domain errors."""


class NotEligible(SocialError):
    """User fails the participation/cohort gate for this action."""


class NotAMember(SocialError):
    """Action requires current membership the user doesn't have."""


class InvalidState(SocialError):
    """Target object is not in a state that permits this action."""


class DuplicatePlace(SocialError):
    """A proposed venue duplicates an existing place (F25). Carries the existing place id/name
    so the UI can link to it; ``soft`` marks a near-but-different venue the user may override."""

    def __init__(self, place_id, place_name, *, soft=False):
        self.place_id = place_id
        self.place_name = place_name
        self.soft = soft
        super().__init__(f"A place already exists nearby: {place_name}")


# F25: a stricter same-surface 'don't re-add an existing venue' radius, deliberately separate
# from the 75 m cross-source ingest dedup. Overridable via settings.
PLACE_PROPOSAL_DEDUP_RADIUS_M = 60
PLACE_PROPOSAL_SOFT_RADIUS_M = 25


def _has_cohort(user) -> bool:
    return user.cohort != Cohort.UNASSIGNED


def can_create_activity(user) -> bool:
    return can_participate(user) and _has_cohort(user)


def visible_activities(user):
    """Activities a user may see — those in their own cohort (isolation), excluding
    any owned by a user they've blocked or been blocked by (D4)."""
    if not _has_cohort(user):
        return Activity.objects.none()
    from apps.safety.services import blocked_user_ids

    qs = Activity.objects.filter(cohort=user.cohort, is_hidden=False)
    blocked = blocked_user_ids(user)
    if blocked:
        qs = qs.exclude(owner_id__in=blocked)
    return qs


def can_see_activity(user, activity) -> bool:
    return _has_cohort(user) and user.cohort == activity.cohort


# Search queries shorter than this return nothing (too noisy, and a 1-char probe is a
# cheap enumeration surface). Shared by every search entry point (web + DRF).
SEARCH_MIN_QUERY_LEN = 2
SEARCH_MAX_RESULTS = 100


def _matching_type_ids(query):
    """W2-F1: resolve a free-text query to the ActivityType ids it should match, reading the
    RO/EN ``aliases`` + the slug (NOT just the display name — already-seeded vocabulary like
    'alergare'/'streetball' otherwise returns nothing), then a depth-1 SYNONYM/VARIANT walk so a
    search for one term also finds its synonyms/variants. The taxonomy is tiny (~100 rows), so the
    alias scan is a single cheap query in Python — no JSONField lookup quirks, no ranking."""
    from apps.taxonomy.models import ActivityRelation, ActivityType

    q = (query or "").strip().lower()
    if not q:
        return set()
    matched = {
        t.id
        for t in ActivityType.objects.filter(is_active=True).only("id", "name", "slug", "aliases")
        if q in t.name.lower()
        or q in t.slug.lower()
        or any(isinstance(a, str) and q in a.lower() for a in (t.aliases or []))
    }
    if not matched:
        return matched
    expanded = set(matched)
    rels = ActivityRelation.objects.filter(
        Q(source_id__in=matched) | Q(target_id__in=matched),
        kind__in=[ActivityRelation.Kind.SYNONYM, ActivityRelation.Kind.VARIANT],
    ).only("source_id", "target_id", "symmetric")
    for r in rels:
        if r.source_id in matched:
            expanded.add(r.target_id)
        if r.target_id in matched and r.symmetric:
            expanded.add(r.source_id)
    return expanded


def activity_search_filter(qs, query):
    """Apply the free-text activity search predicate to an Activity queryset.

    Matches title, description and the venue name (plain trigram-indexed icontains), plus the
    activity TYPE resolved through its slug + RO/EN aliases + a depth-1 synonym/variant walk
    (W2-F1) — so seeded vocabulary actually matches. Honest substring match, no relevance ranking,
    so ordering stays soonest-first (never popularity)."""
    predicate = (
        Q(title__icontains=query)
        | Q(description__icontains=query)
        | Q(place__name__icontains=query)
        | Q(activity_type__name__icontains=query)
    )
    type_ids = _matching_type_ids(query)
    if type_ids:
        predicate |= Q(activity_type_id__in=type_ids)
    return qs.filter(predicate)


def search_did_you_mean(viewer, query, *, threshold=0.3):
    """W2-F1: when a search finds nothing, suggest the closest activity-type NAME by trigram
    similarity (the taxonomy is small, so no index is needed). Honest + never auto-applied: only
    a name the viewer could actually act on (an active type with an upcoming, visible activity) is
    suggested, so 'did you mean X?' never leads to a dead end. Returns the name or None."""
    from django.contrib.postgres.search import TrigramSimilarity

    from apps.taxonomy.models import ActivityType

    query = (query or "").strip()
    if len(query) < SEARCH_MIN_QUERY_LEN:
        return None
    best = (
        ActivityType.objects.filter(is_active=True)
        .annotate(sim=TrigramSimilarity("name", query))
        .filter(sim__gt=threshold)
        .order_by("-sim", "name")
        .first()
    )
    if best is None:
        return None
    leads_somewhere = (
        visible_activities(viewer)
        .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now(), activity_type=best)
        .exists()
    )
    return best.name if leads_somewhere else None


def search_activities(viewer, query, *, beginners=False, limit=SEARCH_MAX_RESULTS):
    """Free-text search over the activities the viewer may see (W1).

    Routed through ``visible_activities`` so cohort isolation and blocking hold
    identically to every other discovery surface. Only OPEN, future activities are
    returned (a search is a way to find something to join, not an archive browse).
    Venue names are safe to match: ``create_activity`` only accepts ``public_places()``
    venues, so no pending user-proposed place name can leak through an activity.
    Bounded and soonest-first."""
    query = (query or "").strip()
    if len(query) < SEARCH_MIN_QUERY_LEN:
        return Activity.objects.none()
    qs = visible_activities(viewer).filter(
        status=Activity.Status.OPEN, starts_at__gte=timezone.now()
    )
    if beginners:
        qs = qs.filter(beginners_welcome=True)
    return (
        activity_search_filter(qs, query)
        .select_related("place", "activity_type", "owner")
        .order_by("starts_at", "id")[: max(1, min(int(limit), SEARCH_MAX_RESULTS))]
    )


def search_thread_posts(viewer, activity, query, *, limit=50):
    """Search inside one thread's plaintext posts (W1 "search into chat").

    Fail-closed: re-gates on ``can_read_thread`` even though callers should have
    gated already — a search must never see further than the thread itself. Hidden
    posts stay hidden. Returns newest-first, bounded. (E2EE direct messages are
    structurally unsearchable server-side — the server never has plaintext.)"""
    query = (query or "").strip()
    if len(query) < SEARCH_MIN_QUERY_LEN:
        return Post.objects.none()
    if not can_read_thread(viewer, activity):
        raise NotEligible("You can't search this thread.")
    return (
        activity.thread.posts.filter(is_hidden=False, body__icontains=query)
        .select_related("author", "reply_to__author")
        .order_by("-created_at", "-id")[:limit]
    )


def with_counts(qs):
    """Annotate an Activity queryset with ``member_n`` (current members) and
    ``participant_n`` (members holding a position — excludes supervisory guardians) so
    list serialization needs no per-row COUNT. The serializer reads these annotations
    when present, eliminating the N+1 on the activities feed / recommendations."""
    member = Q(memberships__state=Membership.State.MEMBER)
    return qs.annotate(
        member_n=Count("memberships", filter=member, distinct=True),
        participant_n=Count(
            "memberships",
            filter=member & ~Q(memberships__role=Membership.Role.GUARDIAN),
            distinct=True,
        ),
    )


def current_members(activity):
    return activity.memberships.filter(state=Membership.State.MEMBER)


def voting_members(activity):
    """Members who vote on join requests — peers only; guardians are supervisory and
    do not vote."""
    return current_members(activity).exclude(role=Membership.Role.GUARDIAN)


def is_organizer(user, activity) -> bool:
    """F22: True if `user` may act as an organiser of `activity` — the owner, OR a current member
    granted the CO_ORGANIZER role. Fail-closed (anonymous / non-member → False). This is the single
    gate the operational owner-actions (cancel/edit/admit/announce) route through, so a co-organiser
    inherits exactly those powers and nothing more (granting/revoking/transfer stay owner-only)."""
    if not getattr(user, "id", None):
        return False
    if activity.owner_id == user.id:
        return True
    return activity.memberships.filter(
        user=user,
        state=Membership.State.MEMBER,
        role=Membership.Role.CO_ORGANIZER,
    ).exists()


# W2-F5: how soon a blank meeting point becomes "needs attention" on the organizer console.
ORGANIZER_PREP_WINDOW = timedelta(hours=48)


def organizer_console(user) -> dict:
    """W2-F5: a self-scoped read-only digest of everything ``user`` runs, each item tagged with the
    concrete action it needs NOW. Composes the existing chokepoints — no new write/auth surface:

    * ``activities``: OPEN, upcoming activities the user OWNS or co-organises (is_organizer), each
      annotated with ``pending_joins`` (REQUESTED count), ``needs_supervisor`` (F29 — supervised
      but no live supervisor seated), ``missing_meeting_point`` (starts within 48h, blank), plus
      (W3-F5) a ``readiness`` sub-dict (missing what-to-bring / getting-home (CHILD only) /
      near-capacity), a ``quorum`` sub-dict (the calm "needs N more to go" line) and a
      ``venue_flag`` (the place has a live wrong-hours data-quality flag — check before you go);
    * ``series``: the user's own recurring templates; ``groups``: the user's own standing groups.

    Deterministic (soonest-first), bounded, and STRICTLY FUNCTIONAL — it surfaces work to do and
    links into the existing edit/admit/announce screens; it performs nothing and exposes NO
    per-organizer vanity counter (a pending-join count is a task, not a score)."""
    if not getattr(user, "is_authenticated", False):
        return {"activities": [], "series": [], "groups": []}
    # F5: the venue-health flag reuses F28's decay window (same source the PlaceViewSet uses), so a
    # stale wrong-hours report stops counting and the flag self-heals.
    from apps.places.services import _open_now_settings, hours_reliable

    now = timezone.now()
    _, _report_decay = _open_now_settings()
    report_cutoff = now - timedelta(seconds=_report_decay)
    # Owner OR co-organiser, resolved to ids first so the pending-join annotation can't be
    # multiplied by the co-organiser membership join.
    ids = set(Activity.objects.filter(owner=user).values_list("id", flat=True))
    ids |= set(
        Activity.objects.filter(
            memberships__user=user,
            memberships__role=Membership.Role.CO_ORGANIZER,
            memberships__state=Membership.State.MEMBER,
        ).values_list("id", flat=True)
    )
    activities = (
        Activity.objects.filter(id__in=ids, status=Activity.Status.OPEN, starts_at__gte=now)
        .select_related("place", "activity_type")
        # F20: the template renders place.display_name, which reads place.corrections — prefetch
        # so the list stays O(1) queries (the established pattern on every display_name surface).
        .prefetch_related("place__corrections")
        # F5: batch every per-row read onto the single console queryset so the up-to-100-row list
        # stays O(1) queries — never an attendance_summary()/participant_count()/hours_reliable()
        # call inside the comprehension below. distinct=True is load-bearing: the counts span two
        # multi-valued relations (memberships and place__open_now_reports), and without it the
        # join fan-out would multiply each tally.
        .annotate(
            pending_n=Count(
                "memberships",
                filter=Q(memberships__state=Membership.State.REQUESTED),
                distinct=True,
            ),
            # voting_members (state=MEMBER, never a supervisory guardian) — the quorum "total".
            member_n=Count(
                "memberships",
                filter=Q(memberships__state=Membership.State.MEMBER)
                & ~Q(memberships__role=Membership.Role.GUARDIAN),
                distinct=True,
            ),
            # of those, the ones who've said they're GOING — the quorum "going".
            going_n=Count(
                "memberships",
                filter=Q(memberships__state=Membership.State.MEMBER)
                & ~Q(memberships__role=Membership.Role.GUARDIAN)
                & Q(memberships__attendance_intent=Membership.AttendanceIntent.GOING),
                distinct=True,
            ),
            # F28 recent wrong-hours reports for THIS activity's place — re-derived through the
            # reverse FK (place__open_now_reports), NOT copied from the PlaceViewSet's direct
            # annotation. Fed onto place.recent_report_n below so hours_reliable() reads it.
            place_report_n=Count(
                "place__open_now_reports",
                filter=Q(place__open_now_reports__created_at__gte=report_cutoff),
                distinct=True,
            ),
        )
        .order_by("starts_at", "id")[:100]
    )
    prep_cutoff = now + ORGANIZER_PREP_WINDOW
    rows = []
    for a in activities:
        place = a.place
        # Feed the batched count onto the place so hours_reliable() reads the annotation instead
        # of firing a per-row open_now_reports query.
        place.recent_report_n = a.place_report_n
        live = a.min_to_go is not None  # the queryset is already filtered to status=OPEN
        rows.append(
            {
                "activity": a,
                "pending_joins": a.pending_n,
                "needs_supervisor": a.supervised and not supervision_satisfied(a),
                "missing_meeting_point": a.starts_at <= prep_cutoff
                and not (a.meeting_point or "").strip(),
                # F5 night-before readiness — already-fetched fields only, no query. Each is a TASK
                # snapshot (a gap to fix), never a per-organizer score.
                "readiness": {
                    "missing_what_to_bring": not (a.what_to_bring or "").strip(),
                    # getting_home is a CHILD-only logistics field; surface its gap only there.
                    "missing_getting_home": a.cohort == Cohort.CHILD
                    and not (a.getting_home_note or "").strip(),
                    "near_capacity": a.capacity is not None and a.member_n >= a.capacity,
                },
                # F5 calm "needs N more to go" quorum line — same shape as attendance_summary,
                # computed from the batched counts (never a per-row attendance_summary() call).
                "quorum": {
                    "going": a.going_n,
                    "total": a.member_n,
                    "min_to_go": a.min_to_go if live else None,
                    "met_minimum": (a.going_n >= a.min_to_go) if live else None,
                    "remaining_needed": max(a.min_to_go - a.going_n, 0) if live else None,
                },
                # F5 "check this venue" task when the place has a live data-quality flag.
                "venue_flag": not hours_reliable(place),
            }
        )
    # Only series still in play — an ENDED series can never run again and needs nothing, so it
    # has no place on a "what each one needs next" console (PAUSED stays: it's resumable).
    series = list(
        visible_series(user)
        .filter(owner=user)
        .exclude(status=ActivitySeries.Status.ENDED)
        .order_by("next_starts_at", "id")[:100]
    )
    groups = list(visible_groups(user).filter(owner=user).order_by("title", "id")[:100])
    return {"activities": rows, "series": series, "groups": groups}


def thread_members(owner_obj):
    """Current MEMBER-state memberships of a thread owner — an Activity OR a Group. The single
    dispatcher the hardened thread gates (post_to_thread / can_read_thread / toggle_reaction /
    edit_post) use so they hold IDENTICALLY on both surfaces. Fail-closed: an unknown owner type
    raises (never silently treated as an Activity — that default would be a cross-cohort leak)."""
    if isinstance(owner_obj, Activity):
        return owner_obj.memberships.filter(state=Membership.State.MEMBER)
    if isinstance(owner_obj, Group):
        return owner_obj.memberships.filter(state=GroupMembership.State.MEMBER)
    raise TypeError(f"Unknown thread owner type: {type(owner_obj)!r}")


def is_thread_frozen(owner_obj) -> bool:
    """Whether a thread is frozen to new writes: a CANCELLED Activity or a non-ACTIVE (ARCHIVED)
    Group. The freeze gate, generalised. Fail-closed on an unknown owner type."""
    if isinstance(owner_obj, Activity):
        return owner_obj.status == Activity.Status.CANCELLED
    if isinstance(owner_obj, Group):
        return owner_obj.status != Group.Status.ACTIVE
    raise TypeError(f"Unknown thread owner type: {type(owner_obj)!r}")


def thread_audience_summary(viewer, owner_obj) -> dict:
    """W2-F34: a calm, honest "who can see this" summary for the thread composer — converts the
    invisible scope gates into FELT assurance without adding any capability. Pure read: stores
    nothing, changes no gate, opens no path; it only re-describes gates that already hold.

    The thread audience is EXACTLY the same-cohort current members: can_read_thread walls out every
    OTHER cohort (``user.cohort != activity.cohort -> False``). A supervisory guardian is always a
    cross-cohort adult on a CHILD activity, so they genuinely CANNOT read the thread and are
    deliberately NOT named here (that would be a false "an adult is reading" claim — the exact
    overclaim this feature avoids; the page's "supervised" chip carries the supervision fact).

    Returns:
      - ``is_group``: phrasing flag (a Group thread vs an Activity thread).
      - ``peer_count``: the number of OTHER current peer members — an ADULT-viewer-only surface
        (None for CHILD/TEEN/anon), reusing the platform-wide count-suppression rule so a minor
        never sees a roster size (they get a generic phrase in the template instead).
    """
    is_group = isinstance(owner_obj, Group)
    peer_count = None
    # The count shows ONLY to an adult viewer of an adult thread — the only audience that can both
    # see a count (count-suppression: minors never do) AND legitimately read this thread (the
    # same-cohort rule). Requiring the THREAD be adult too makes the helper self-protecting: a
    # cross-cohort mis-call (e.g. an adult guardian + a CHILD thread) returns None, never a count.
    if (
        getattr(viewer, "cohort", None) == Cohort.ADULT
        and getattr(owner_obj, "cohort", None) == Cohort.ADULT
    ):
        members = thread_members(owner_obj)
        # Guardians are cross-cohort and can't read the thread, so they're never peers anyway.
        peers = members if is_group else members.exclude(role=Membership.Role.GUARDIAN)
        peer_count = peers.exclude(user_id=viewer.id).count()
    return {"is_group": is_group, "peer_count": peer_count}


def participant_count(activity) -> int:
    """Number of participants holding a position — members/owner, excluding guardians."""
    return voting_members(activity).count()


def open_positions(activity) -> int | None:
    """Remaining open spots, or None when the activity is uncapped."""
    if activity.capacity is None:
        return None
    return max(activity.capacity - participant_count(activity), 0)


def can_join(user, activity) -> bool:
    if not can_participate(user):
        return False
    if user.cohort != activity.cohort:
        return False
    if activity.status != Activity.Status.OPEN:
        return False
    if activity.capacity is not None and participant_count(activity) >= activity.capacity:
        return False  # no open positions left
    existing = (
        activity.memberships.filter(user=user).exclude(state=Membership.State.REMOVED).exists()
    )
    if existing:
        return False
    # F9: a CHILD-cohort meetup must be at an approved public venue type (defence in depth — the
    # same gate runs at create; this also covers a place that lost its classification since).
    if (
        activity.cohort == Cohort.CHILD
        and getattr(settings, "CHILD_PUBLIC_VENUES_ONLY", True)
        and not _venue_ok_for_child(activity)
    ):
        return False
    # F7: a CHILD ward's guardian(s) may have set conservative participation guardrails. These
    # only ever NARROW access; we apply the STRICTEST across all active guardians, fail-closed.
    if user.cohort == Cohort.CHILD and not _passes_guardrails(user, activity):
        return False
    return True


def _venue_ok_for_child(activity) -> bool:
    """True iff the activity's meetup place is an approved public venue type for children (F9)."""
    from apps.places.services import is_child_safe_venue

    return is_child_safe_venue(activity.place)


def _passes_guardrails(user, activity) -> bool:
    """True iff the activity satisfies the strictest active guardian guardrail on a CHILD ward
    (F7). No guardrail -> always True. Each clause is a hard NARROW: supervised_only requires a
    guardian-accompanied meetup; latest_start_hour caps the meetup's *local* start hour; and
    max_open_joins caps how many OPEN meetups (non-removed memberships) the ward is already in.
    Called only for CHILD users (see can_join)."""
    rail = effective_guardrail(user)
    if rail is None:
        return True
    if rail["supervised_only"] and not activity.guardian_accompanied:
        return False
    local = timezone.localtime(activity.starts_at)
    latest = rail["latest_start_hour"]
    if latest is not None and local.hour > latest:
        return False
    # W3-F1 family-calendar window. allowed_weekdays is None when unrestricted, else a frozenset
    # of ISO days (an empty set — a conflicting intersection — blocks every day, fail-closed).
    allowed_weekdays = rail.get("allowed_weekdays")
    if allowed_weekdays is not None and local.isoweekday() not in allowed_weekdays:
        return False
    earliest = rail.get("earliest_start_hour")
    if earliest is not None and local.hour < earliest:
        return False
    # W3-F2 category envelope — the SAME decision fn the create chokepoints use (no drift), fed the
    # rail we already loaded above (no extra query on the join path).
    if not _type_in_category_envelope(rail.get("allowed_categories"), activity.activity_type):
        return False
    cap = rail["max_open_joins"]
    if cap is not None:
        # Count the ward's current commitments to OPEN meetups (REQUESTED or MEMBER, not REMOVED).
        # The activity being joined isn't counted (can_join's `existing` check excludes members),
        # so block when joining would exceed the cap.
        current = (
            Membership.objects.filter(user=user, activity__status=Activity.Status.OPEN)
            .exclude(state=Membership.State.REMOVED)
            .count()
        )
        if current >= cap:
            return False
    return True


def _type_in_category_envelope(allowed, activity_type) -> bool:
    """W3-F2 — the SINGLE place the category-envelope decision is made, so every child chokepoint
    agrees. ``allowed`` is ``effective_guardrail(...)["allowed_categories"]``: None (no guardian set
    a category restriction) -> True; otherwise a frozenset of allowed category slugs and the
    activity's type passes iff its category-ancestry intersects it. An EMPTY frozenset (conflicting
    guardian allowlists) -> False (fail-closed). The ancestry walk is the shared taxonomy helper, so
    the safety gate and the recommendations embedding can't drift."""
    if allowed is None:
        return True
    from apps.taxonomy.services import category_ancestry_slugs

    return any(slug in allowed for slug in category_ancestry_slugs(activity_type))


def category_envelope_allows(user, activity_type) -> bool:
    """Whether a user may join/organize/propose this ``activity_type`` under their guardians'
    W3-F2 category envelope. Non-CHILD and no-active-guardrail both pass with NO query. Wraps
    ``_type_in_category_envelope`` for the create_activity / create_series / propose_interest
    chokepoints, where no Activity exists yet (the join path uses the core fn directly with its
    already-loaded rail). Enforcing at ALL FOUR chokepoints is load-bearing: a CHILD organizer is
    auto-seated MEMBER inside create_activity WITHOUT passing the join gate, so a join-only check
    would let a child escape the envelope by organizing the disallowed category themselves."""
    if user.cohort != Cohort.CHILD:
        return True
    rail = effective_guardrail(user)
    if rail is None:
        return True
    return _type_in_category_envelope(rail.get("allowed_categories"), activity_type)


@transaction.atomic
def create_activity(
    owner,
    *,
    place,
    activity_type,
    title,
    starts_at,
    ends_at=None,
    description="",
    join_threshold=None,
    capacity=None,
    min_to_go=None,
    guardian_accompanied=False,
    supervised=False,
    meeting_point="",
    what_to_bring="",
    organizer_note="",
    getting_home_note="",
    first_time_note="",
    fallback_meeting_point="",
    cost_band=Activity.CostBand.UNSPECIFIED,
    difficulty=Activity.Difficulty.UNSPECIFIED,
    accessibility_notes="",
    beginners_welcome=False,
    fallback_starts_at=None,
):
    if not can_create_activity(owner):
        raise NotEligible(
            _("User cannot create activities (needs verification/consent + a cohort).")
        )
    # F29: a supervised activity REQUIRES a guardian seat, so it must also be guardian-accompanied
    # (else the owner could never seat the supervisor — a deadlock). supervised implies it.
    if supervised:
        if owner.cohort != Cohort.CHILD:
            raise InvalidState(_("Only children's activities can require a supervising guardian."))
        guardian_accompanied = True
    if guardian_accompanied and owner.cohort != Cohort.CHILD:
        raise InvalidState(_("Only children's activities can be guardian-accompanied."))
    if min_to_go is not None and capacity is not None and min_to_go > capacity:
        # An un-confirmable meetup (the minimum can never be reached within the cap) is nonsensical.
        raise InvalidState(_("Minimum to happen can't be more than the capacity."))
    if ends_at is not None and starts_at is not None and ends_at < starts_at:
        # Centralised here so every caller (web/DRF create, series spawn, F27 gauge convert)
        # inherits it — the web forms also check, but the DRF/convert serializers did not.
        raise InvalidState(_("End time cannot be before the start time."))
    # W2-F10: a plan-B time only makes sense as a LATER backup. Centralised so web AND DRF agree
    # (the web form also checks); the invoke-time strictly-future check is the separate safety gate.
    if fallback_starts_at is not None and fallback_starts_at <= starts_at:
        raise InvalidState(_("The plan-B time must be after the planned start."))
    # F25 gate: an activity may only be organised at a PUBLICLY-visible place — never at a
    # still-pending/rejected user-proposed venue. public_places() is the single visibility
    # chokepoint, so this holds identically on the web form and the DRF surface.
    from apps.places.services import public_places

    if place is None or not public_places().filter(pk=place.pk).exists():
        raise InvalidState(_("That place isn't available to organise an activity at yet."))
    # F9: a CHILD-cohort meetup may only be set at a known public venue type (or a staff-approved
    # place). Fail-closed, but the message names the staff-approval path rather than silently
    # over-blocking. Gated behind CHILD_PUBLIC_VENUES_ONLY (default ON).
    if owner.cohort == Cohort.CHILD and getattr(settings, "CHILD_PUBLIC_VENUES_ONLY", True):
        from apps.places.services import is_child_safe_venue

        if not is_child_safe_venue(place):
            raise InvalidState(
                _(
                    "This venue isn't on the approved list for children's activities yet. Pick a "
                    "library, park, school, sports or community venue — or ask a moderator to "
                    "approve this place."
                )
            )
    # W3-F2: a CHILD organizer is auto-seated MEMBER below WITHOUT passing can_join, so the
    # guardian category envelope MUST be enforced here too — otherwise a child escapes the
    # envelope by organizing the disallowed category themselves. Same shared gate as the join path.
    if not category_envelope_allows(owner, activity_type):
        raise InvalidState(_("Your guardian's settings don't allow this kind of activity yet."))
    activity = Activity.objects.create(
        owner=owner,
        place=place,
        activity_type=activity_type,
        title=title,
        description=description,
        starts_at=starts_at,
        ends_at=ends_at,
        cohort=owner.cohort,
        join_threshold=DEFAULT_JOIN_THRESHOLD if join_threshold is None else join_threshold,
        capacity=capacity,
        min_to_go=min_to_go,
        guardian_accompanied=guardian_accompanied,
        supervised=supervised,
        meeting_point=meeting_point,
        what_to_bring=what_to_bring,
        organizer_note=organizer_note,
        getting_home_note=getting_home_note,
        first_time_note=first_time_note,
        fallback_meeting_point=fallback_meeting_point,
        cost_band=cost_band,
        difficulty=difficulty,
        accessibility_notes=accessibility_notes,
        beginners_welcome=beginners_welcome,
        fallback_starts_at=fallback_starts_at,
    )
    Membership.objects.create(
        activity=activity,
        user=owner,
        role=Membership.Role.OWNER,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )
    Thread.objects.create(activity=activity)
    return activity


# --- F4: recurring activity series ---------------------------------------------------
# An organiser defines a repeating meetup once; spawn_due_series materialises ONLY the next
# single Activity through create_activity (so every cohort/consent/blocking gate re-runs per
# instance). A series is never a roster or an attendance rollup — each instance needs a fresh
# per-member join. place/activity_type/cohort are immutable and re-asserted at spawn.


def _add_month(dt, anchor_day=None):
    """One calendar month later, clamping the day to the target month's length (e.g. Jan 31 ->
    Feb 28). ``anchor_day`` (the series' intended day-of-month) is clamped fresh each month, so a
    "last day" series recovers the full day in longer months (Feb 28 -> Mar 31) instead of decaying
    to the 28th forever. Operates on the value as-is — the caller handles timezone."""
    target_day = anchor_day or dt.day
    month = dt.month + 1
    year = dt.year
    if month > 12:
        month = 1
        year += 1
    day = min(target_day, calendar.monthrange(year, month)[1])
    return dt.replace(year=year, month=month, day=day)


def _advance_slot(starts_at, cadence, anchor_day=None):
    """The next occurrence after starts_at for the given cadence (next-instance-only; no rrule).
    The arithmetic is done on the LOCAL wall-clock and re-localised, so a meetup keeps its local
    start time across a DST transition (stays 18:00, not 17:00/19:00 in the launch city)."""
    tz = timezone.get_current_timezone()
    naive = timezone.localtime(starts_at, tz).replace(tzinfo=None)
    if cadence == ActivitySeries.Cadence.WEEKLY:
        naive = naive + timedelta(weeks=1)
    elif cadence == ActivitySeries.Cadence.BIWEEKLY:
        naive = naive + timedelta(weeks=2)
    elif cadence == ActivitySeries.Cadence.MONTHLY:
        naive = _add_month(naive, anchor_day)
    else:
        raise InvalidState(_("Invalid cadence."))
    return timezone.make_aware(naive, tz)


@transaction.atomic
def create_series(
    owner,
    *,
    place,
    activity_type,
    title,
    cadence,
    first_starts_at,
    ends_at=None,
    description="",
    join_threshold=None,
    capacity=None,
    min_to_go=None,
    guardian_accompanied=False,
    supervised=False,
    meeting_point="",
    what_to_bring="",
    organizer_note="",
    getting_home_note="",
    cost_band=Activity.CostBand.UNSPECIFIED,
    difficulty=Activity.Difficulty.UNSPECIFIED,
    accessibility_notes="",
    beginners_welcome=False,
) -> ActivitySeries:
    """Create a recurring-activity template. Mirrors create_activity's gates so a series can
    never define a meetup the owner couldn't create one-off. cohort is pinned from the owner."""
    if not can_create_activity(owner):
        raise NotEligible(
            _("User cannot create activity series (needs verification/consent + a cohort).")
        )
    # F29: supervised implies guardian_accompanied (so each spawned instance can seat a supervisor).
    if supervised:
        if owner.cohort != Cohort.CHILD:
            raise InvalidState(_("Only children's activities can require a supervising guardian."))
        guardian_accompanied = True
    if guardian_accompanied and owner.cohort != Cohort.CHILD:
        raise InvalidState(_("Only children's activities can be guardian-accompanied."))
    if min_to_go is not None and capacity is not None and min_to_go > capacity:
        raise InvalidState(_("Minimum to happen can't be more than the capacity."))
    if cadence not in ActivitySeries.Cadence.values:
        raise InvalidState(_("Invalid cadence."))
    from apps.places.services import public_places

    if place is None or not public_places().filter(pk=place.pk).exists():
        raise InvalidState(_("That place isn't available to organise an activity at yet."))
    # F9: a CHILD series must template a known public venue type too — otherwise it would be a
    # "zombie series" that silently never spawns (every spawn re-checks the same gate). Same gate,
    # same message, same flag as create_activity.
    if owner.cohort == Cohort.CHILD and getattr(settings, "CHILD_PUBLIC_VENUES_ONLY", True):
        from apps.places.services import is_child_safe_venue

        if not is_child_safe_venue(place):
            raise InvalidState(
                _(
                    "This venue isn't on the approved list for children's activities yet. Pick a "
                    "library, park, school, sports or community venue — or ask a moderator to "
                    "approve this place."
                )
            )
    # W3-F2: enforce the guardian category envelope on a CHILD series too — every spawned instance
    # re-checks it, but blocking at template time avoids a "zombie series" that never spawns.
    if not category_envelope_allows(owner, activity_type):
        raise InvalidState(_("Your guardian's settings don't allow this kind of activity yet."))
    duration = None
    if ends_at is not None and ends_at > first_starts_at:
        duration = int((ends_at - first_starts_at).total_seconds() // 60)
    series = ActivitySeries.objects.create(
        owner=owner,
        place=place,
        activity_type=activity_type,
        cohort=owner.cohort,
        title=title,
        description=description,
        cadence=cadence,
        next_starts_at=first_starts_at,
        anchor_day=timezone.localtime(first_starts_at).day,
        duration_minutes=duration,
        join_threshold=DEFAULT_JOIN_THRESHOLD if join_threshold is None else join_threshold,
        capacity=capacity,
        min_to_go=min_to_go,
        guardian_accompanied=guardian_accompanied,
        supervised=supervised,
        meeting_point=meeting_point,
        what_to_bring=what_to_bring,
        organizer_note=organizer_note,
        getting_home_note=getting_home_note,
        cost_band=cost_band,
        difficulty=difficulty,
        accessibility_notes=accessibility_notes,
        beginners_welcome=beginners_welcome,
    )
    from apps.safety.services import record_audit

    record_audit("series.created", actor=owner, target=series)
    return series


@transaction.atomic
def pause_series(owner, series) -> ActivitySeries:
    """Owner pauses a series (no further instances spawn). Reversible via resume_series."""
    if series.owner_id != getattr(owner, "id", None):
        raise NotAMember(_("Only the series owner may pause it."))
    if series.status != ActivitySeries.Status.ACTIVE:
        raise InvalidState(_("Only an active series can be paused."))
    series.status = ActivitySeries.Status.PAUSED
    series.save(update_fields=["status", "updated_at"])
    from apps.safety.services import record_audit

    record_audit("series.paused", actor=owner, target=series)
    return series


@transaction.atomic
def resume_series(owner, series) -> ActivitySeries:
    """Owner resumes a paused series. The spawn job fast-forwards a stale cursor to the next
    future slot, so resuming never backfills missed past meetups."""
    if series.owner_id != getattr(owner, "id", None):
        raise NotAMember(_("Only the series owner may resume it."))
    if series.status != ActivitySeries.Status.PAUSED:
        raise InvalidState(_("Only a paused series can be resumed."))
    series.status = ActivitySeries.Status.ACTIVE
    series.save(update_fields=["status", "updated_at"])
    from apps.safety.services import record_audit

    record_audit("series.resumed", actor=owner, target=series)
    return series


NEXT_INSTANCE_NOTE_MAX = 500  # mirrors ActivitySeries.next_instance_note max_length


@transaction.atomic
def set_next_instance_note(owner, series, note: str) -> ActivitySeries:
    """W2-F14: stage a one-shot note appended to ONLY the next spawned instance's organizer_note,
    then auto-cleared on that spawn (consume-once) — timely per-occurrence guidance ("back pitch
    this time, bring cleats") without waiting for the spawn or editing every instance. Owner-scoped;
    refused on an ENDED series. Capped at the model max here too (the form also caps it, but the
    nightly spawn never re-validates a form). Pass "" to clear a staged note."""
    if series.owner_id != getattr(owner, "id", None):
        raise NotAMember(_("Only the series owner may set the next-meetup note."))
    if series.status == ActivitySeries.Status.ENDED:
        raise InvalidState(_("This series has ended."))
    series.next_instance_note = (note or "").strip()[:NEXT_INSTANCE_NOTE_MAX]
    series.save(update_fields=["next_instance_note", "updated_at"])
    from apps.safety.services import record_audit

    record_audit("series.next_note_set", actor=owner, target=series)
    return series


@transaction.atomic
def end_series(owner, series) -> ActivitySeries:
    """Owner ends a series permanently. Already-spawned instances stand (Activity.series is
    SET_NULL-safe); an ENDED series never spawns again."""
    if series.owner_id != getattr(owner, "id", None):
        raise NotAMember(_("Only the series owner may end it."))
    if series.status == ActivitySeries.Status.ENDED:
        raise InvalidState(_("This series has already ended."))
    series.status = ActivitySeries.Status.ENDED
    series.save(update_fields=["status", "updated_at"])
    from apps.safety.services import record_audit

    record_audit("series.ended", actor=owner, target=series)
    return series


def visible_series(user):
    """Series a user may see/manage. A series is an owner-management template (not a meetup),
    so it is scoped to its owner — peers discover the spawned Activities via the cohort feed,
    never the template. Staff see all (moderation). The single read chokepoint for series."""
    qs = ActivitySeries.objects.select_related("owner", "place", "activity_type")
    if not getattr(user, "is_authenticated", False):
        return qs.none()
    if user.is_staff:
        return qs
    return qs.filter(owner=user)


def spawn_due_series(*, now=None) -> dict:
    """Nightly engine: spawn the next single instance of each due ACTIVE series via the normal
    create_activity path. One instance per series per tick, materialised SERIES_SPAWN_LEAD_DAYS
    ahead so members can discover/join before it starts; never backfills past occurrences and
    never keeps more than one upcoming instance live at once. Per-series isolation — one broken
    series never aborts the tick. cohort is re-asserted against the owner's CURRENT cohort before
    every spawn (create_activity always re-pins cohort=owner.cohort, so a drift would otherwise
    spawn into the wrong cohort). No request user: audits actor=series.owner; the summary is
    actor-less. Idempotent."""
    from apps.safety.services import record_audit

    now = now or timezone.now()
    lead = now + timedelta(days=getattr(settings, "SERIES_SPAWN_LEAD_DAYS", 14))
    cap = getattr(settings, "SERIES_SPAWN_BATCH", 500)
    spawned = skipped = paused = 0
    due = (
        ActivitySeries.objects.filter(status=ActivitySeries.Status.ACTIVE, next_starts_at__lte=lead)
        .select_related("owner", "place", "activity_type")
        .order_by("id")
    )
    for series in due.iterator():
        if spawned >= cap:
            break  # anomaly guard: never mass-spawn in a single tick
        try:
            with transaction.atomic():
                # Lock the row so two overlapping ticks can't double-spawn the same slot; a row
                # already held by another tick is skipped this run (skip_locked) and picked up next.
                series = (
                    ActivitySeries.objects.select_for_update(skip_locked=True)
                    .select_related("owner", "place", "activity_type")
                    .filter(pk=series.pk, status=ActivitySeries.Status.ACTIVE)
                    .first()
                )
                if series is None:
                    continue
                # Cohort re-assert (the isolation boundary). Only a cohort DRIFT pauses; a
                # transient eligibility/place loss is left to create_activity (raises -> skip).
                if series.owner.cohort != series.cohort:
                    series.status = ActivitySeries.Status.PAUSED
                    series.save(update_fields=["status", "updated_at"])
                    record_audit(
                        "series.paused",
                        actor=series.owner,
                        target=series,
                        reason="owner_cohort_drift",
                    )
                    paused += 1
                    continue
                # One upcoming instance at a time ("only the next single instance"): don't create a
                # second future instance while one is still live/upcoming.
                if series.instances.filter(starts_at__gte=now).exists():
                    continue
                # Fast-forward past any slot already in the past (long pause / missed ticks) — never
                # spawn a past-dated meetup nobody can attend, and never backfill the missed ones.
                guard = 0
                while series.next_starts_at < now and guard < 240:
                    series.next_starts_at = _advance_slot(
                        series.next_starts_at, series.cadence, series.anchor_day
                    )
                    guard += 1
                if series.next_starts_at < now:
                    # Guard cap hit on an implausibly stale cursor: never spawn a past-dated meetup.
                    # Persist the partial fast-forward and skip; it self-heals over later ticks.
                    logger.error(
                        "spawn_due_series: series %s cursor still stale after fast-forward cap",
                        series.pk,
                    )
                    series.save(update_fields=["next_starts_at", "updated_at"])
                    skipped += 1
                    continue
                if series.next_starts_at > lead:
                    # Next future slot isn't within the lead window yet — persist cursor and wait.
                    series.save(update_fields=["next_starts_at", "updated_at"])
                    continue
                # Idempotency: a prior tick may already have materialised this exact slot.
                if series.instances.filter(starts_at=series.next_starts_at).exists():
                    series.next_starts_at = _advance_slot(
                        series.next_starts_at, series.cadence, series.anchor_day
                    )
                    series.save(update_fields=["next_starts_at", "updated_at"])
                    continue
                ends_at = None
                if series.duration_minutes:
                    ends_at = series.next_starts_at + timedelta(minutes=series.duration_minutes)
                # W2-F14: a staged one-shot note is APPENDED to this instance's organizer_note
                # (never replacing the standing template note), then consumed below so it lands on
                # exactly one spawn. Atomic + race-safe under the held select_for_update row.
                instance_organizer_note = "\n\n".join(
                    part for part in (series.organizer_note, series.next_instance_note) if part
                )
                activity = create_activity(
                    series.owner,
                    place=series.place,
                    activity_type=series.activity_type,
                    title=series.title,
                    starts_at=series.next_starts_at,
                    ends_at=ends_at,
                    description=series.description,
                    join_threshold=series.join_threshold,
                    capacity=series.capacity,
                    min_to_go=series.min_to_go,
                    guardian_accompanied=series.guardian_accompanied,
                    supervised=series.supervised,
                    meeting_point=series.meeting_point,
                    what_to_bring=series.what_to_bring,
                    organizer_note=instance_organizer_note,
                    getting_home_note=series.getting_home_note,
                    cost_band=series.cost_band,
                    difficulty=series.difficulty,
                    accessibility_notes=series.accessibility_notes,
                    beginners_welcome=series.beginners_welcome,
                )
                activity.series = series
                activity.save(update_fields=["series"])
                series.next_instance_note = ""  # consume the one-shot note (this spawn only)
                series.next_starts_at = _advance_slot(
                    series.next_starts_at, series.cadence, series.anchor_day
                )
                series.save(update_fields=["next_starts_at", "next_instance_note", "updated_at"])
                record_audit(
                    "series.spawned", actor=series.owner, target=activity, series_id=series.id
                )
                spawned += 1
        except SocialError:
            # Owner lost eligibility / place un-published since create -> clean per-series skip
            # (self-heals when they re-verify / the place re-publishes), never abort the tick.
            logger.exception("spawn_due_series: skipping series %s (social gate)", series.pk)
            skipped += 1
        except Exception:  # noqa: BLE001 — one broken series must not kill the whole tick
            logger.exception("spawn_due_series: skipping series %s after an error", series.pk)
            skipped += 1
    record_audit("series.swept", spawned=spawned, skipped=skipped, paused=paused)
    return {"spawned": spawned, "skipped": skipped, "paused": paused}


@transaction.atomic
def request_to_join(user, activity) -> Membership:
    if not can_join(user, activity):
        raise NotEligible(_("User is not eligible to join this activity."))
    membership = Membership.objects.create(
        activity=activity,
        user=user,
        role=Membership.Role.MEMBER,
        state=Membership.State.REQUESTED,
    )
    _notify(
        activity.owner,
        "join_requested",
        "New join request",
        body=f"{user.display_name or user.username} asked to join “{activity.title}”.",
        url=f"/api/social/activities/{activity.id}/",
    )
    return membership


@transaction.atomic
def leave_activity(user, activity) -> Membership | None:
    """A member leaves an activity. The owner cannot leave their own activity (they must
    cancel it instead). Returns the removed membership, or None if not a member."""
    membership = activity.memberships.filter(user=user).first()
    if membership is None or membership.state == Membership.State.REMOVED:
        return None
    if membership.role == Membership.Role.OWNER:
        raise InvalidState(_("The owner cannot leave their own activity."))
    membership.state = Membership.State.REMOVED
    # Reset the per-activity transient signals so a removed row carries nothing: the RSVP
    # go/no-go (F20), the "we met up" confirmation (F22), the W2-F9 transit cue, and the W3-F3
    # "heading home" ping. Keeps each scoped to live members so re-joining starts clean and
    # nothing aggregates per-user.
    membership.attendance_intent = Membership.AttendanceIntent.UNKNOWN
    membership.met_confirmed_at = None
    membership.transit_status = Membership.TransitStatus.NONE
    membership.departing_at = None
    membership.save(
        update_fields=[
            "state",
            "attendance_intent",
            "met_confirmed_at",
            "transit_status",
            "departing_at",
            "updated_at",
        ]
    )
    return membership


# Fields an owner may change on an OPEN, not-yet-started activity. Deliberately excludes
# place / activity_type / cohort / owner / guardian_accompanied: those define the meetup's
# identity and the cohort-isolation boundary, so an edit must never touch them (no
# bait-and-switch, no escaping the safety pin). See docs/SAFETY.md.
ACTIVITY_EDITABLE_FIELDS = (
    "title",
    "description",
    "starts_at",
    "ends_at",
    "capacity",
    "min_to_go",  # F1 Quorum-go — owner-curated minimum-to-happen threshold
    "meeting_point",  # F9 logistics — owner-curated, routed through the same edit path
    "what_to_bring",
    "organizer_note",
    "getting_home_note",  # F18 — mirrored onto a CHILD ward's guardian manifest
    "first_time_note",  # F41 — member-only "what to expect when you arrive" note
    "fallback_meeting_point",  # W3-F8 — member-only plan-B spot within the venue
    "cost_band",  # F8 what-to-expect
    "difficulty",
    "accessibility_notes",
    "beginners_welcome",  # F17 per-activity flag
    "fallback_starts_at",  # W2-F10 owner-curated plan-B time (consumed by invoke_fallback)
)


@transaction.atomic
def cancel_activity(owner, activity, *, reason: str = "") -> Activity:
    """Owner cancels a meetup they can no longer host. Flips the activity to CANCELLED
    (so it leaves discovery/joining) and tells every current member, with the reason, so
    nobody travels to a meetup that isn't happening. Idempotent-safe: only an OPEN
    activity can be cancelled."""
    if not is_organizer(owner, activity):
        raise NotAMember(_("Only the activity organiser may cancel it."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("Only an open activity can be cancelled."))
    activity.status = Activity.Status.CANCELLED
    activity.save(update_fields=["status", "updated_at"])
    reason = (reason or "").strip()[:200]
    body = _("“%(title)s” was cancelled by the organiser.") % {"title": activity.title}
    if reason:
        body = f"{body} {reason}"
    for membership in current_members(activity).exclude(user_id=owner.id).select_related("user"):
        _notify(
            membership.user,
            "activity_cancelled",
            _("An activity was cancelled"),
            body=body,
            url=f"/api/social/activities/{activity.id}/",
        )
    from apps.safety.services import record_audit

    record_audit("activity.cancelled", actor=owner, target=activity, reason=reason)
    return activity


@transaction.atomic
def complete_activity(activity) -> Activity:
    """Move a past OPEN activity to its terminal COMPLETED state. Housekeeping only — no
    notification — so a finished meetup stops being shown as live. No-op unless OPEN."""
    if activity.status != Activity.Status.OPEN:
        return activity
    activity.status = Activity.Status.COMPLETED
    activity.save(update_fields=["status", "updated_at"])
    return activity


def _supersede_reminders(activity) -> None:
    """Clear any already-sent event reminders for this activity so a changed start time
    re-fires one. send_activity_reminders dedups on (recipient, kind, url) and the url
    carries no time, so without this a corrected time would silently never be reminded."""
    from apps.notifications.models import Notification

    Notification.objects.filter(
        kind=Notification.Kind.EVENT_REMINDER,
        url=f"/api/social/activities/{activity.id}/",
    ).delete()


@transaction.atomic
def update_activity(owner, activity, **changes) -> Activity:
    """Owner edits an OPEN, not-yet-started activity in place (preserving its roster,
    thread and vote history). Only ACTIVITY_EDITABLE_FIELDS are honoured; a material time
    change re-notifies members and supersedes the stale reminder."""
    if not is_organizer(owner, activity):
        raise NotAMember(_("Only the activity organiser may edit it."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("Only an open activity can be edited."))
    if activity.starts_at <= timezone.now():
        raise InvalidState(_("This activity has already started and can no longer be edited."))

    fields = {k: v for k, v in changes.items() if k in ACTIVITY_EDITABLE_FIELDS}
    new_starts = fields.get("starts_at", activity.starts_at)
    new_ends = fields.get("ends_at", activity.ends_at)
    if new_ends is not None and new_ends < new_starts:
        raise InvalidState(_("End time cannot be before the start time."))
    # W2-F10: keep the plan-B time after the (possibly newly-edited) start. invoke_fallback NULLs
    # the fallback before its own update_activity call, so this never blocks the fallback path.
    new_fallback = fields.get("fallback_starts_at", activity.fallback_starts_at)
    if new_fallback is not None and new_fallback <= new_starts:
        raise InvalidState(_("The plan-B time must be after the planned start."))
    new_capacity = fields.get("capacity", activity.capacity)
    if new_capacity is not None and new_capacity < participant_count(activity):
        raise InvalidState(_("Capacity cannot be lower than the current number of participants."))
    new_min_to_go = fields.get("min_to_go", activity.min_to_go)
    if new_min_to_go is not None and new_capacity is not None and new_min_to_go > new_capacity:
        raise InvalidState(_("Minimum to happen can't be more than the capacity."))

    time_changed = "starts_at" in fields and fields["starts_at"] != activity.starts_at
    if not fields:
        return activity
    for key, value in fields.items():
        setattr(activity, key, value)
    activity.save(update_fields=[*fields.keys(), "updated_at"])

    if "min_to_go" in fields:
        # F1: lowering the threshold can make a still-open meetup cross its (now-lower) minimum on
        # the LIVE count without any new RSVP — fire the one-shot confirm exactly as an RSVP would.
        # _maybe_confirm_meetup's go_confirmed_at guard + OPEN check keep it at-most-once and safe.
        _maybe_confirm_meetup(activity)

    if time_changed:
        _supersede_reminders(activity)
        body = _("“%(title)s” now starts %(when)s.") % {
            "title": activity.title,
            "when": f"{activity.starts_at:%Y-%m-%d %H:%M}",
        }
        for membership in (
            current_members(activity).exclude(user_id=owner.id).select_related("user")
        ):
            _notify(
                membership.user,
                "activity_updated",
                _("An activity you joined changed"),
                body=body,
                url=f"/api/social/activities/{activity.id}/",
            )
    return activity


@transaction.atomic
def invoke_fallback(owner, activity) -> Activity:
    """W2-F10: shift a meetup to its single pre-declared plan-B time, ONCE — so a rained-out or
    quorum-short meetup gently moves instead of dying. Organiser-only (owner or F22 co-organiser),
    OPEN, and requires a fallback_starts_at that is still strictly in the future. Routes through
    update_activity so it inherits _supersede_reminders + the member re-notify (and the CHILD
    guardian manifest reflects the new time for free), and writes its OWN audit entry
    (update_activity itself isn't audited). ONE-USE LATCH: fallback_starts_at is cleared in the SAME
    transaction, so a re-invoke can never loop into an open-ended reschedule.

    SAFETY BOUNDARY (F7): a later start could in principle push a CHILD meetup past a guardrail's
    latest_start_hour — but that guardrail is a JOIN-time gate, not an edit-time one, exactly like
    the existing update_activity time-change path. We deliberately do NOT add a ward-eviction path
    here; the shift is surfaced to guardians read-time via the wards manifest, like any edit."""
    from apps.safety.services import record_audit

    if not is_organizer(owner, activity):
        raise NotAMember(_("Only the activity organiser may use the plan-B time."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("Only an open activity can fall back to its plan-B time."))
    if activity.fallback_starts_at is None:
        raise InvalidState(_("This activity has no plan-B time set."))
    if activity.fallback_starts_at <= timezone.now():
        raise InvalidState(_("The plan-B time has already passed."))
    target = activity.fallback_starts_at
    # One-use latch FIRST (same txn): clear the backup so it can't be reused into a reschedule loop.
    activity.fallback_starts_at = None
    activity.save(update_fields=["fallback_starts_at", "updated_at"])
    # update_activity re-checks organiser/OPEN/before-start and fires the time-change re-notify; if
    # it rejects (e.g. the ORIGINAL start has passed), the atomic block rolls the latch back too.
    activity = update_activity(owner, activity, starts_at=target)
    record_audit("activity.fallback_invoked", actor=owner, target=activity)
    return activity


def _notify(recipient, kind, title, *, body="", url=""):
    """Emit an in-app notification (best-effort; never blocks the social action). Returns the
    created Notification, or None when the recipient has muted this (mutable) kind, so callers
    that need an honest delivery signal can branch on it."""
    from apps.notifications.services import notify

    return notify(recipient, kind, title, body=body, url=url)


def _is_genuinely_new(membership: Membership) -> bool:
    """True when the joiner holds no OTHER current MEMBER membership — i.e. this is their first
    activity. A presence/absence fact about the joiner themselves (never a rating); used to fire
    the first-timer welcome at most once. Self excluded by pk so it's robust to flush order."""
    return not (
        Membership.objects.filter(user_id=membership.user_id, state=Membership.State.MEMBER)
        .exclude(pk=membership.pk)
        .exists()
    )


# --- F29: verified-adult supervisor seat ---------------------------------------------
# A supervised CHILD activity cannot SETTLE a join until the owner's OWN verified guardian is
# seated as a read-only GUARDIAN member. "Is a supervisor present now" is derived LIVE from
# memberships (never stored) — keyed strictly on is_guardian_of(guardian, OWNER), never loosened
# to "any participant" (that would open an adult -> other-people's-minors read-window).


def active_supervisor_present(activity) -> bool:
    """True iff a verified guardian OF THE OWNER is currently seated as a GUARDIAN member.
    Computed live so the supervision chip can never lie after the guardian leaves/is removed."""
    from apps.accounts.services import is_guardian_of

    owner = activity.owner
    guardian_memberships = activity.memberships.filter(
        role=Membership.Role.GUARDIAN, state=Membership.State.MEMBER
    ).select_related("user")
    return any(is_guardian_of(m.user, owner) for m in guardian_memberships)


def supervision_satisfied(activity) -> bool:
    """An activity that doesn't require supervision is always satisfied; a supervised one needs a
    live supervisor of the owner. The single predicate both the settle gate and the chip use."""
    if not activity.supervised:
        return True
    return active_supervisor_present(activity)


def _settle_pending_joins(activity) -> None:
    """Re-evaluate REQUESTED memberships so any that already cleared the vote threshold but couldn't
    settle for lack of a supervisor are admitted now (called after a supervisor is seated)."""
    for membership in activity.memberships.filter(state=Membership.State.REQUESTED):
        _evaluate_vote(membership)


def _admit(membership: Membership) -> None:
    # F29: a supervised activity cannot settle a join until the owner's guardian supervises it.
    # Fail-closed no-op (the vote/approval is preserved on the still-REQUESTED row and settles via
    # _settle_pending_joins once the supervisor is seated). owner_admit raises a clear message.
    if not supervision_satisfied(membership.activity):
        return
    membership.state = Membership.State.MEMBER
    membership.decided_at = timezone.now()
    body = str(_("You were admitted to “%(title)s”.") % {"title": membership.activity.title})
    # F39: a genuinely-new joiner (their first activity) gets a one-time welcome line on this
    # notification + a self-dismissing banner; welcomed_at makes it at-most-once.
    is_new = membership.welcomed_at is None and _is_genuinely_new(membership)
    update_fields = ["state", "decided_at", "updated_at"]
    if is_new:
        membership.welcomed_at = timezone.now()
        update_fields.append("welcomed_at")
        body += str(
            _(
                " New here? Say a quick hello in the thread and check the meetup logistics — "
                "the group is glad you joined."
            )
        )
    membership.save(update_fields=update_fields)
    _notify(
        membership.user,
        "join_approved",
        "You're in!",
        body=body,
        url=f"/api/social/activities/{membership.activity_id}/",
    )


def _vote_threshold_met(membership: Membership) -> bool:
    """Whether a requested membership's approvals have cleared the activity's join threshold — the
    exact condition _evaluate_vote uses to admit. Extracted so the W3-F7 supervisor nudge re-runs
    the SAME check — never a bare 'a REQUESTED row exists' (which must not summon a guardian)."""
    member_count = voting_members(membership.activity).count()
    if member_count == 0:
        return False
    approvals = membership.votes.filter(approve=True).count()
    return approvals / member_count >= membership.activity.join_threshold


def _evaluate_vote(membership: Membership) -> None:
    """Promote a requested membership to member once approvals clear the threshold."""
    if _vote_threshold_met(membership):
        _admit(membership)


def join_stuck_on_supervision(activity) -> bool:
    """W3-F7: True iff a supervised activity has at least one REQUESTED join that has CLEARED the
    vote threshold (so it WOULD be admitted) but cannot settle because no supervisor is seated —
    exactly the _admit fail-closed no-op. Never merely "a REQUESTED row exists"; a request nobody
    has voted through must never summon a guardian."""
    if not activity.supervised or supervision_satisfied(activity):
        return False
    return any(
        _vote_threshold_met(m)
        for m in activity.memberships.filter(state=Membership.State.REQUESTED)
    )


@transaction.atomic
def cast_vote(voter, membership: Membership, approve: bool) -> Membership:
    activity = membership.activity
    if membership.state != Membership.State.REQUESTED:
        raise InvalidState(_("This membership is not awaiting a join vote."))
    if membership.user_id == voter.id:
        raise InvalidState("A requester cannot vote on their own join request.")
    if not voting_members(activity).filter(user=voter).exists():
        raise NotAMember(_("Only current members may vote on join requests."))
    JoinVote.objects.update_or_create(
        membership=membership, voter=voter, defaults={"approve": approve}
    )
    _evaluate_vote(membership)
    return membership


@transaction.atomic
def owner_admit(owner, membership: Membership) -> Membership:
    """Organiser override: admit a requested member directly (if enabled for the activity)."""
    activity = membership.activity
    if not is_organizer(owner, activity):
        raise NotAMember(_("Only the activity organiser may override."))
    if not activity.owner_can_override:
        raise InvalidState(_("Owner override is disabled for this activity."))
    if membership.state != Membership.State.REQUESTED:
        raise InvalidState(_("This membership is not awaiting a join vote."))
    # F29: surface the bootstrap clearly — the owner must seat their guardian supervisor before
    # anyone can be admitted to a supervised activity (the vote path just waits silently).
    if not supervision_satisfied(activity):
        raise InvalidState(
            _("Add your guardian as this activity's supervisor before admitting members.")
        )
    _admit(membership)
    return membership


@transaction.atomic
def add_guardian(owner, activity, guardian) -> Membership:
    """The child owner adds a verified adult as an accompanying guardian (supervisory,
    group-only). Controlled exception to cohort isolation: only on a CHILD-cohort
    activity explicitly flagged guardian_accompanied, and the guardian must be a
    verified adult. Guardians don't vote and aren't open-discoverable. See docs/SAFETY.md.
    """
    from apps.accounts.services import is_guardian_of

    if activity.owner_id != owner.id:
        raise NotAMember("Only the activity owner may add a guardian.")
    if not activity.guardian_accompanied or activity.cohort != Cohort.CHILD:
        raise InvalidState("This activity does not allow accompanying guardians.")
    if guardian.cohort != Cohort.ADULT or not can_participate(guardian):
        raise NotEligible("A guardian must be a verified adult.")
    if not is_guardian_of(guardian, owner):
        raise NotEligible("This adult is not a registered guardian of the activity owner.")
    existing = (
        activity.memberships.filter(user=guardian).exclude(state=Membership.State.REMOVED).first()
    )
    if existing:
        return existing
    membership = Membership.objects.create(
        activity=activity,
        user=guardian,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )
    # F29: seating the supervisor unblocks any join that already cleared the vote but couldn't
    # settle for lack of supervision (the bootstrap: requests can arrive before the guardian).
    if activity.supervised:
        _settle_pending_joins(activity)
    return membership


@transaction.atomic
def set_activity_supervision(owner, activity, supervised: bool) -> Activity:
    """Owner toggles the F29 supervised pin AFTER create — a guarded service, deliberately NOT in
    ACTIVITY_EDITABLE_FIELDS (which freezes the cohort-isolation boundary). Enabling implies
    guardian_accompanied so the supervisor can be seated; CHILD-only; audited. Disabling (or, once
    enabled, seating the guardian) releases any join that was waiting on supervision."""
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the activity owner may change supervision."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("Only an open activity's supervision can be changed."))
    if supervised and activity.cohort != Cohort.CHILD:
        raise InvalidState(_("Only children's activities can require a supervising guardian."))
    activity.supervised = bool(supervised)
    fields = ["supervised", "updated_at"]
    if supervised and not activity.guardian_accompanied:
        activity.guardian_accompanied = True
        fields.append("guardian_accompanied")
    activity.save(update_fields=fields)
    from apps.safety.services import record_audit

    record_audit(
        "activity.supervision_set", actor=owner, target=activity, supervised=bool(supervised)
    )
    # If supervision is now satisfied (turned off, or already-seated guardian), settle any join
    # that cleared the vote while blocked.
    _settle_pending_joins(activity)
    return activity


def _coorg_eligible(activity, user):
    """A current, same-cohort, non-GUARDIAN MEMBER who isn't the owner — the only kind of person who
    can be made a co-organiser or handed ownership. Returns the Membership row or None.

    Re-checks the member's LIVE cohort + participation eligibility, not just the stale Membership
    row: the activity cohort wall is otherwise enforced only at read time (visible_activities /
    can_read_thread), so a member re-verified ADULT->minor after joining keeps a stale MEMBER row on
    an (immutable-cohort) ADULT activity. Mirroring that read-time wall here makes the "adult-only,
    same-cohort" promise structural — a cross-cohort or no-longer-eligible member can never be
    promoted to organiser/owner (no adult<->minor organiser path via a downgraded seat)."""
    if user.id == activity.owner_id:
        return None
    if getattr(user, "cohort", None) != activity.cohort or not can_participate(user):
        return None
    return (
        current_members(activity).exclude(role=Membership.Role.GUARDIAN).filter(user=user).first()
    )


@transaction.atomic
def grant_co_organizer(owner, activity, member) -> Membership:
    """F22: the OWNER grants a current member co-organiser rights. ADULT activities only — a child/
    teen activity refuses peer organiser handoff entirely (no adult<->minor organiser path). A
    co-organiser shares the operational owner-actions via is_organizer (cancel/edit/admit/announce)
    but NOT the meta-powers — grant/revoke/transfer stay owner-only, so a co-organiser can never
    lock the owner out."""
    from apps.notifications.models import Notification
    from apps.notifications.services import notify
    from apps.safety.services import record_audit

    # Lock the activity row and re-check the owner FK on the locked instance: the owner gate + the
    # role write must serialize against any concurrent meta-power call (grant/revoke/transfer), so
    # two in-flight requests can't leave split-brain role state (mirrors _maybe_confirm_meetup).
    activity = Activity.objects.select_for_update().get(pk=activity.pk)
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the activity owner may grant co-organiser rights."))
    if activity.cohort != Cohort.ADULT:
        raise InvalidState(_("Co-organisers are only available on adult activities."))
    m = _coorg_eligible(activity, member)
    if m is None:
        raise NotAMember(_("A co-organiser must be a current member of this activity."))
    if m.role != Membership.Role.CO_ORGANIZER:
        m.role = Membership.Role.CO_ORGANIZER
        m.save(update_fields=["role"])
        record_audit(
            "activity.co_organizer_granted", actor=owner, target=activity, member_ref=member.id
        )
        notify(
            member,
            Notification.Kind.ORGANIZER_ROLE,
            str(_("You're now a co-organiser")),
            body=str(
                _(
                    'You can now help run "%(title)s" — edit details, admit members, and post '
                    "announcements."
                )
                % {"title": activity.title}
            ),
            url=f"/activities/{activity.id}/",
        )
    return m


@transaction.atomic
def revoke_co_organizer(owner, activity, member) -> Membership:
    """F22: the OWNER removes a member's co-organiser rights (back to a plain member)."""
    from apps.notifications.models import Notification
    from apps.notifications.services import notify
    from apps.safety.services import record_audit

    activity = Activity.objects.select_for_update().get(
        pk=activity.pk
    )  # serialize meta-power calls
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the activity owner may change co-organiser rights."))
    m = current_members(activity).filter(user=member, role=Membership.Role.CO_ORGANIZER).first()
    if m is None:
        raise NotAMember(_("That member is not a co-organiser."))
    m.role = Membership.Role.MEMBER
    m.save(update_fields=["role"])
    record_audit(
        "activity.co_organizer_revoked", actor=owner, target=activity, member_ref=member.id
    )
    notify(
        member,
        Notification.Kind.ORGANIZER_ROLE,
        str(_("Your co-organiser role was removed")),
        body=str(_('You\'re still a member of "%(title)s".') % {"title": activity.title}),
        url=f"/activities/{activity.id}/",
    )
    return m


@transaction.atomic
def transfer_ownership(owner, activity, new_owner) -> Activity:
    """F22: the OWNER hands the activity over to a current member — so a thriving meetup survives
    the organiser stepping down (and so an owner can leave before a GDPR erasure CASCADE-destroys
    the thread). The new owner's membership becomes OWNER, the old owner stays on as a plain MEMBER,
    and the Activity.owner FK + the denormalised roles are updated together. ADULT activities only;
    never to a guardian."""
    from apps.notifications.models import Notification
    from apps.notifications.services import notify
    from apps.safety.services import record_audit

    # Lock + re-check on the committed row: this is the one service that denormalises a role across
    # multiple rows (new owner -> OWNER, old owner -> MEMBER) and reassigns the owner FK, so two
    # concurrent transfers must serialize — otherwise the loser leaves a second stale OWNER row.
    activity = Activity.objects.select_for_update().get(pk=activity.pk)
    if activity.owner_id != owner.id:
        raise NotAMember(_("Only the current organiser may hand off the activity."))
    if activity.cohort != Cohort.ADULT:
        raise InvalidState(_("Ownership hand-off is only available on adult activities."))
    target = _coorg_eligible(activity, new_owner)
    if target is None:
        raise NotAMember(_("You can only hand off to a current member of this activity."))
    old = current_members(activity).filter(user=owner, role=Membership.Role.OWNER).first()
    activity.owner = new_owner
    activity.save(update_fields=["owner"])
    target.role = Membership.Role.OWNER
    target.save(update_fields=["role"])
    if old is not None:  # demote the former owner to a plain member (they stepped down)
        old.role = Membership.Role.MEMBER
        old.save(update_fields=["role"])
    record_audit(
        "activity.ownership_transferred", actor=owner, target=activity, new_owner_ref=new_owner.id
    )
    notify(
        new_owner,
        Notification.Kind.ORGANIZER_ROLE,
        str(_("You're now the organiser")),
        body=str(
            _('"%(title)s" was handed over to you — you can now fully manage it.')
            % {"title": activity.title}
        ),
        url=f"/activities/{activity.id}/",
    )
    return activity


@transaction.atomic
def post_to_thread(
    author,
    activity,
    body: str,
    *,
    reply_to=None,
    allow_empty=False,
    ping=False,
    share_activity=None,
    share_place=None,
    share_event=None,
) -> Post:
    """THE single write path for an activity thread, shared by the web form, the DRF API,
    and the WebSocket consumer (via post_to_thread_realtime). It enforces the FULL union of
    the gates the two old surfaces had — so the child-safety gate holds identically on every
    surface (the whole point of collapsing Post + chat into one stream).

    Gate: current MEMBER (not a supervisory guardian) + verified/consented participation +
    the activity isn't moderator-hidden + the activity isn't CANCELLED (OPEN *and* COMPLETED
    both admit posts, so the post-meetup "thanks for coming" + F22 "did we meet?" flow keep
    working — only a cancelled meetup freezes its thread) + not blocked-vs-owner + a per-user
    rate limit + the swappable MessagePolicy/CSAR content seam. ``reply_to`` is validated to
    the same thread, must not be hidden, and is re-parented to its top-level ancestor so the
    tree can never exceed one level. A committed write schedules a live broadcast on commit.

    ``@mentions`` in the body are always rendered as a calm highlight (tag-not-ping). ``ping``
    is an explicit author opt-in: only then is a MENTION notification fanned out — to PEER
    members named in the body, minus the author and minus blocked pairs, and still mutable by
    each recipient (no engagement-maxxing, no surprise pings)."""
    from apps.chat.policy import get_message_policy  # local: avoid social<->chat import cycle
    from apps.safety.services import allow_action, is_blocked

    membership = thread_members(activity).filter(user=author).first()
    if membership is None:
        raise NotAMember(_("Only current members can post in the activity thread."))
    if membership.role == Membership.Role.GUARDIAN:
        # Guardians accompany children's activities as transparent, read-only supervisors;
        # an adult must not post into a children's thread (cohort isolation for the peers).
        # Vestigial for a Group (GroupMembership has no GUARDIAN role — a test pins this), kept
        # unconditionally so a future GUARDIAN role could never silently gain posting rights.
        raise NotEligible(_("Guardians accompany activities as read-only supervisors."))
    # Minor-cohort GROUP threads are ANNOUNCEMENT-ONLY: peers read, only the owner/staff broadcasts
    # (post_announcement). On a standing, city-wide minor group this collapses the active-poster
    # enumeration surface to zero — the per-meetup Activity thread is where minors actually
    # converse, naturally bounded to that meetup. A no-op for activity threads and adult groups;
    # bylines elsewhere stay so any post author can still be reported.
    if isinstance(activity, Group) and activity.cohort in (Cohort.CHILD, Cohort.TEEN):
        raise NotEligible(
            _("This group's thread is announcement-only; the organiser posts updates here.")
        )
    if not can_participate(author):
        # Catches a member whose parental consent was revoked or assurance lapsed after join.
        raise NotEligible(_("Posting requires verified, consented participation."))
    if getattr(activity, "is_hidden", False):
        raise InvalidState(_("This activity is no longer available."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    if author.id != activity.owner_id and is_blocked(author, activity.owner):
        raise InvalidState(_("This activity is no longer available."))
    limit = getattr(settings, "THREAD_POST_RATE_LIMIT", 30)
    window = getattr(settings, "THREAD_POST_RATE_WINDOW_SECONDS", 60)
    if not allow_action(author, "thread_post", limit=limit, window_seconds=window):
        raise InvalidState(_("You are posting too quickly; slow down."))
    share = _validate_share(author, share_activity, share_place, share_event)
    result = get_message_policy().process(author=author, thread=activity.thread, body=body)
    if result.allowed:
        result_body = result.body
    elif (allow_empty or share) and not (body or "").strip():
        # An attachment-only or share-only message: an empty body is fine. Any OTHER
        # policy rejection (too long, or a future CSAR content block) still applies.
        result_body = ""
    else:
        raise InvalidState(result.reason or _("Message rejected."))
    parent = _validate_reply_to(activity, reply_to)
    post = Post.objects.create(
        thread=activity.thread, author=author, body=result_body, reply_to=parent, **share
    )
    # Normalize updated_at == created_at on a fresh post (auto_now_add and auto_now fire as two
    # separate now() calls, so they'd otherwise differ by microseconds and falsely read as
    # "edited"). After this, any real edit makes updated_at strictly greater. One cheap write.
    Post.objects.filter(pk=post.pk).update(updated_at=post.created_at)
    post.updated_at = post.created_at
    if ping:
        _ping_mentioned(author, activity, post)
    transaction.on_commit(lambda: broadcast_post(post))
    return post


def _ping_mentioned(author, activity, post) -> None:
    """Opt-in fan-out: notify PEER members @mentioned in ``post`` — never the author, never a
    blocked pair, never a guardian (resolve_mentions already excludes guardians by resolving
    against voting_members). notify() still honours each recipient's per-kind mute, so MENTION
    stays a fully user-controllable signal. Runs inside the post's transaction so a rolled-back
    post pings nobody."""
    from apps.notifications.models import Notification
    from apps.safety.services import blocked_user_ids

    # @mentions are an ACTIVITY-thread affordance only. On a standing GROUP thread we deliberately
    # do NOT resolve mentions (no @autocomplete, no ping), so the active-member set is never
    # enumerable by name — consistent with the roster-less-for-minors rule. Bylines remain for
    # reporting; this just removes the name-resolution/ping surface entirely on group threads.
    if isinstance(activity, Group):
        return
    mentioned = resolve_mentions(activity, post.body, exclude_user=author)
    if not mentioned:
        return
    blocked = blocked_user_ids(author)
    who = author.display_name or author.username
    title = _("%(who)s mentioned you") % {"who": who}
    url = f"/activities/{activity.id}/#post-{post.id}"
    for user in mentioned:
        if user.id in blocked:
            continue
        _notify(user, Notification.Kind.MENTION, title, body=post.body[:140], url=url)


def _validate_share(author, share_activity, share_place, share_event) -> dict:
    """Validate an optional share target at WRITE time (W6). At most ONE target; the
    author must be able to see it through the same gates as everywhere else:

    - an activity: same cohort as the author (can_see_activity) and not hidden — since a
      thread's readers share the author's cohort, a share can never bridge cohorts;
    - a place: in ``public_places()`` (the F25 chokepoint — no pending-place disclosure).
      A venue card is the privacy-safe "send a location": never a person's coordinates;
    - an event: its venue (if any) must be public (same F25 rule).

    Accepts model instances or pks. Returns kwargs for Post.objects.create. Render-time
    re-gating happens separately in ``share_card`` (a target can become hidden later)."""
    chosen = [x for x in (share_activity, share_place, share_event) if x is not None]
    if not chosen:
        return {}
    if len(chosen) > 1:
        raise InvalidState(_("Share one thing at a time."))
    from apps.events.models import Event
    from apps.places.models import Place
    from apps.places.services import public_places

    if share_activity is not None:
        target = (
            share_activity
            if isinstance(share_activity, Activity)
            else Activity.objects.filter(pk=_safe_pk(share_activity)).first()
        )
        if (
            target is None
            or target.is_hidden
            # Sharing a cancelled meetup invites people to nothing — reject at write,
            # mirroring the read side (share_card degrades a later-cancelled target).
            or target.status == Activity.Status.CANCELLED
            or not can_see_activity(author, target)
        ):
            raise InvalidState(_("You can't share that."))
        return {"shared_activity": target}
    if share_place is not None:
        pk = share_place.pk if isinstance(share_place, Place) else _safe_pk(share_place)
        target = public_places().filter(pk=pk).first()
        if target is None:
            raise InvalidState(_("You can't share that."))
        return {"shared_place": target}
    pk = share_event.pk if isinstance(share_event, Event) else _safe_pk(share_event)
    target = (
        Event.objects.select_related("place")
        .filter(pk=pk)
        .filter(Q(place__isnull=True) | Q(place_id__in=public_places().values("id")))
        .first()
    )
    if target is None:
        raise InvalidState(_("You can't share that."))
    return {"shared_event": target}


def _safe_pk(value):
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def attach_share_cards(posts) -> None:
    """Batch-derive ``post.share`` for a page of posts (W6), re-gating each target's
    visibility NOW (not when it was shared): a hidden or CANCELLED activity, an
    unpublished place or a vanished target render as an honest "unavailable" stub.
    Thread readers share the author's cohort by construction, so no per-viewer cohort
    check is needed. ONE public-places query for the whole page (no per-post N+1) —
    callers must have select_related the shared_* FKs."""
    from apps.places.services import public_places

    posts = list(posts)
    place_ids = set()
    for p in posts:
        if p.shared_place_id:
            place_ids.add(p.shared_place_id)
        if p.shared_event_id and p.shared_event is not None and p.shared_event.place_id:
            place_ids.add(p.shared_event.place_id)
    public_ids = (
        set(public_places().filter(pk__in=place_ids).values_list("id", flat=True))
        if place_ids
        else set()
    )
    for p in posts:
        p.share = _derive_share_card(p, public_ids)


def share_card(post):
    """Single-post variant of ``attach_share_cards`` (used by the API serializer when a
    post arrives without the batch pass). Same re-gating; one query at most."""
    if not (post.shared_activity_id or post.shared_place_id or post.shared_event_id):
        return None
    attach_share_cards([post])
    return post.share


def _derive_share_card(post, public_place_ids: set):
    if post.shared_activity_id:
        a = post.shared_activity
        if a is None or a.is_hidden or a.status == Activity.Status.CANCELLED:
            return {"kind": "gone"}
        return {"kind": "activity", "obj": a}
    if post.shared_place_id:
        if post.shared_place is None or post.shared_place_id not in public_place_ids:
            return {"kind": "gone"}
        return {"kind": "place", "obj": post.shared_place}
    if post.shared_event_id:
        e = post.shared_event
        if e is None or (e.place_id and e.place_id not in public_place_ids):
            return {"kind": "gone"}
        return {"kind": "event", "obj": e}
    return None


def _validate_reply_to(activity, reply_to):
    """Resolve an optional reply target to a TOP-LEVEL ancestor Post in the same thread, or
    None. Re-parenting (parent.reply_to or parent) enforces the one-level depth cap in the
    service, never the schema. Refused: a hidden parent (no replying to a removed post), a
    PINNED ANNOUNCEMENT (it isn't part of the reply tree — a reply to it would be orphaned out
    of thread_page), a wrong-thread parent, and a non-integer id (raised as a domain error, not
    an uncaught ValueError that would tear down the WebSocket consumer)."""
    if reply_to is None:
        return None
    if isinstance(reply_to, Post):
        parent = reply_to
    else:
        try:
            parent = Post.objects.filter(pk=int(reply_to)).first()
        except (TypeError, ValueError) as exc:
            raise InvalidState(_("You can't reply to that message.")) from exc
    if (
        parent is None
        or parent.thread_id != activity.thread.id
        or parent.is_hidden
        or parent.is_announcement
    ):
        raise InvalidState(_("You can't reply to that message."))
    return parent.reply_to if parent.reply_to_id else parent


def post_to_thread_realtime(author, activity, body: str, *, reply_to_id=None) -> Post:
    """Thin wrapper the WebSocket consumer calls so the socket write goes through the EXACT
    same gate as the form/API — gate divergence between surfaces is structurally impossible."""
    return post_to_thread(author, activity, body, reply_to=reply_to_id)


def can_read_thread(user, activity) -> bool:
    """The single read/write membership gate for a thread, used by the web view, the bounded
    history read, AND the WebSocket consumer (connect + per-receive + per-delivery re-auth).
    Folds the old chat.can_access_thread logic so all surfaces agree on who may see a thread.

    ``activity`` is the thread OWNER OBJECT — an Activity or a Group (duck-typed: both expose
    ``is_hidden`` / ``cohort`` / ``memberships`` / ``owner_id`` / ``owner``). Step 3 (the cohort
    wall) is the SINGLE fail-closed read gate; there is no carve-out, so an aged-out or cross-cohort
    user is rejected at read time even if an eviction was missed."""
    if not user or not getattr(user, "is_authenticated", False) or not user.is_active:
        return False
    if getattr(activity, "is_hidden", False):
        return False
    if user.cohort != activity.cohort:
        return False
    if not can_participate(user):
        return False
    if not thread_members(activity).filter(user=user).exists():
        return False
    from apps.safety.services import is_blocked

    if user.id != activity.owner_id and is_blocked(user, activity.owner):
        return False
    return True


def thread_page(activity, *, before=None, limit=None):
    """A bounded, keyset-paginated window of TOP-LEVEL posts (reply_to IS NULL) for an
    activity thread, newest-window-first then returned oldest->newest for display, each with
    its non-hidden replies prefetched (one extra query, no N+1, no recursive CTE). Replaces
    the old unbounded thread load. The CALLER MUST gate on can_read_thread first so the
    ``before`` cursor can never leak across the membership wall. Returns
    (posts_oldest_first, has_older, older_cursor_id)."""
    from django.db.models import Prefetch

    limit = limit or getattr(settings, "SOCIAL_THREAD_POST_LIMIT", 100)
    replies_qs = Post.objects.filter(is_hidden=False).select_related(
        "author", "reply_to__author", "shared_activity", "shared_place", "shared_event"
    )
    top = (
        activity.thread.posts.filter(is_hidden=False, is_announcement=False, reply_to__isnull=True)
        .select_related("author", "shared_activity", "shared_place", "shared_event")
        .prefetch_related(Prefetch("replies", queryset=replies_qs.order_by("created_at")))
        .order_by("-created_at")
    )
    if before:
        try:
            before_id = int(before)
        except (TypeError, ValueError):
            before_id = None  # a malformed cursor degrades to the first page, never a 500
        anchor = (
            Post.objects.filter(pk=before_id, thread=activity.thread).first()
            if before_id is not None
            else None
        )
        if anchor is not None:
            # Keyset on (created_at, id) — strictly older than the anchor, stable on ties.
            top = top.filter(
                Q(created_at__lt=anchor.created_at)
                | Q(created_at=anchor.created_at, id__lt=anchor.id)
            )
    window = list(top[: limit + 1])
    has_older = len(window) > limit
    window = window[:limit]
    older_cursor_id = window[-1].id if (has_older and window) else None
    window.reverse()  # oldest -> newest for display
    all_posts = []
    for tp in window:
        tp.is_edited = _is_edited(tp)
        all_posts.append(tp)
        for reply in tp.replies.all():  # prefetched, already filtered + ordered
            reply.is_edited = _is_edited(reply)
            reply.snippet = reply_snippet(reply)
            all_posts.append(reply)
    attach_share_cards(all_posts)  # ONE public-places query for the whole page
    return window, has_older, older_cursor_id


def _is_edited(post) -> bool:
    # post_to_thread normalizes updated_at == created_at on a fresh post, so a strict
    # inequality means a genuine later edit (edit_post bumps updated_at via auto_now).
    if not post.updated_at or not post.created_at:
        return False
    return post.updated_at > post.created_at


def reply_snippet(post, *, length=120):
    """The 'Replying to <author>: <text>' snippet, ALWAYS derived from the CURRENT parent at
    read time (never a stored copy): a hidden/removed parent yields a neutral placeholder, so
    an edited or moderated parent can't resurface stale text inside its replies."""
    parent = post.reply_to
    if parent is None:
        return None
    author = parent.author.display_name or parent.author.username
    if parent.is_hidden:
        return {"author": author, "text": str(_("(message removed)")), "pk": parent.id}
    text = (parent.body or "").strip().replace("\n", " ")
    if len(text) > length:
        text = text[: length - 1].rstrip() + "…"
    return {"author": author, "text": text, "pk": parent.id}


@transaction.atomic
def edit_post(author, post, body: str) -> Post:
    """Author-only in-place edit. Same participation/status gate as posting; refuses a
    moderator-hidden post (no moderation evasion) and an announcement (the owner re-announces
    instead). Because reply snippets are render-derived, an edit here automatically updates
    every reply that quotes this post on its next read. The 'edited' marker is derived from
    updated_at != created_at — no edit-count, no revision table."""
    from apps.chat.policy import get_message_policy

    if post.author_id != author.id:
        raise NotEligible(_("You can only edit your own messages."))
    if post.is_hidden or post.is_announcement:
        raise InvalidState(_("This message can't be edited."))
    activity = post.thread.owner_object
    if not thread_members(activity).filter(user=author).exists():
        raise NotAMember(_("Only current members can edit a message."))
    if not can_participate(author):
        raise NotEligible(_("Editing requires verified, consented participation."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    result = get_message_policy().process(author=author, thread=post.thread, body=body)
    if not result.allowed:
        raise InvalidState(result.reason or _("Message rejected."))
    post.body = result.body
    post.save(update_fields=["body", "updated_at"])
    transaction.on_commit(lambda: broadcast_post(post, edited=True))
    return post


@transaction.atomic
def delete_own_post(author, post) -> Post:
    """Author soft-delete: flag the post hidden so it drops from member reads but the row is
    RETAINED for audit/appeal (like a moderator REMOVE). Refuses a post already moderator-
    hidden (no clobbering a moderation record). Because snippets are render-derived, a
    self-deleted parent's quote drops from its replies on next read automatically. GDPR
    erasure (apps/ops) stays the only hard-delete path."""
    from apps.safety.services import record_audit

    if post.author_id != author.id:
        raise NotEligible(_("You can only delete your own messages."))
    if post.is_hidden:
        return post  # idempotent; never un-hides a moderation action
    post.is_hidden = True
    post.save(update_fields=["is_hidden", "updated_at"])
    record_audit("post.self_deleted", actor=author, target=post)
    return post


def broadcast_post(post, *, edited=False) -> None:
    """Fan a committed Post out to its thread's WebSocket group as PURE live delivery (the
    durable record already exists; this only saves connected members a reload). Called via
    transaction.on_commit, so a rolled-back write broadcasts nothing. Per-delivery re-auth in
    the consumer drops blocked/cohort-changed/erased members, so this need not filter. Wrapped
    to a graceful no-op when there is no working channel layer (single-process InMemory across
    processes) — the no-JS surface already has the content on reload."""
    try:
        from asgiref.sync import async_to_sync
        from channels.layers import get_channel_layer

        layer = get_channel_layer()
        if layer is None:
            return
        snippet = reply_snippet(post)
        author = post.author.display_name or post.author.username
        # Live share-card summary so a share-only post never arrives as a blank bubble.
        # Title only (text) — the client builds the href from kind + integer id, never
        # from this string.
        share = share_card(post)
        share_payload = None
        if share is not None:
            if share["kind"] == "gone":
                share_payload = {"kind": "gone"}
            else:
                target = share["obj"]
                share_payload = {
                    "kind": share["kind"],
                    "id": target.pk,
                    "title": getattr(target, "title", "") or getattr(target, "name", ""),
                }
        payload = {
            "id": post.id,
            "author": author,
            "author_id": post.author_id,
            "body": post.body,
            "is_announcement": post.is_announcement,
            "reply_to": post.reply_to_id,
            "reply_snippet": snippet,
            "share": share_payload,
            "edited": edited
            or (post.updated_at and post.created_at and post.updated_at > post.created_at),
            "created_at": post.created_at.isoformat() if post.created_at else None,
        }
        async_to_sync(layer.group_send)(
            f"chat_{post.thread_id}", {"type": "chat.message", "message": payload}
        )
    except Exception:  # noqa: BLE001 — live delivery is best-effort; never break the write
        pass


# --- thread reactions (anonymous, COUNTLESS, no who-list) -----------------------------------

# A fixed, NON-extensible ack set — never user-supplied custom emoji (a custom-emoji economy is
# an engagement/vanity surface). Overridable via settings only by an operator.
DEFAULT_REACTION_EMOJIS = ["👍", "❤️", "🎉", "👏", "🙏"]


def allowed_reactions() -> list:
    return list(getattr(settings, "THREAD_REACTION_EMOJIS", DEFAULT_REACTION_EMOJIS))


@transaction.atomic
def toggle_reaction(user, post, emoji) -> bool:
    """Add or remove the user's OWN emoji reaction on a thread post. Enforces the SAME write
    gate as post_to_thread (membership, not-a-guardian, consent, not-blocked-vs-owner, activity
    not hidden/cancelled) plus a fixed-emoji-set and not-a-hidden-post check, so the reaction
    surface can never become a weaker side door than posting. Returns True if now reacted, False
    if removed. Never exposes a count or a who-list anywhere."""
    from apps.safety.services import allow_action, is_blocked

    from .models import PostReaction

    if emoji not in allowed_reactions():
        raise InvalidState(_("That reaction isn't available."))
    if post.is_hidden:
        raise InvalidState(_("You can't react to that message."))
    activity = post.thread.owner_object
    if getattr(activity, "is_hidden", False):
        raise InvalidState(_("This activity is no longer available."))
    membership = thread_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can react."))
    if membership.role == Membership.Role.GUARDIAN:
        # Guardians are read-only supervisors (like post_to_thread) — reacting is a write.
        raise NotEligible(_("Guardians accompany activities as read-only supervisors."))
    if not can_participate(user):
        raise NotEligible(_("Reacting requires verified, consented participation."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    if user.id != activity.owner_id and is_blocked(user, activity.owner):
        # Mirror post_to_thread (a block leaves Membership intact, so it must be re-checked here);
        # otherwise a blocked-vs-owner member's emoji would surface on the owner's own posts.
        raise InvalidState(_("This activity is no longer available."))
    limit = getattr(settings, "THREAD_REACT_RATE_LIMIT", 60)
    window = getattr(settings, "THREAD_REACT_RATE_WINDOW_SECONDS", 60)
    if not allow_action(user, "thread_react", limit=limit, window_seconds=window):
        raise InvalidState(_("You are reacting too quickly; slow down."))
    existing = PostReaction.objects.filter(post=post, user=user, emoji=emoji).first()
    if existing is not None:
        existing.delete()
        return False
    # get_or_create swallows a concurrent duplicate (a fast double-tap) as a benign no-op via its
    # own savepoint, rather than poisoning this atomic block with an unhandled IntegrityError 500.
    # (Don't bind the throwaway to ``_`` — that's the module-level gettext alias.)
    _obj, created = PostReaction.objects.get_or_create(post=post, user=user, emoji=emoji)
    return created


def post_reaction_emojis(post) -> list:
    """The DISTINCT emojis present on a post, in the fixed display order — NO count, NO who."""
    present = set(post.reactions.values_list("emoji", flat=True))
    return [e for e in allowed_reactions() if e in present]


# --- @mentions (tag-not-ping by default; an explicit ping is a calm opt-in) -----------------

# A mention is "@" + a username. The lookbehind refuses a "@" glued to a preceding word char,
# so an email address (alice@example.com) never reads as a mention/ping. Username charset mirrors
# the model's max length; we resolve case-insensitively against ACTUAL peer members, so a token
# that doesn't name a current peer is left as plain text (never a fake highlight, never a ping).
MENTION_RE = re.compile(r"(?<![\w@])@([\w.\-]{1,150})")


def mention_roster(activity) -> dict:
    """Lowercased-username -> peer member User for this activity. PEERS ONLY — a supervisory
    guardian is never mentionable (no adult is pulled into a children's thread by a tag), and a
    mention can never reach outside the activity's own roster. Compute ONCE per request and pass
    into highlight_mentions for each post (the rendering loop must not re-query per post)."""
    out = {}
    for m in voting_members(activity).select_related("user"):
        out[m.user.username.lower()] = m.user
    return out


def resolve_mentions(activity, body, *, exclude_user=None) -> list:
    """Distinct peer members named by '@username' in ``body`` (order of first appearance), minus
    ``exclude_user``. Resolution is against the live roster, so the set self-narrows as people
    leave — a mention is only ever a current peer, never a stranger or a guardian."""
    if not body:
        return []
    roster = mention_roster(activity)
    seen, result = set(), []
    for token in MENTION_RE.findall(body):
        user = roster.get(token.lower())
        if user is None or user.id in seen:
            continue
        if exclude_user is not None and user.id == exclude_user.id:
            continue
        seen.add(user.id)
        result.append(user)
    return result


def highlight_mentions(body, roster):
    """Render a thread body to SAFE HTML: HTML-escaped, newlines -> <br>, and every '@username'
    that names a CURRENT peer member (a key in ``roster`` from ``mention_roster``) wrapped in
    <span class="mention">. Escaping happens BEFORE any markup is inserted, so a hostile body can
    never inject HTML; only the literal mention spans we add are trusted. A token that doesn't
    resolve to a peer stays plain escaped text."""
    from django.utils.html import escape
    from django.utils.safestring import mark_safe

    if not body:
        return mark_safe("")

    def repl(match):
        token = match.group(1)
        if token.lower() in roster:
            return f'<span class="mention">@{escape(token)}</span>'
        return escape(match.group(0))  # not a real member — leave as escaped plain text

    # Escape the gaps between mentions ourselves so the whole string is safe, then mark_safe.
    pieces, last = [], 0
    for m in MENTION_RE.finditer(body):
        pieces.append(escape(body[last : m.start()]))
        pieces.append(repl(m))
        last = m.end()
    pieces.append(escape(body[last:]))
    # Normalise CRLF/CR before turning newlines into <br> so no stray \r survives (parity with
    # the |linebreaksbr fallback). All segments are already escaped, so this only adds our <br>.
    html = "".join(pieces).replace("\r\n", "\n").replace("\r", "\n").replace("\n", "<br>")
    return mark_safe(html)


def reactions_for_posts(posts, viewer) -> dict:
    """Batch (no N+1): post_id -> {"present": [distinct emojis, no count], "mine": {viewer's own}}.
    Used by the thread view to render reaction chips + highlight the viewer's own toggles."""
    from .models import PostReaction

    ids = [p.id for p in posts]
    out = {pid: {"present": set(), "mine": set()} for pid in ids}
    if not ids:
        return out
    for r in PostReaction.objects.filter(post_id__in=ids).values("post_id", "user_id", "emoji"):
        slot = out[r["post_id"]]
        slot["present"].add(r["emoji"])
        if r["user_id"] == viewer.id:
            slot["mine"].add(r["emoji"])
    order = allowed_reactions()
    return {
        pid: {
            "present": [e for e in order if e in v["present"]],  # ordered distinct, no count
            "mine": v["mine"],
        }
        for pid, v in out.items()
    }


@transaction.atomic
def post_announcement(owner, activity, body: str) -> Post:
    """Owner-only pinned broadcast: a must-read logistics post that surfaces above the
    thread and fires one notification to every current member. Same cohort/consent gate
    as an ordinary post; only the owner may use it."""
    from apps.notifications.models import Notification
    from apps.safety.services import blocked_user_ids

    if not is_organizer(owner, activity):
        raise NotAMember(_("Only the organiser can post an announcement."))
    if not can_participate(owner):
        raise NotEligible(_("Posting requires verified, consented participation."))
    # Re-check current membership (mirrors post_to_thread's first gate): an owner who was EVICTED
    # (e.g. a cohort change flipped their membership to REMOVED) can no longer broadcast into a
    # group they no longer belong to, even though they still nominally own the row. Deliberately
    # NOT a cohort check — the staff curator of a MINOR group is an ADULT and MUST still announce.
    if thread_members(activity).filter(user=owner).first() is None:
        raise NotAMember(_("Only a current member can post an announcement."))
    if is_thread_frozen(activity):
        raise InvalidState(_("This conversation is closed."))
    post = Post.objects.create(
        thread=activity.thread, author=owner, body=body, is_announcement=True
    )
    body_preview = body.strip()
    if len(body_preview) > 140:
        body_preview = body_preview[:139].rstrip() + "…"
    # An announcement on a GROUP is the owner/staff broadcast channel (and, for a minor group, the
    # ONLY write into its announcement-only thread). It uses the mutable GROUP_ANNOUNCEMENT kind and
    # a /groups/ link; an activity announcement keeps its existing kind + link unchanged.
    is_group = isinstance(activity, Group)
    kind = Notification.Kind.GROUP_ANNOUNCEMENT if is_group else "announcement"
    url = f"/groups/{activity.id}/" if is_group else f"/api/social/activities/{activity.id}/"
    title = (_("Group announcement: %(title)s") if is_group else _("Announcement: %(title)s")) % {
        "title": activity.title
    }
    # Exclude blocked pairs from the fan-out — without this a member who blocked (or was
    # blocked by) the owner kept receiving the owner's announcements (the pre-existing gap
    # that mark_arrived already closes). The live group_send is filtered at delivery by the
    # consumer's can_read_thread re-auth, which also drops blocked members. The fan-out targets
    # current MEMBERS of the (cohort-pinned) thread and does not re-check each recipient's live
    # cohort/eligibility — matching every other notify() fan-out; can_read_thread re-gates at READ
    # time, so a stale recipient at most sees a notification title, never thread content (LOW).
    blocked = blocked_user_ids(owner)
    recipients = (
        thread_members(activity)
        .exclude(user_id=owner.id)
        .exclude(user_id__in=blocked)
        .select_related("user")
    )
    for membership in recipients:
        _notify(membership.user, kind, title, body=body_preview, url=url)
    transaction.on_commit(lambda: broadcast_post(post))
    return post


# --- F30: minor-group "ask the organiser" relief valve ---------------------------------


@transaction.atomic
def group_ask_organiser(member, group, prompt_choice) -> bool:
    """Inbound voice for a muted minor group, with NO adult↔minor private-contact path.

    A minor-cohort Group thread is announcement-only (peers read, only the staff curator
    broadcasts), so without this it is a one-way board. This lets a current MEMBER send ONE
    of a small FIXED set of prompts (``GroupQuestionPrompt`` — never free text) to the
    group's STAFF organiser, and ONLY to that organiser:

    - **Writes NO Post** — the question is never member-visible and never adds to the
      active-poster enumeration surface the announcement-only rule exists to collapse.
    - **Notifies only ``group.owner``** — never ``thread_members``, never a fan-out.
    - The organiser's only reply channel is a group-wide ``post_announcement`` — there is
      deliberately NO private adult→minor reply, so any answer is public to the whole group.
      The web/DRF surfaces state this asymmetry plainly so a child is never misled.

    Rate-limited (anti-flood of the organiser) and audited (the choice key only — there is
    no free text to leak). Returns ``True`` if a notification reached the organiser, ``False``
    if it was suppressed because the organiser muted this (mutable) kind — so the caller can be
    honest with the child instead of always claiming delivery."""
    from apps.safety.services import allow_action, is_blocked, record_audit

    # Minor groups only. An adult-group member just posts in the thread (not announcement-only).
    if not isinstance(group, Group) or group.cohort not in (Cohort.CHILD, Cohort.TEEN):
        raise NotEligible(_("Asking the organiser is only for under-18 groups."))
    if group.status != Group.Status.ACTIVE or group.is_hidden:
        raise InvalidState(_("This group isn't active."))
    # The caller must be a current MEMBER — role MEMBER, not OWNER (the staff curator holds an
    # OWNER-role membership and must not be able to "ask themselves"; they answer via announce).
    if not group.memberships.filter(
        user=member,
        role=GroupMembership.Role.MEMBER,
        state=GroupMembership.State.MEMBER,
    ).exists():
        raise NotAMember(_("Join the group to ask its organiser a question."))
    if not can_participate(member):
        raise NotEligible(_("Verified, consented participation is required."))
    # Defence-in-depth: the only recipient must be a vetted STAFF curator. Minor-group
    # creation forces both (is_staff_curated + a staff owner) and there is no group
    # ownership-transfer service, so this holds structurally — but re-assert it here so a
    # legacy/misconfigured row can NEVER route a minor's message to a non-staff adult.
    owner = group.owner
    if not group.is_staff_curated or not owner.is_staff:
        raise NotEligible(_("This group has no staff organiser to receive questions."))
    # Block wall (defence-in-depth, mirroring join_group / post_to_thread / post_announcement):
    # the gate lives in the service so it holds identically on every surface, not only where the
    # caller pre-filtered via visible_groups. If either party blocked the other, the organiser's
    # only reply channel (post_announcement) already excludes the pair — so accepting the question
    # would be a dead-end. Refuse before spending rate budget or writing an audit row.
    if member.id != owner.id and is_blocked(member, owner):
        raise NotEligible(_("This group is no longer available."))
    # Fixed enum ONLY — no free text (closes the grooming / PII-disclosure vector).
    try:
        prompt = GroupQuestionPrompt(prompt_choice)
    except ValueError as exc:
        raise InvalidState(_("Pick one of the listed questions.")) from exc
    # Rate-limit in a dedicated per-user bucket so the organiser can't be flooded.
    limit = getattr(settings, "GROUP_QUESTION_RATE_LIMIT", 6)
    window = getattr(settings, "GROUP_QUESTION_RATE_WINDOW_SECONDS", 3600)
    if not allow_action(member, "group_question", limit=limit, window_seconds=window):
        raise InvalidState(_("You've sent a few questions already — please wait a while."))
    # Audit INSIDE the transaction (it takes a row lock). Record the choice KEY only.
    record_audit("group.question_asked", actor=member, target=group, prompt=prompt.value)
    # Notify ONLY the staff organiser. Never thread_members, never a Post, never member-visible.
    from apps.notifications.models import Notification

    title = _("New question in %(group)s") % {"group": group.title}
    body = _("A member asks: %(q)s") % {"q": str(prompt.label)}
    notice = _notify(
        owner,
        Notification.Kind.GROUP_QUESTION,
        str(title),
        body=str(body),
        url=f"/groups/{group.id}/",
    )
    # None when the organiser muted this (mutable) kind — report honest non-delivery upward.
    return notice is not None


# --- F20: RSVP attendance intent -------------------------------------------------------


@transaction.atomic
def set_attendance_intent(user, activity, intent) -> Membership:
    """A current member flips their transient go/no-go for THIS activity. No notification,
    no audit, no cross-activity history (that would be behavioural tracking)."""
    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can RSVP."))
    if intent not in Membership.AttendanceIntent.values:
        raise InvalidState(_("Invalid attendance choice."))
    membership.attendance_intent = intent
    membership.save(update_fields=["attendance_intent", "updated_at"])
    _maybe_confirm_meetup(activity)  # F1: one-shot "it's on" notice when GOING crosses min_to_go
    return membership


def _maybe_confirm_meetup(activity) -> None:
    """F1 Quorum-go: if a min_to_go threshold is set and the LIVE GOING count has now reached it,
    latch the one-shot ``go_confirmed_at`` and fan out a single MEETUP_CONFIRMED notice. The latch
    ONLY dedups the notification (so a wobbling count never spams) — it never feeds the displayed
    state. Locks the activity row so two concurrent RSVPs can't double-fire the notice. Must be
    called inside set_attendance_intent's transaction (so the just-saved RSVP is counted)."""
    if activity.min_to_go is None:
        return
    locked = Activity.objects.select_for_update().get(pk=activity.pk)
    if locked.min_to_go is None or locked.go_confirmed_at is not None:
        return
    if locked.status != Activity.Status.OPEN:
        # A cancelled/completed meetup can never become "on" — never latch or fan out an
        # "it's happening" notice for a frozen activity (checked on the locked row, race-safe).
        return
    going = (
        voting_members(locked).filter(attendance_intent=Membership.AttendanceIntent.GOING).count()
    )
    if going < locked.min_to_go:
        return
    locked.go_confirmed_at = timezone.now()
    locked.save(update_fields=["go_confirmed_at", "updated_at"])
    activity.go_confirmed_at = locked.go_confirmed_at  # keep the caller's instance fresh
    # Notify synchronously in-txn (like mark_arrived / post_announcement create their notices): a
    # rolled-back RSVP rolls back the latch AND the notices together, so a phantom "it's on" can
    # never outlive a failed RSVP. The activity-row lock above makes the one-shot fan-out atomic.
    _notify_meetup_confirmed(locked)


def _notify_meetup_confirmed(activity) -> None:
    """Fan a single 'the meetup is on' notice to current members (minus blocked pairs), once the
    quorum is first reached. Mirrors post_announcement's blocked-pair exclusion."""
    from apps.notifications.models import Notification
    from apps.safety.services import blocked_user_ids

    blocked = blocked_user_ids(activity.owner)
    title = _("A meetup is on")
    body = _("“%(title)s” has enough people going — it's happening.") % {"title": activity.title}
    url = f"/api/social/activities/{activity.id}/"
    for membership in current_members(activity).exclude(user_id__in=blocked).select_related("user"):
        _notify(membership.user, Notification.Kind.MEETUP_CONFIRMED, title, body=body, url=url)


def attendance_summary(activity) -> dict:
    """Per-activity go count for the participants (peers, excluding supervisory guardians), plus the
    F1 Quorum-go state. A live snapshot shown only to members — never stored, never aggregated
    per-user. ``met_minimum`` / ``remaining_needed`` are derived LIVE from the current GOING count
    (NOT from the go_confirmed_at latch), so the chip can never claim "on" after the count drops."""
    members = voting_members(activity)
    going = members.filter(attendance_intent=Membership.AttendanceIntent.GOING).count()
    # The forward-looking quorum state ("it's on / needs N more") is meaningful only for a LIVE
    # (OPEN) meetup — a cancelled or completed activity has no such state, and showing one would be
    # a lying chip. The configured min_to_go still lives on the model + ActivitySerializer; this
    # dict is the LIVE snapshot, so its quorum keys go None once the meetup is no longer open.
    live = activity.min_to_go is not None and activity.status == Activity.Status.OPEN
    min_to_go = activity.min_to_go if live else None
    return {
        "going": going,
        "total": members.count(),
        "min_to_go": min_to_go,
        "met_minimum": (going >= min_to_go) if live else None,
        "remaining_needed": max(min_to_go - going, 0) if live else None,
    }


@transaction.atomic
def set_met_confirmed(user, activity, confirmed: bool = True) -> Membership:
    """A participant privately confirms (or undoes) that a finished meetup actually happened
    (F22). Allowed only once the activity is COMPLETED. No notification, no audit, no
    cross-activity trace — it is a single per-activity boolean, never a judgement of a person."""
    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can confirm a meetup."))
    if membership.role == Membership.Role.GUARDIAN:
        raise NotEligible(_("Guardians accompany activities as read-only supervisors."))
    if activity.status != Activity.Status.COMPLETED:
        raise InvalidState(_("You can only confirm a meetup after it has finished."))
    if confirmed and membership.met_confirmed_at is not None:
        return membership  # idempotent: a second tap changes nothing
    membership.met_confirmed_at = timezone.now() if confirmed else None
    membership.save(update_fields=["met_confirmed_at", "updated_at"])
    return membership


def met_confirmation_summary(activity) -> dict:
    """Per-activity 'did we meet up?' count over the participants (excludes guardians). A live
    snapshot shown only to members — never stored, never rolled up per-user or cross-activity."""
    members = voting_members(activity)
    return {
        "confirmed": members.filter(met_confirmed_at__isnull=False).count(),
        "total": members.count(),
    }


# --- F35: extractive "catch up" thread digest -----------------------------------------


def thread_digest(activity, viewer) -> dict:
    """A deterministic, extractive recap of an activity thread (F35): the latest announcements, a
    few logistical posts (conservative keyword match) and the most-recent posts. Pure read; the
    SAME content for every member (no per-user 'last read' state — that would be behavioural
    tracking). Bounded by DIGEST_SCAN_LIMIT.

    The numeric summary (going / total / member_count) is COHORT-GATED by ``viewer``: it is returned
    only to an ADULT viewer, and is None for CHILD/TEEN viewers (and anyone else). ``member_count``
    AND ``total`` both reveal the roster size, so the headline 'minors never see a member count'
    rule suppresses the whole numeric block for minors — the digest then carries only the textual
    content. ``viewer`` is REQUIRED so a caller can never accidentally emit an ungated count."""
    posts = activity.thread.posts
    announcements = list(
        posts.filter(is_hidden=False, is_announcement=True)
        .select_related("author")
        .order_by("-created_at")[:DIGEST_MAX_ANNOUNCEMENTS]
    )
    scanned = list(
        posts.filter(is_hidden=False, is_announcement=False)
        .select_related("author")
        .order_by("-created_at")[:DIGEST_SCAN_LIMIT]
    )
    recent = scanned[:DIGEST_RECENT_POSTS]
    recent_ids = {p.id for p in recent}
    logistical = [p for p in scanned if p.id not in recent_ids and _LOGISTICAL_RE.search(p.body)][
        :DIGEST_LOGISTICAL_POSTS
    ]
    # Counts are an ADULT-only surface (the count-leak fix, platform-wide). Skip the queries
    # entirely for a minor/anon viewer so nothing leaks and nothing is wasted.
    going = total = member_count = None
    if getattr(viewer, "cohort", None) == Cohort.ADULT:
        att = attendance_summary(activity)
        going, total = att["going"], att["total"]
        member_count = current_members(activity).count()
    return {
        "announcements": announcements,
        "recent": recent,
        "logistical": logistical,
        "going": going,
        "total": total,
        "member_count": member_count,
        "has_content": bool(announcements or recent or logistical),
    }


# --- F36: template-driven activity draft helper ----------------------------------------


# W4-F5: the whitelisted fields a "set up another like this" clone may pre-fill into a fresh create
# form. Deliberately EXCLUDES starts_at (a clone is a NEW occurrence — the organiser picks when) and
# anything membership/state/roster-derived. place + activity_type seed the form too; create_activity
# re-validates ALL of them on submit (cohort re-pinned, place/type re-checked, child-venue +
# category envelope re-applied), so a clone can never escape the create gate.
_CLONE_PREFILL_FIELDS = (
    "title",
    "description",
    "meeting_point",
    "what_to_bring",
    "organizer_note",
    "getting_home_note",
    "fallback_meeting_point",
    "cost_band",
    "difficulty",
    "accessibility_notes",
    "beginners_welcome",
    "capacity",
    "min_to_go",
)


def draft_from_activity(user, source) -> dict:
    """W4-F5: a prefill dict for cloning the organiser's OWN past meetup into a new create form
    ("set up another like this"). Returns {} unless `user` owns or co-organises `source`, so a
    tampered ?from= pointing at someone else's activity injects nothing. PREFILL ONLY — every value
    is re-validated through create_activity's full gate on submit; starts_at is never copied."""
    if not (source.owner_id == user.id or is_organizer(user, source)):
        return {}
    prefill = {f: getattr(source, f) for f in _CLONE_PREFILL_FIELDS}
    prefill["place"] = source.place_id
    prefill["activity_type"] = source.activity_type_id
    return prefill


def draft_activity_text(*, activity_type, place=None, starts_at=None, cohort=None) -> dict:
    """A deterministic (no ML) draft title + description composed from the organiser's OWN
    chosen type/place/time, to seed an empty create form (F36). A CHILD/TEEN organiser also
    gets a short safety reminder. Returns {'title', 'description'}; callers only ever seed
    EMPTY initial, never overwrite what the user typed. gettext fragments are str()-coerced
    before slicing/concatenation (a lazy proxy can't be sliced).

    NB (F18): we deliberately do NOT seed getting_home_note. A template prompt stored verbatim
    would be mirrored onto the CHILD guardian manifest as if it were the organiser's real plan,
    defeating the "see the ACTUAL plan" purpose. The create form's help_text carries that
    guidance instead, so nothing misleading is ever persisted."""
    has_place_name = bool(place and (place.name or "").strip())
    if has_place_name:
        title = str(_("%(type)s at %(place)s") % {"type": activity_type.name, "place": place.name})
    else:
        title = str(activity_type.name)
    title = title[:200]

    where = str(_(" at %(place)s") % {"place": place.name}) if has_place_name else ""
    when = str(_(" on %(when)s") % {"when": f"{starts_at:%a %d %b, %H:%M}"}) if starts_at else ""
    base = str(
        _("A %(type)s meetup%(where)s%(when)s. Add any details below before you post.")
        % {"type": activity_type.name, "where": where, "when": when}
    )
    # Minor signal = cohort, NOT requires_parental_consent (which is UNDER_16-only and would
    # silently skip TEEN organisers).
    if cohort in (Cohort.CHILD, Cohort.TEEN):
        # W7: same reminder, calmer label — guidance, not a "you are a child" badge.
        safety = str(_("Tip: meet in a public place and bring a friend."))
        description = "\n\n".join([base, safety])
    else:
        description = base
    return {"title": title, "description": description}


# --- W2-F27: read-aloud-friendly plain-language meetup brief ---------------------------


def plain_meetup_brief(activity, *, is_member: bool) -> list:
    """A deterministic, template-only "read it aloud" brief of a meetup: short labelled declarative
    sentences from already-stored fields, for screen-reader, low-literacy and elderly users. Returns
    an ORDERED list of (label, sentence) pairs (render as ONE ARIA-landmarked region, no JS). Pure
    read — no ML, no model write, no PII, no new query beyond the passed object.

    Visibility mirrors EXACTLY the fields it draws from, reusing the caller's own ``is_member``
    signal (never re-deriving membership): the cohort-visible chips (cost / difficulty /
    accessibility) always show; the member-only logistics (meeting_point / what_to_bring /
    organizer_note / getting_home_note / first_time_note) show ONLY to a member. Emits NO numeric
    counts to anyone, so it can never leak a roster size to a minor (sidesteps thread_digest's
    count-suppression entirely)."""
    brief: list[tuple[str, str]] = []
    brief.append((str(_("What")), str(activity.title)))
    brief.append(
        (str(_("Activity")), str(_("It is %(type)s.") % {"type": activity.activity_type.name}))
    )
    place_name = (getattr(activity.place, "name", "") or "").strip()
    if place_name:
        brief.append((str(_("Where")), str(_("It is at %(place)s.") % {"place": place_name})))
    when = timezone.localtime(activity.starts_at)
    brief.append(
        (str(_("When")), str(_("It starts on %(when)s.") % {"when": f"{when:%A %d %B at %H:%M}"}))
    )
    # Cohort-visible "what to expect" chips (same audience as the cohort-visible serializer).
    if activity.cost_band != Activity.CostBand.UNSPECIFIED:
        brief.append((str(_("Cost")), str(activity.get_cost_band_display())))
    if activity.difficulty != Activity.Difficulty.UNSPECIFIED:
        brief.append(
            (
                str(_("Difficulty")),
                str(_("It is %(level)s.") % {"level": activity.get_difficulty_display()}),
            )
        )
    if (activity.accessibility_notes or "").strip():
        brief.append((str(_("Access")), activity.accessibility_notes.strip()))
    # Member-only logistics — included ONLY for a member (mirrors the member-gated card).
    if is_member:
        for label, value in (
            (_("Where to meet"), activity.meeting_point),
            (_("What to bring"), activity.what_to_bring),
            (_("A note from the organiser"), activity.organizer_note),
            (_("Getting home"), activity.getting_home_note),
            (_("First time here"), activity.first_time_note),
            (_("Plan B location"), activity.fallback_meeting_point),
        ):
            if (value or "").strip():
                brief.append((str(label), value.strip()))
    return brief


# --- F3: "we're here" arrival ping -----------------------------------------------------


def arrival_window_open(activity) -> bool:
    """Whether arrival may be marked right now: an OPEN activity within the start-relative
    window. Used by the web view to show/hide the button (the service re-checks anyway)."""
    if activity.status != Activity.Status.OPEN:
        return False
    now = timezone.now()
    before = getattr(settings, "ARRIVAL_WINDOW_BEFORE_HOURS", ARRIVAL_WINDOW_BEFORE_HOURS)
    after = getattr(settings, "ARRIVAL_WINDOW_AFTER_HOURS", ARRIVAL_WINDOW_AFTER_HOURS)
    return (
        activity.starts_at - timedelta(hours=before)
        <= now
        <= activity.starts_at + timedelta(hours=after)
    )


@transaction.atomic
def mark_arrived(user, activity) -> Membership:
    """A current member self-declares "I've arrived". Quietly tells the OTHER current
    members (excluding blocked pairs); for a CHILD-cohort member it ALSO tells their active
    guardian(s), so a child is never standing alone. Self-declared only (no on-behalf-of),
    no free text, no location ever, idempotent, and cleared a few hours later by
    expire_arrivals so it never becomes a presence dashboard."""
    from apps.safety.services import blocked_user_ids, record_audit

    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can mark themselves arrived."))
    if not can_participate(user):
        raise NotEligible(_("Marking arrival requires verified, consented participation."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("You can only mark arrival for an active meetup."))
    if not arrival_window_open(activity):
        raise InvalidState(_("Arrival can only be marked around the start time."))
    if membership.arrived_at is not None:
        return membership  # idempotent: a second tap never re-pings the group

    membership.arrived_at = timezone.now()
    membership.save(update_fields=["arrived_at", "updated_at"])

    blocked = blocked_user_ids(user)
    # Server-composed, fixed copy. The only arriver-derived string is display_name — the
    # same low-entropy handle already shown app-wide (members list, thread). NO per-ping
    # note exists, so no unmoderated child-authored text reaches an adult.
    title = _("Someone arrived")
    body = _("%(name)s is at “%(title)s”.") % {
        "name": user.display_name or user.username,
        "title": activity.title,
    }
    url = f"/api/social/activities/{activity.id}/"
    notified: set[int] = set()
    for member in current_members(activity).exclude(user_id=user.id).select_related("user"):
        if member.user_id in blocked:
            continue
        _notify(member.user, "arrival", title, body=body, url=url)
        notified.add(member.user_id)

    # CHILD cohort only (teens self-manage, matching F5/F6). Keyed on an ACTIVE
    # GuardianRelationship — never a loose is_child flag. Each guardian gets at most one ping.
    if user.cohort == Cohort.CHILD:
        for rel in GuardianRelationship.objects.filter(
            ward=user, status=GuardianRelationship.Status.ACTIVE
        ).select_related("guardian"):
            guardian = rel.guardian
            if guardian.id in blocked or guardian.id in notified:
                continue
            _notify(guardian, "arrival", title, body=body, url=url)
            notified.add(guardian.id)

    record_audit("activity.arrived", actor=user, target=activity)
    return membership


# --- W3-F3: "heading home" departure ping (the bookend to the arrival ping) ----------------


def departure_window_open(activity) -> bool:
    """Whether "heading home" may be marked right now: an OPEN activity from its start until a
    few hours after it ends. END-relative on purpose (a departure happens near the end, not the
    start), with a fallback assumed duration when ends_at is open-ended — so the button is live
    exactly when a departing member would tap it, unlike the start-relative arrival window."""
    if activity.status != Activity.Status.OPEN:
        return False
    now = timezone.now()
    if now < activity.starts_at:
        return False  # nobody heads home before the meetup has started
    fallback = getattr(
        settings, "DEPARTURE_FALLBACK_DURATION_HOURS", DEPARTURE_FALLBACK_DURATION_HOURS
    )
    after = getattr(settings, "DEPARTURE_WINDOW_AFTER_HOURS", DEPARTURE_WINDOW_AFTER_HOURS)
    end = activity.ends_at or (activity.starts_at + timedelta(hours=fallback))
    return now <= end + timedelta(hours=after)


@transaction.atomic
def mark_departing(user, activity) -> Membership:
    """A current CHILD member self-declares "I'm heading home" — the departure bookend to
    mark_arrived. Quietly tells ONLY their active guardian(s) (never the other members: a
    departure is a guardian-reassurance signal, not group logistics), keyed strictly on an
    ACTIVE GuardianRelationship, with blocked pairs excluded. Self-declared only (no
    on-behalf-of), no free text, no location ever, idempotent, and cleared a few hours after the
    meetup ends by expire_arrivals so it never becomes a presence record. CHILD-cohort only —
    teens self-manage and only a CHILD has the supervisory guardian fan-out (matching the
    guardian path of mark_arrived)."""
    from django.urls import reverse

    from apps.safety.services import blocked_user_ids, record_audit

    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can mark themselves heading home."))
    if not can_participate(user):
        raise NotEligible(_("This requires verified, consented participation."))
    if user.cohort != Cohort.CHILD:
        raise InvalidState(_("Heading-home pings are for younger members with a guardian."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("You can only do this for an active meetup."))
    if not departure_window_open(activity):
        raise InvalidState(_("You can mark heading home once the meetup has started."))
    if membership.departing_at is not None:
        return membership  # idempotent: a second tap never re-pings the guardian(s)

    membership.departing_at = timezone.now()
    membership.save(update_fields=["departing_at", "updated_at"])

    blocked = blocked_user_ids(user)
    # Server-composed, fixed copy. The only departer-derived string is display_name — the same
    # low-entropy handle shown app-wide. No per-ping note exists, so no unmoderated child-authored
    # text reaches an adult. Mutable ARRIVAL kind reused (a calm convenience cue, not a DSA notice).
    title = _("Someone is heading home")
    body = _("%(name)s is heading home from “%(title)s”.") % {
        "name": user.display_name or user.username,
        "title": activity.title,
    }
    # Link to the guardian's own /wards/ manifest, NOT the activity thread: an adult guardian is
    # cross-cohort to a CHILD activity and is walled out of its thread, so the thread link would
    # be a dead end for them.
    url = reverse("wards")
    notified: set[int] = set()
    for rel in GuardianRelationship.objects.filter(
        ward=user, status=GuardianRelationship.Status.ACTIVE
    ).select_related("guardian"):
        guardian = rel.guardian
        if guardian.id in blocked or guardian.id in notified:
            continue
        _notify(guardian, "arrival", title, body=body, url=url)
        notified.add(guardian.id)

    record_audit("activity.departing", actor=user, target=activity)
    return membership


# W2-F9: forward-only ordering for the transit cue. A request for a status at or below the
# member's current rank is an idempotent no-op (never re-pings, never pings on a clear to
# NONE), so a member emits at most two pings ever: ON_MY_WAY, then RUNNING_LATE.
_TRANSIT_RANK = {
    Membership.TransitStatus.NONE: 0,
    Membership.TransitStatus.ON_MY_WAY: 1,
    Membership.TransitStatus.RUNNING_LATE: 2,
}
# Server-composed, fixed copy keyed on the destination state — the notice is DERIVED from the
# state, never the generic arrival line, and carries no member-authored text. Only these two
# states are user-settable (NONE is reached only by expire_arrivals / leave_activity).
_TRANSIT_COPY = {
    Membership.TransitStatus.ON_MY_WAY: (
        _("Someone is on the way"),
        _("%(name)s is on the way to “%(title)s”."),
    ),
    Membership.TransitStatus.RUNNING_LATE: (
        _("Someone is running late"),
        _("%(name)s is running about 10 minutes late for “%(title)s”."),
    ),
}


@transaction.atomic
def set_transit_status(user, activity, status) -> Membership:
    """A current member self-declares an ephemeral "on my way" / "running ~10 min late" cue, so
    the group can hold the start a moment. Mirrors mark_arrived's every safety property — fixed
    enum (no free text, no location), members-only, can_participate-gated, OPEN + arrival window,
    blocked pairs excluded, CHILD-cohort guardian fan-out keyed on an ACTIVE GuardianRelationship,
    audited, and cleared by expire_arrivals so it never becomes a punctuality record. Forward-only
    and per-state idempotent: re-asserting the same (or an earlier) state never re-pings.

    Like mark_arrived (and unlike the read-only-conversation gate on post_to_thread / reactions),
    a seated GUARDIAN-role member MAY emit this cue: a guardian accompanying a CHILD meetup in
    person is a real co-located participant, and "I'm on the way" is logistics, not conversation.
    Kept deliberately identical to its arrival-ping sibling — change both together if that ever
    shifts."""
    from apps.safety.services import blocked_user_ids, record_audit

    if status not in _TRANSIT_COPY:  # NONE / unknown values are not a user action
        raise InvalidState(_("Choose a valid status."))
    membership = current_members(activity).filter(user=user).first()
    if membership is None:
        raise NotAMember(_("Only current members can share a status."))
    if not can_participate(user):
        raise NotEligible(_("Sharing a status requires verified, consented participation."))
    if activity.status != Activity.Status.OPEN:
        raise InvalidState(_("You can only share a status for an active meetup."))
    if not arrival_window_open(activity):
        raise InvalidState(_("A status can only be shared around the start time."))
    if _TRANSIT_RANK[status] <= _TRANSIT_RANK[membership.transit_status]:
        return membership  # idempotent: same-or-earlier state never re-pings the group

    membership.transit_status = status
    membership.save(update_fields=["transit_status", "updated_at"])

    blocked = blocked_user_ids(user)
    title, body_template = _TRANSIT_COPY[status]
    # The only arriver-derived string is display_name — the same low-entropy handle shown
    # app-wide. No per-cue note exists, so no unmoderated child-authored text reaches an adult.
    body = body_template % {
        "name": user.display_name or user.username,
        "title": activity.title,
    }
    url = f"/api/social/activities/{activity.id}/"
    notified: set[int] = set()
    for member in current_members(activity).exclude(user_id=user.id).select_related("user"):
        if member.user_id in blocked:
            continue
        _notify(member.user, "arrival", title, body=body, url=url)
        notified.add(member.user_id)

    # CHILD cohort only (teens self-manage, matching mark_arrived). Keyed on an ACTIVE
    # GuardianRelationship — never a loose is_child flag. Each guardian gets at most one ping.
    if user.cohort == Cohort.CHILD:
        for rel in GuardianRelationship.objects.filter(
            ward=user, status=GuardianRelationship.Status.ACTIVE
        ).select_related("guardian"):
            guardian = rel.guardian
            if guardian.id in blocked or guardian.id in notified:
                continue
            _notify(guardian, "arrival", title, body=body, url=url)
            notified.add(guardian.id)

    record_audit("activity.transit", actor=user, target=activity, reason=status)
    return membership


@transaction.atomic
def propose_place(proposer, place, required_confirmations=None) -> UserPlaceProposal:
    if not can_participate(proposer):
        raise NotEligible("User cannot propose places (needs verification/consent).")
    return UserPlaceProposal.objects.create(
        place=place,
        proposer=proposer,
        required_confirmations=(
            DEFAULT_PLACE_QUORUM if required_confirmations is None else required_confirmations
        ),
    )


@transaction.atomic
def confirm_place(user, proposal: UserPlaceProposal) -> UserPlaceProposal:
    if proposal.status != UserPlaceProposal.Status.PENDING:
        raise InvalidState("This place proposal is no longer open for confirmation.")
    if proposal.proposer_id == user.id:
        raise InvalidState("The proposer cannot confirm their own place.")
    if not can_participate(user):
        raise NotEligible("User cannot confirm places (needs verification/consent).")
    PlaceConfirmation.objects.get_or_create(proposal=proposal, user=user)
    if proposal.confirmations.count() >= proposal.required_confirmations:
        proposal.status = UserPlaceProposal.Status.PUBLISHED
        proposal.published_at = timezone.now()
        proposal.save(update_fields=["status", "published_at"])
    return proposal


@transaction.atomic
def propose_place_with_venue(
    proposer, *, name, lon, lat, activity_type, allow_nearby=False
) -> UserPlaceProposal:
    """Create a user venue (source=USER) + its seed activity edge, then open a co-creation
    proposal (F25). Hidden from the public until the quorum (or staff) publishes it. A
    name-similar venue within the dedup radius is a hard DuplicatePlace; any place within the
    soft radius is a DuplicatePlace(soft=True) the proposer can override with allow_nearby."""
    from django.contrib.gis.geos import Point
    from django.contrib.gis.measure import D

    from apps.places.enrichment.dedup import find_duplicate
    from apps.places.models import Place, PlaceActivity

    if not can_participate(proposer):  # fail before creating any Place (no orphan)
        raise NotEligible(_("User cannot propose places (needs verification/consent)."))
    name = (name or "").strip()[:255]
    point = Point(lon, lat, srid=4326)
    radius = getattr(settings, "PLACE_PROPOSAL_DEDUP_RADIUS_M", PLACE_PROPOSAL_DEDUP_RADIUS_M)
    dup = find_duplicate(point, name, max_distance_m=radius)
    if dup is not None:  # same named venue nearby — hard block
        raise DuplicatePlace(dup.id, dup.name)
    if not allow_nearby:  # soft: any place very close, even with a different name
        soft_radius = getattr(
            settings, "PLACE_PROPOSAL_SOFT_RADIUS_M", PLACE_PROPOSAL_SOFT_RADIUS_M
        )
        near = Place.objects.filter(location__distance_lte=(point, D(m=soft_radius))).first()
        if near is not None:
            raise DuplicatePlace(near.id, near.name or "a nearby place", soft=True)
    place = Place.objects.create(name=name, location=point, source=Place.Source.USER)
    # origin=MANUAL is in the ingest PROTECTED_ORIGINS, so a re-ingest won't clobber the edge.
    PlaceActivity.objects.create(
        place=place,
        activity=activity_type,
        origin=PlaceActivity.Origin.MANUAL,
        confidence=1.0,
        source="user",
    )
    return propose_place(proposer, place)


@transaction.atomic
def staff_publish_proposal(staff_user, proposal: UserPlaceProposal) -> UserPlaceProposal:
    """Moderator fast-publish (F25) — the single-launch-city escape hatch when a 3-user quorum
    won't be reached organically."""
    if not staff_user.is_staff:
        raise NotEligible(_("Only staff may publish a place proposal."))
    if proposal.status != UserPlaceProposal.Status.PENDING:
        raise InvalidState(_("This proposal is not pending."))
    proposal.status = UserPlaceProposal.Status.PUBLISHED
    proposal.published_at = timezone.now()
    proposal.save(update_fields=["status", "published_at"])
    from apps.safety.services import record_audit

    record_audit("place.staff_published", actor=staff_user, target=proposal.place)
    return proposal


@transaction.atomic
def staff_reject_proposal(
    staff_user, proposal: UserPlaceProposal, *, reason=""
) -> UserPlaceProposal:
    """Moderator close-out of a bad/duplicate submission. A REJECTED proposal keeps its place
    hidden by public_places (never published)."""
    if not staff_user.is_staff:
        raise NotEligible(_("Only staff may reject a place proposal."))
    if proposal.status != UserPlaceProposal.Status.PENDING:
        raise InvalidState(_("This proposal is not pending."))
    proposal.status = UserPlaceProposal.Status.REJECTED
    proposal.save(update_fields=["status"])
    from apps.safety.services import record_audit

    record_audit(
        "place.staff_rejected", actor=staff_user, target=proposal.place, reason=reason[:200]
    )
    return proposal


def pending_proposals_for(user):
    """Open proposals OTHER users may confirm (F25). Annotates a confirmation count so the list
    can show '2 of 3 confirmed' WITHOUT ever naming the proposer/confirmers."""
    from django.db.models import Count

    return (
        UserPlaceProposal.objects.filter(status=UserPlaceProposal.Status.PENDING)
        .exclude(proposer=user)
        .select_related("place")
        .annotate(confirmations_count=Count("confirmations"))
        .order_by("created_at")[:200]
    )


# --- Public Groups: persistent, cohort-pinned standing groups ------------------------------
#
# A Group reuses the ONE hardened thread stack (post_to_thread / can_read_thread, generalised to
# owner_object above) rather than cloning it. The group-specific logic below is discovery (who can
# see which groups), membership (open join / leave / cohort-change eviction), the per-cohort roster
# rule (minors are roster-LESS), creation/curation (staff-only for minors; flag-gated for adults),
# and the read-time Community linkage. Every state-changing service is @transaction.atomic and
# audits inside the txn. See docs/PUBLIC_GROUPS_DESIGN.md.


def _group_user_creation_cohorts() -> set:
    """Cohorts permitted to SELF-CREATE a group (the hard-wall). UNASSIGNED and BOTH minor cohorts
    are unconditionally discarded — a minor can never own/create a group even by misconfiguration
    (mirrors connections._allowed_cohorts). Minor groups are staff-curated only, never self-made."""
    allowed = set(getattr(settings, "GROUPS_USER_CREATION_COHORTS", (Cohort.ADULT,)))
    allowed.discard(Cohort.UNASSIGNED)
    allowed.discard(Cohort.CHILD)
    allowed.discard(Cohort.TEEN)
    return allowed


def visible_groups(viewer):
    """Groups a viewer may discover — ACTIVE, not-hidden groups of their OWN cohort, excluding any
    owned by a blocked user. The SOLE read-access path for group discovery: every group-entity
    surface (web list/detail, DRF GroupViewSet, Community linkage) MUST source from this, so a CHILD
    can never even learn an ADULT group exists. Anon/UNASSIGNED -> none (never a named empty group),
    mirroring visible_communities."""
    if not getattr(viewer, "is_authenticated", False) or viewer.cohort == Cohort.UNASSIGNED:
        return Group.objects.none()
    from apps.safety.services import blocked_user_ids

    qs = Group.objects.filter(
        cohort=viewer.cohort, status=Group.Status.ACTIVE, is_hidden=False
    ).select_related("area", "category", "activity_type", "owner")
    blocked = blocked_user_ids(viewer)
    if blocked:
        qs = qs.exclude(owner_id__in=blocked)
    return qs.order_by("title")


def group_by_id(group_id, viewer):
    """A group at this id reachable by the viewer, else None — a cross-cohort/hidden/archived id is
    a clean 404 for an ordinary member, never a content leak. The single RETRIEVE chokepoint shared
    by the web detail/management views and the DRF GroupViewSet retrieve.

    STAFF get a curation/moderation bypass (any group, incl. hidden/cross-cohort), mirroring the
    activity_detail staff bypass — so the adult staff curator of a MINOR group can still
    view/announce/archive it (announce + archive are owner/staff-gated, not cohort-gated). The
    cohort wall in can_read_thread still bars them from a minor PEER thread (announcement-only
    anyway). DISCOVERY (visible_groups + the list) stays STRICTLY cohort-walled; this bypass is
    read-only retrieve, never a discovery surface."""
    if getattr(viewer, "is_staff", False):
        return Group.objects.filter(pk=group_id).first()
    return visible_groups(viewer).filter(pk=group_id).first()


def group_feed_activities(group, viewer, *, upcoming=True):
    """Upcoming activities a viewer can see in this group's (area x type/category) coordinate — the
    same cohort-filtered feed a Community shows, narrowed to the group's coordinate. Cohort-walled
    twice (viewer.cohort == group.cohort here, and visible_activities is itself cohort-pinned). This
    is DISCOVERY (activities to go to), never the group's membership."""
    from apps.communities.services import _area_place_q

    if not getattr(viewer, "is_authenticated", False) or viewer.cohort != group.cohort:
        return Activity.objects.none()
    qs = visible_activities(viewer).filter(_area_place_q(group.area))
    if group.tier == Group.Tier.TYPE:
        qs = qs.filter(activity_type=group.activity_type)
    else:
        qs = qs.filter(activity_type__category=group.category)
    if upcoming:
        qs = qs.filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
    return qs.select_related("activity_type", "place", "owner").order_by("starts_at")


@transaction.atomic
def create_group(
    actor,
    *,
    area,
    title,
    activity_type=None,
    category=None,
    description="",
    cohort=None,
    is_staff_curated=False,
):
    """Create a standing group. The cohort is PINNED at creation and IMMUTABLE thereafter; cross-age
    is structurally impossible. Two creation paths:

      - CHILD/TEEN group: STAFF-CURATED ONLY (``actor.is_staff``) and only while minor onboarding is
        enabled (the whole minor apparatus ships dark in prod). A minor can never own a group — an
        openly-joinable, persistent space gathering minors by city+activity is a high-value grooming
        target, so a human gate guards its very existence (matching how minor Communities are
        materialized by a vetted job, never user-declared).
      - ADULT group: self-creatable only behind ``GROUPS_ALLOW_USER_CREATED`` (default False —
        staff-curated everywhere first); staff may always create.

    A non-staff actor can only ever create a group in their OWN cohort. Owner is auto-admitted
    (role=OWNER, state=MEMBER); the Thread is created in the SAME txn (mirrors create_activity)."""
    from apps.accounts.services import minor_onboarding_enabled
    from apps.safety.services import allow_action, record_audit

    if not can_participate(actor) or not _has_cohort(actor):
        raise NotEligible(
            _("You need verified, consented participation and a cohort to create a group.")
        )

    target_cohort = cohort or actor.cohort
    if target_cohort == Cohort.UNASSIGNED:
        raise NotEligible(_("Group creation is not available for your account."))
    # A non-staff actor can only ever create a group in their OWN cohort (no cross-cohort creation).
    if not actor.is_staff and target_cohort != actor.cohort:
        raise NotEligible(_("You can only create a group in your own cohort."))

    minor = target_cohort in (Cohort.CHILD, Cohort.TEEN)
    if minor:
        if not actor.is_staff:
            raise NotEligible(_("Groups for under-18s are created by staff only."))
        if not minor_onboarding_enabled():
            raise NotEligible(_("Minor groups are not enabled on this deployment."))
        is_staff_curated = True
    else:
        # Adult-cohort. The hard-wall makes CHILD/TEEN structurally unreachable here regardless.
        if target_cohort not in _group_user_creation_cohorts():
            raise NotEligible(_("Group creation is not available for that cohort."))
        if not actor.is_staff and not getattr(settings, "GROUPS_ALLOW_USER_CREATED", False):
            raise NotEligible(_("Group creation is staff-only on this deployment for now."))
        if actor.is_staff:
            is_staff_curated = True

    # Resolve the taxonomy coordinate -> tier + rollup category.
    if activity_type is not None:
        tier = Group.Tier.TYPE
        category = activity_type.category
    elif category is not None:
        tier = Group.Tier.CATEGORY
    else:
        raise InvalidState(_("A group needs an activity type or a category."))

    limit = getattr(settings, "GROUP_CREATE_RATE_LIMIT", 5)
    window = getattr(settings, "GROUP_CREATE_RATE_WINDOW_SECONDS", 3600)
    if not allow_action(actor, "group_create", limit=limit, window_seconds=window):
        raise InvalidState(_("You are creating groups too quickly; slow down."))

    group = Group.objects.create(
        owner=actor,
        area=area,
        category=category,
        activity_type=activity_type,
        tier=tier,
        cohort=target_cohort,
        title=title,
        description=description,
        is_staff_curated=is_staff_curated,
    )
    GroupMembership.objects.create(
        group=group,
        user=actor,
        role=GroupMembership.Role.OWNER,
        state=GroupMembership.State.MEMBER,
    )
    Thread.objects.create(group=group)
    record_audit(
        "group.created",
        actor=actor,
        target=group,
        cohort=str(target_cohort),
        staff_curated=is_staff_curated,
    )
    return group


@transaction.atomic
def join_group(user, group_id) -> GroupMembership:
    """Openly join a group: admit straight to MEMBER (no vote, no request). Re-resolves the group
    through visible_groups(user) by id (NEVER trusts a passed object), so a hidden/archived/
    cross-cohort group can't be joined. Idempotent — a re-join by a current member is a no-op (never
    re-notifies, never re-audits). Rate-limited on a DEDICATED bucket (mass-join reconnaissance)."""
    from apps.safety.services import allow_action, is_blocked, record_audit

    group = visible_groups(user).filter(pk=group_id).first()
    if group is None:
        raise NotAMember(_("No such group."))
    if not can_participate(user):
        raise NotEligible(_("Joining requires verified, consented participation."))
    if user.id != group.owner_id and is_blocked(user, group.owner):
        raise InvalidState(_("This group is no longer available."))
    existing = group.memberships.filter(user=user).first()
    if existing is not None and existing.state == GroupMembership.State.MEMBER:
        return existing  # idempotent: already a member
    limit = getattr(settings, "GROUP_JOIN_RATE_LIMIT", 20)
    window = getattr(settings, "GROUP_JOIN_RATE_WINDOW_SECONDS", 3600)
    if not allow_action(user, "group_join", limit=limit, window_seconds=window):
        raise InvalidState(_("You are joining groups too quickly; slow down."))
    if existing is not None:
        existing.state = GroupMembership.State.MEMBER
        existing.save(update_fields=["state"])
        membership = existing
    else:
        membership = GroupMembership.objects.create(
            group=group,
            user=user,
            role=GroupMembership.Role.MEMBER,
            state=GroupMembership.State.MEMBER,
        )
    record_audit("group.joined", actor=user, target=group)
    return membership


@transaction.atomic
def leave_group(user, group_id) -> GroupMembership | None:
    """A member leaves a group (state -> LEFT). The owner cannot leave (they archive instead).
    Re-resolves via visible_groups. Returns the membership, or None if not a current member."""
    from apps.safety.services import record_audit

    group = visible_groups(user).filter(pk=group_id).first()
    if group is None:
        return None
    membership = group.memberships.filter(user=user).first()
    if membership is None or membership.state != GroupMembership.State.MEMBER:
        return None
    if membership.role == GroupMembership.Role.OWNER:
        raise InvalidState(_("The owner cannot leave their own group; archive it instead."))
    membership.state = GroupMembership.State.LEFT
    membership.save(update_fields=["state"])
    record_audit("group.left", actor=user, target=group)
    return membership


@transaction.atomic
def remove_user_from_groups(user, *, reason="participation_revoked") -> int:
    """Evict a user from ALL their groups (MEMBER -> REMOVED) when they lose eligibility — a cohort
    change (their old groups are all cross-cohort now) or a consent/participation revocation (which
    does NOT change cohort, so it must be wired separately — see accounts.apply_assurance /
    revoke_parental_consent / revoke_guardian). Mirrors messaging.remove_user_from_conversations but
    SIMPLER: groups have no guardian-observer rows, so there is no prune fan-out. The read-time
    cohort + can_participate re-checks in can_read_thread / group_roster fail closed even if a call
    here is somehow missed. Returns the number of memberships removed."""
    from apps.safety.services import record_audit

    n = GroupMembership.objects.filter(user=user, state=GroupMembership.State.MEMBER).update(
        state=GroupMembership.State.REMOVED
    )
    # ACCEPTED LOW (review finding): this evicts the user's own MEMBERSHIPS but does not archive
    # groups they OWN. An evicted owner can no longer announce (post_announcement re-checks current
    # membership) or peer-post, and staff can archive an ownerless group, so at most a benign
    # unmanaged SAME-cohort group remains — never a cross-cohort or safety hole. Minor groups are
    # staff-owned and staff are never evicted here, so a minor group can never become ownerless.
    if n:
        record_audit("group.participation_revoked", actor=user, count=n, reason=reason)
    return n


def group_roster(group, viewer):
    """The SOLE 'who is in this group' read path — returns a list[User] or None. Cohort wall FIRST,
    then the per-cohort rule (the headline child-safety requirement):

      - anon / cross-cohort viewer    -> None
      - CHILD / TEEN viewer           -> None ALWAYS (minors never see a roster/count/who-is-here)
      - ADULT non-member              -> None (member-gated, not just cohort-gated)
      - ADULT member                  -> the live members, defended in depth: each listed member is
        re-filtered to ``user.cohort == group.cohort`` (a missed eviction can NEVER surface an
        off-cohort user — symmetric with can_read_thread's read-time cohort re-check), to active +
        eligible (can_participate, catching a consent-revoked/suspended member eviction missed), and
        block-filtered both ways."""
    if not getattr(viewer, "is_authenticated", False) or viewer.cohort != group.cohort:
        return None
    if viewer.cohort in (Cohort.CHILD, Cohort.TEEN):
        return None
    if not group.memberships.filter(user=viewer, state=GroupMembership.State.MEMBER).exists():
        return None
    from apps.safety.services import blocked_user_ids

    blocked = blocked_user_ids(viewer)
    rows = (
        group.memberships.filter(
            state=GroupMembership.State.MEMBER, user__cohort=group.cohort, user__is_active=True
        )
        .exclude(user_id__in=blocked)
        .select_related("user")
        .order_by("joined_at")
    )
    return [m.user for m in rows if can_participate(m.user)]


def group_member_count(group, viewer):
    """``len(group_roster(...))`` or None — the SAME gated read, never a stored count, never a
    second surface. None for minors / non-members; an incidental display number for an adult
    member."""
    roster = group_roster(group, viewer)
    return len(roster) if roster is not None else None


@transaction.atomic
def archive_group(actor, group) -> Group:
    """Owner or staff archives a group: status -> ARCHIVED, which FREEZES its thread (via
    is_thread_frozen) and drops it from discovery (visible_groups filters ACTIVE). Not a hard
    delete (audit/appeal)."""
    from apps.safety.services import record_audit

    if actor.id != group.owner_id and not getattr(actor, "is_staff", False):
        raise NotAMember(_("Only the owner or staff can archive a group."))
    if group.status != Group.Status.ACTIVE:
        return group
    group.status = Group.Status.ARCHIVED
    group.save(update_fields=["status", "updated_at"])
    record_audit("group.archived", actor=actor, target=group)
    return group


def linked_group_for_community(community, viewer):
    """The ACTIVE group (if any) on the SAME (cohort, area, type-or-category) coordinate as a
    community, VISIBLE to the viewer. Sourced from visible_groups(viewer) — NEVER raw Group.objects
    — so a child community card can never surface an adult group's existence (both ends cohort-
    walled). Returns a Group or None; the caller links by NAME only (no membership/count)."""
    qs = visible_groups(viewer).filter(area_id=community.area_id)
    if community.tier == community.Tier.TYPE:
        qs = qs.filter(tier=Group.Tier.TYPE, activity_type_id=community.activity_type_id)
    else:
        qs = qs.filter(tier=Group.Tier.CATEGORY, category_id=community.category_id)
    return qs.first()


# --- F27: ephemeral "gauge interest" proto-meetups -------------------------------------
# The throwaway, threshold sibling of the persistent Group: float a place+type+coarse-time,
# let same-cohort peers signal "I'd come", and convert to a real Activity once a few do. A
# failed gauge silently expires. The interest signal is a plain M2M that NEVER touches
# Membership, so it can never establish a shared activity / enable connections.can_connect.

INTEREST_DEFAULT_LIFETIME_DAYS = 14  # short by design; settings.INTEREST_LIFETIME_DAYS overrides
INTEREST_GO_THRESHOLD = 3  # a low "ready to start" nudge; never a hard block on the proposer


def _interest_lifetime() -> timedelta:
    return timedelta(
        days=getattr(settings, "INTEREST_LIFETIME_DAYS", INTEREST_DEFAULT_LIFETIME_DAYS)
    )


def interest_threshold() -> int:
    return getattr(settings, "INTEREST_THRESHOLD", INTEREST_GO_THRESHOLD)


def visible_gauges(user):
    """Active gauges (not converted, not expired) in the viewer's OWN cohort, minus blocked
    proposers — the single cohort-walled read primitive, mirroring visible_groups."""
    if not _has_cohort(user):
        return ActivityInterest.objects.none()
    from apps.safety.services import blocked_user_ids

    return (
        ActivityInterest.objects.filter(
            cohort=user.cohort,
            converted_activity__isnull=True,
            expires_at__gt=timezone.now(),
        )
        .exclude(proposer_id__in=blocked_user_ids(user))
        .select_related("place", "activity_type", "proposer")
        .order_by("expires_at")
    )


def gauge_by_id(pk, user):
    return visible_gauges(user).filter(pk=pk).first()


def interest_count(interest) -> int:
    """How many peers have signalled — a COUNT only (the gauge never exposes WHO)."""
    return interest.interested_users.count()


def _gauge_active(interest) -> bool:
    return interest.converted_activity_id is None and interest.expires_at > timezone.now()


@transaction.atomic
def propose_interest(proposer, *, place, activity_type, coarse_window) -> ActivityInterest:
    """Float an ephemeral gauge. Same eligibility as creating an activity (the proposer must be
    able to actually convert it), cohort pinned from the proposer, at a PUBLICLY-visible place
    (F25). For a CHILD proposer the place must also pass the F9 child-venue gate — otherwise the
    gauge could never convert (a dead end) and would surface a non-child-safe venue to children.
    The proposer auto-counts as interested."""
    from apps.places.services import public_places

    if not can_create_activity(proposer):
        raise NotEligible(
            _("You need to be verified (and, if a minor, consented) and in a cohort to start one.")
        )
    if activity_type is None or not getattr(activity_type, "is_active", False):
        raise NotEligible(_("Pick an available activity type."))
    if place is None or not public_places().filter(pk=place.pk).exists():
        raise NotEligible(_("Pick a publicly listed place."))
    # F9: mirror create_activity so a CHILD gauge can't be floated at a non-child-safe venue
    # (which would then fail at convert — a dead end — and surface that venue to children).
    if proposer.cohort == Cohort.CHILD and getattr(settings, "CHILD_PUBLIC_VENUES_ONLY", True):
        from apps.places.services import is_child_safe_venue

        if not is_child_safe_venue(place):
            raise InvalidState(
                _(
                    "This venue isn't on the approved list for children's activities yet. Pick a "
                    "library, park, school, sports or community venue — or ask a moderator to "
                    "approve this place."
                )
            )
    # W3-F2: enforce the guardian category envelope on a CHILD's gauge too — otherwise a child
    # could float (and rally a quorum around) a disallowed-category meetup that then can't convert.
    if not category_envelope_allows(proposer, activity_type):
        raise InvalidState(_("Your guardian's settings don't allow this kind of activity yet."))
    if coarse_window not in ActivityInterest.CoarseWindow.values:
        raise InvalidState(_("Pick one of the listed time windows."))
    interest = ActivityInterest.objects.create(
        proposer=proposer,
        place=place,
        activity_type=activity_type,
        cohort=proposer.cohort,
        coarse_window=coarse_window,
        expires_at=timezone.now() + _interest_lifetime(),
    )
    interest.interested_users.add(proposer)
    return interest


@transaction.atomic
def mark_interested(user, interest) -> ActivityInterest:
    """A same-cohort peer signals "I'd come". Idempotent. Never creates a Membership, so it can
    never make the user a co-member or feed connections.can_connect."""
    from apps.safety.services import is_blocked

    if not _gauge_active(interest):
        raise InvalidState(_("This gauge is no longer open."))
    if getattr(user, "cohort", None) != interest.cohort:
        raise NotEligible(_("This gauge is for a different group."))
    if not can_participate(user):
        raise NotEligible(_("Verified, consented participation is required."))
    if is_blocked(user, interest.proposer):
        raise NotEligible(_("This gauge is not available."))
    interest.interested_users.add(user)  # idempotent (a repeat never double-counts)
    return interest


@transaction.atomic
def unmark_interested(user, interest) -> ActivityInterest:
    """Withdraw the signal. Idempotent; allowed even after expiry (pure removal)."""
    interest.interested_users.remove(user)
    return interest


@transaction.atomic
def convert_to_activity(proposer, interest, *, title, starts_at, **extra) -> Activity:
    """The proposer turns a gauge into a real meetup. Calls create_activity VERBATIM (so every
    cohort/consent/place/child-venue gate re-runs and the cohort is re-pinned), pinning the
    gauge's OWN place + activity_type (a tampered request can't swap them). Then fans a one-shot
    JOIN-style invite to everyone who signalled — excluding the proposer and blocked pairs — and
    marks the gauge converted. The interest set is the only input; no Membership is ever copied."""
    from apps.notifications.models import Notification
    from apps.safety.services import blocked_user_ids, record_audit

    # Lock + re-check on the locked row so two concurrent converts can't both spawn an Activity
    # (mirrors cast_vote/leave/cancel/complete — "two in-flight requests can't split-brain").
    interest = ActivityInterest.objects.select_for_update().get(pk=interest.pk)
    if interest.proposer_id != proposer.id:
        raise NotAMember(_("Only the proposer can start this gauge as a meetup."))
    if not _gauge_active(interest):
        raise InvalidState(_("This gauge is no longer open."))
    # place/activity_type are the gauge's own — never from the request (no bait-and-switch).
    extra.pop("place", None)
    extra.pop("activity_type", None)
    activity = create_activity(
        proposer,
        place=interest.place,
        activity_type=interest.activity_type,
        title=title,
        starts_at=starts_at,
        **extra,
    )
    interest.converted_activity = activity
    interest.save(update_fields=["converted_activity", "updated_at"])
    # One-shot invite to the peers who signalled, excluding the proposer + blocked pairs. Re-filter
    # each recipient by the CONVERTED activity's cohort + live eligibility: a peer who re-verified
    # into another cohort (or lost consent) after signalling must never be pushed a cross-cohort or
    # ineligible invite — defence-in-depth mirroring group_roster's read-time re-check.
    blocked = blocked_user_ids(proposer)
    title_n = _("A meetup you were interested in is on")
    body_n = _("%(t)s is now a real meetup — join if you can come.") % {"t": activity.title}
    url = f"/api/social/activities/{activity.id}/"
    for u in interest.interested_users.exclude(id=proposer.id).exclude(id__in=blocked):
        if getattr(u, "cohort", None) != activity.cohort or not can_participate(u):
            continue
        _notify(u, Notification.Kind.INTEREST_CONVERTED, str(title_n), body=str(body_n), url=url)
    record_audit("interest.converted", actor=proposer, target=activity)
    return activity
