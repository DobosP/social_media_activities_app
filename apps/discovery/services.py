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
from apps.social.models import Activity, GroupMembership, Post
from apps.social.services import visible_activities, visible_groups


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
    membership is checked live (never a stored rollup). Hidden posts stay hidden.

    Two-step shape (review W1-17): resolve the viewer's few thread ids first (cheap,
    indexed), then read newest-first per thread via the (thread, created_at) Post index
    — instead of one big join the planner may heap-scan on every home render."""
    from apps.social.models import Thread

    my_group_ids = list(
        visible_groups(user)
        .filter(memberships__user=user, memberships__state=GroupMembership.State.MEMBER)
        .values_list("id", flat=True)
    )
    if not my_group_ids:
        return []
    thread_ids = list(Thread.objects.filter(group_id__in=my_group_ids).values_list("id", flat=True))
    return list(
        Post.objects.filter(thread_id__in=thread_ids, is_announcement=True, is_hidden=False)
        .select_related("thread__group", "author")
        .order_by("-created_at", "-id")[:limit]
    )


def beginner_friendly(user, *, limit=6, exclude_ids=()):
    """W3-F11: an always-on, low-stakes entry point for newcomers — upcoming OPEN activities in the
    viewer's cohort that explicitly welcome beginners (the F17 ``beginners_welcome`` flag),
    soonest-first and bounded. Routed through ``visible_activities`` so the cohort wall + the
    blocked-owner exclusion hold; ordering is deterministic (never popularity/engagement — inv.2)
    and NO join-derived count/badge is emitted. It lets a newcomer with no track record see where
    they are explicitly wanted WITHOUT scanning the whole feed or knowing to toggle a filter.
    ``exclude_ids`` drops activities already shown elsewhere on the page so the strip is distinct,
    never a second copy of a card already on screen."""
    from django.utils import timezone

    return list(
        visible_activities(user)
        .filter(
            status=Activity.Status.OPEN,
            starts_at__gte=timezone.now(),
            beginners_welcome=True,
        )
        .exclude(id__in=exclude_ids)
        .select_related("place", "activity_type", "owner")
        .order_by("starts_at")[:limit]
    )


def build_home_feed(user, *, near_point=None, radius_m=None, limit=8):
    """The typed home feed. Returns plain sections so the web view renders them
    directly and the API serializes them — one composition, two surfaces."""
    recommended = recommended_with_reasons(
        user, limit=limit, near_point=near_point, radius_m=radius_m
    )
    return {
        "recommended": recommended,
        # W3-F11: a DISTINCT, always-on beginners strip — deduped here against `recommended` so it
        # is never a second copy of a card shown there (the web view additionally dedups it against
        # its web-only "upcoming" block).
        "beginners": beginner_friendly(user, exclude_ids=[a.id for a in recommended]),
        "events": interest_matched_events(user),
        "group_updates": group_updates(user),
    }
