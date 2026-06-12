"""Home-feed composition (W2): ONE deterministic "for you" feed shared by the web home
page and the mobile feed API, so both surfaces show the same items for the same honest
reasons. Three typed sections, each behind its existing read gate:

- recommended activities — ``recommended_with_reasons`` (visible_activities inside);
- events the viewer may be interested in — matched to their OWN declared interests
  (never inferred/tracked), place-gated by ``upcoming_events`` (F25);
- updates from the viewer's groups — latest announcements, gated through
  ``visible_groups`` + live membership (cohort wall, hidden/archived/blocked-owner out).

Ordering is deterministic (soonest-first / newest-first) — never popularity, never
engagement (inv.2). Every section is bounded; there is no infinite scroll."""

from apps.events.services import upcoming_events
from apps.recommendations.models import UserInterest
from apps.recommendations.services import recommended_with_reasons
from apps.social.models import GroupMembership, Post
from apps.social.services import visible_groups


def interest_matched_events(user, *, limit=6):
    """Upcoming events matched to the viewer's declared interests, each carrying an
    honest ``feed_reason`` ("matches your interest in X"). When fewer than ``limit``
    match (or the user declared nothing), soonest-first events fill the remainder with
    no reason claimed — cold start stays honest."""
    interests = dict(
        UserInterest.objects.filter(user=user).values_list(
            "activity_type__slug", "activity_type__name"
        )
    )
    qs = upcoming_events().order_by("starts_at")
    events = []
    if interests:
        events = list(qs.filter(activity_type__slug__in=interests)[:limit])
        for e in events:
            e.feed_reason = f"matches your interest in {interests[e.activity_type.slug]}"
    if len(events) < limit:
        seen = {e.id for e in events}
        for e in qs[: limit * 2]:
            if e.id in seen:
                continue
            e.feed_reason = ""
            events.append(e)
            if len(events) >= limit:
                break
    return events


def group_updates(user, *, limit=5):
    """The latest announcements from groups the viewer is currently a member of.
    Routed through ``visible_groups`` (the single group discovery chokepoint) so the
    cohort wall, hidden/archived state and the blocked-owner exclusion all hold;
    membership is checked live (never a stored rollup). Hidden posts stay hidden."""
    my_groups = visible_groups(user).filter(
        memberships__user=user, memberships__state=GroupMembership.State.MEMBER
    )
    return list(
        Post.objects.filter(thread__group__in=my_groups, is_announcement=True, is_hidden=False)
        .select_related("thread__group", "author")
        .order_by("-created_at", "-id")[:limit]
    )


def build_home_feed(user, *, near_point=None, radius_m=None, limit=8):
    """The typed home feed. Returns plain sections so the web view renders them
    directly and the API serializes them — one composition, two surfaces."""
    return {
        "recommended": recommended_with_reasons(
            user, limit=limit, near_point=near_point, radius_m=radius_m
        ),
        "events": interest_matched_events(user),
        "group_updates": group_updates(user),
    }
