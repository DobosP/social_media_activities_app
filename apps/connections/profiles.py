"""Tiered profile visibility (ADR-0028): how much of a person another user may see.

The tier is derived LIVE per request from existing relationship primitives — no stored
relationship labels, rollups, or history (inv.2). Hard vetoes come first and are
indistinguishable from nonexistence (the caller 404s): blocked either way, cross-cohort,
UNASSIGNED cohort, inactive target. docs/SAFETY.md caps the lowest tier at a minimal
display name + generated avatar; richer fields require a live shared context:

* ``STRANGER``  — same cohort, no shared context: display name + generated avatar ONLY.
* ``SHARED``    — current peer co-members of an Activity or standing Group, or a pending
  join request between a requester and an organizer (owner decision: the organizer
  reviewing a request sees the requester's card — and vice versa): + username handle,
  age-verified badge, the SHARED context itself, and the Connect affordance.
* ``CONNECTED`` — mutually accepted connection: + Message affordance; for adults also the
  declared-interest chips and (on the profile PAGE only) the uploaded photo.

Minor clamp: when the pair is in a minor cohort (CHILD/TEEN — cohorts always match here),
CONNECTED never adds interests or the uploaded photo; the card stays at the SHARED shape.
The hover card uses the GENERATED avatar for everyone — uploaded photos remain a
profile-page-only surface (existing media invariant).

Views (web page, hover partial, DRF) all call ``profile_card`` — gates live here, never in
views (house rule)."""

from django.db.models import Q

from apps.accounts.models import Cohort
from apps.safety.services import is_blocked
from apps.social.models import Activity, Group, GroupMembership, Membership

from .models import Connection
from .services import _peer_activity_ids, are_connected, can_connect, shares_activity

TIER_STRANGER = "stranger"
TIER_SHARED = "shared"
TIER_CONNECTED = "connected"

_MINOR_COHORTS = (Cohort.CHILD, Cohort.TEEN)
_ORGANIZER_ROLES = (Membership.Role.OWNER, Membership.Role.CO_ORGANIZER)
_CONTEXT_LIMIT = 3  # shared activity/group titles shown on a card


def _peer_group_ids(user):
    return GroupMembership.objects.filter(user=user, state=GroupMembership.State.MEMBER).values(
        "group_id"
    )


def shares_group(a, b) -> bool:
    """True iff a and b are both current members of at least one standing Group (peer-only
    by construction — GroupMembership has no guardian role). Live-derived, like
    ``shares_activity``."""
    return GroupMembership.objects.filter(
        user=b, state=GroupMembership.State.MEMBER, group_id__in=_peer_group_ids(a)
    ).exists()


def _organizer_ids(user):
    return Membership.objects.filter(
        user=user, state=Membership.State.MEMBER, role__in=_ORGANIZER_ROLES
    ).values("activity_id")


def _join_request_between(a, b) -> bool:
    """A pending join request links the two: one ORGANIZES an activity the other has
    REQUESTED to join. This is the 'should I admit this person' context (owner decision)."""
    return (
        Membership.objects.filter(
            user=b, state=Membership.State.REQUESTED, activity_id__in=_organizer_ids(a)
        ).exists()
        or Membership.objects.filter(
            user=a, state=Membership.State.REQUESTED, activity_id__in=_organizer_ids(b)
        ).exists()
    )


def _resolve(viewer, target) -> tuple[str, bool] | None:
    """(tier, join_request_between) or None on a veto — the join-request probe is computed
    once here and threaded into the card (review: avoid re-running its EXISTS pair)."""
    if not viewer or not target or viewer.pk == target.pk:
        return None
    if not getattr(viewer, "is_authenticated", False) or not target.is_active:
        return None
    if viewer.cohort == Cohort.UNASSIGNED or target.cohort == Cohort.UNASSIGNED:
        return None
    if viewer.cohort != target.cohort:
        return None
    if is_blocked(viewer, target):
        return None
    if are_connected(viewer, target):
        return TIER_CONNECTED, _join_request_between(viewer, target)
    if shares_activity(viewer, target) or shares_group(viewer, target):
        return TIER_SHARED, _join_request_between(viewer, target)
    if _join_request_between(viewer, target):
        return TIER_SHARED, True
    return TIER_STRANGER, False


def profile_tier(viewer, target) -> str | None:
    """The visibility tier of ``target`` for ``viewer``, or None (callers must 404 — a veto
    is indistinguishable from a nonexistent account). Self is not handled here: the web view
    redirects to the own-profile page before calling this."""
    resolved = _resolve(viewer, target)
    return resolved[0] if resolved else None


def _shared_activity_titles(viewer, target):
    peer_b = (
        Membership.objects.filter(user=target, state=Membership.State.MEMBER)
        .exclude(role=Membership.Role.GUARDIAN)
        .values("activity_id")
    )
    qs = Activity.objects.filter(id__in=_peer_activity_ids(viewer)).filter(id__in=peer_b)
    titles = list(qs.order_by("-created_at").values_list("title", flat=True)[:_CONTEXT_LIMIT])
    return titles, qs.count()


def _shared_group_titles(viewer, target):
    qs = Group.objects.filter(id__in=_peer_group_ids(viewer)).filter(id__in=_peer_group_ids(target))
    titles = list(qs.order_by("-created_at").values_list("title", flat=True)[:_CONTEXT_LIMIT])
    return titles, qs.count()


def _has_open_request(viewer, target) -> bool:
    return (
        Connection.objects.filter(status=Connection.Status.PENDING)
        .filter(Q(requester=viewer, addressee=target) | Q(requester=target, addressee=viewer))
        .exists()
    )


def profile_card(viewer, target) -> dict | None:
    """The tier-gated card payload for every surface (page, hover partial, API). None => 404.

    Field discipline: NOTHING beyond the matrix in ADR-0028. Never age band, cohort,
    progression, counts, attendance, activity history, or last-seen — at any tier."""
    resolved = _resolve(viewer, target)
    if resolved is None:
        return None
    tier, join_request = resolved
    from django.utils.translation import gettext as _

    from apps.recommendations.services import interest_avatar_data_uri

    minor = viewer.cohort in _MINOR_COHORTS  # cohorts match, so this covers both sides
    # STRANGER never learns the username handle (a SHARED-tier field) — a blank display
    # name falls back to a neutral placeholder, not the handle (review MED).
    if tier == TIER_STRANGER:
        display = target.display_name or _("A member")
    else:
        display = target.display_name or target.username
    card = {
        "tier": tier,
        "public_id": str(target.public_id),
        "display": display,
        # The image is a MUST on every tier: the generated avatar always exists (ADR-0027).
        "avatar": interest_avatar_data_uri(target, px=96),
        "minor": minor,
    }
    if tier == TIER_STRANGER:
        return card  # SAFETY.md cap: minimal display name + avatar, nothing else

    activities, activity_count = _shared_activity_titles(viewer, target)
    groups, group_count = _shared_group_titles(viewer, target)
    card.update(
        {
            "username": target.username,
            "verified": bool(target.is_identity_verified),
            "shared": {
                "activities": activities,
                "activity_count": activity_count,
                "activity_overflow": max(0, activity_count - len(activities)),
                "groups": groups,
                "group_count": group_count,
                "group_overflow": max(0, group_count - len(groups)),
                "join_request": join_request,
            },
            "connected": tier == TIER_CONNECTED,
            "can_connect": tier != TIER_CONNECTED and can_connect(viewer, target),
            "request_pending": tier != TIER_CONNECTED and _has_open_request(viewer, target),
            "can_message": tier == TIER_CONNECTED,
        }
    )
    if tier == TIER_CONNECTED and not minor:
        from apps.recommendations.models import UserInterest

        card["interests"] = list(
            UserInterest.objects.filter(user=target)
            .select_related("activity_type")
            .order_by("activity_type__name")
            .values_list("activity_type__name", flat=True)
        )
        card["show_photo"] = True  # profile PAGE may show the uploaded photo (media gate re-checks)
    else:
        card["interests"] = None
        card["show_photo"] = False
    return card
