"""Recommendation domain logic: manage interests, (re)compute embeddings, and rank
upcoming activities by interest similarity — always within the viewer's cohort."""

import base64

from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.measure import D
from django.utils import timezone
from pgvector.django import CosineDistance

from apps.accounts.avatars import constellation_svg, identicon_svg
from apps.places.services import (
    accessibility_facts,
    get_access_preference,
    matches_access_preference,
)
from apps.social.models import Activity, Membership
from apps.social.services import visible_activities, with_counts
from apps.taxonomy.models import ActivityType

from .embeddings import activity_vector, user_vector
from .models import ActivityEmbedding, UserInterest

# F5 distance-bounded recommendations (request-only proximity; CORE ONLY — no stored vector/area).
# When the home feed carries request-only coordinates, the pgvector ranking is re-ranked in Python
# by cosine_sim x a deterministic distance-decay, plus a small SOFT access-match boost. With no
# coordinates the result is byte-identical to the pure-interest ranking.
DISTANCE_DECAY_SCALE_M = 3000.0  # half-weight at 3 km
REC_OVERFETCH = 4  # over-fetch the cosine ranking 4x before the Python distance re-rank
ACCESS_BOOST = 0.05  # additive, only for a positive access MATCH (never a penalty; F15-soft)
NEAR_SUFFIX_METRES = 2000.0  # the "· near you" reason suffix only shows for genuinely-close venues


def _distance_decay(metres):
    """Deterministic, monotone multiplier in (0, 1]; 1.0 at distance 0, never 0 within a finite
    radius — so it only RE-ORDERS within-radius matches, never erases one (the radius is the only
    hard cut). A null distance (missing geometry) yields 1.0 (no de-prioritisation)."""
    if metres is None:
        return 1.0
    return 1.0 / (1.0 + (max(metres, 0.0) / DISTANCE_DECAY_SCALE_M))


def _rec_score(cosine_distance, metres, access_match):
    """Blended in-Python sort key: interest similarity, de-prioritised by distance, plus a soft
    additive access lift. The similarity base is clamped to >= 0 so a negatively-correlated match
    (cosine_distance > 1) can never INVERT the distance ordering (a farther venue scoring above a
    nearer one); such low-relevance pairs tie at 0 and keep their interest-fetch order."""
    similarity = max(0.0, 1.0 - float(cosine_distance))
    return similarity * _distance_decay(metres) + (ACCESS_BOOST if access_match else 0.0)


def set_interests(user, slugs) -> list[ActivityType]:
    """Replace the user's declared interests with the given active activity-type slugs."""
    types = list(ActivityType.objects.filter(slug__in=list(slugs), is_active=True))
    UserInterest.objects.filter(user=user).delete()
    UserInterest.objects.bulk_create([UserInterest(user=user, activity_type=t) for t in types])
    return types


def get_interests(user):
    return ActivityType.objects.filter(interested_users__user=user).order_by("slug")


# --- Interest-graph avatar (the generated "constellation" profile picture) ----------------------
#
# The generated avatar visualises a user's DECLARED interests as a small graph: one glowing star
# per interest (colour = its taxonomy category), joined by an edge for each same-category pair.
# Edges are derived in pure Python from the nodes (zero extra queries), and the graph reads from
# interests prefetched by ``attach_interest_nodes`` when present, so rendering a *list* of avatars
# never N+1s. Rendering itself is a pure, DB-free function in ``apps.accounts.avatars``; this layer
# only turns a user into (nodes, edges) using the colour palette below. The picture reveals no PII:
# interests show as abstract colour-coded nodes, never readable activity labels, and it is only ever
# shown where any avatar is (same-cohort), exactly like the identicon it supersedes.

# Category slug -> star colour. Generic across all activity types (sport is just the launch slice).
INTEREST_AVATAR_COLORS = {
    "team_sport": "#ff7a45",  # warm orange
    "racquet_sport": "#ffa940",  # amber
    "sport": "#ff7a45",
    "outdoor": "#52c41a",  # green
    "fitness": "#13c2c2",  # teal
    "tabletop": "#9254de",  # violet
    "reading": "#4096ff",  # blue
    "video_games": "#f759ab",  # magenta
    "culture": "#ffc53d",  # gold
    "social": "#ff85c0",  # rose
}
_DEFAULT_AVATAR_COLOR = "#8c8c8c"


def _avatar_node(activity_type):
    cat = activity_type.category.slug if activity_type.category_id else ""
    return {
        "slug": activity_type.slug,
        "name": activity_type.name,
        "category": cat,
        "color": INTEREST_AVATAR_COLORS.get(cat, _DEFAULT_AVATAR_COLOR),
        "wellness": activity_type.wellness,
        "family_friendly": activity_type.family_friendly,
    }


def _same_category_edges(nodes):
    """Undirected edges joining every pair of interests that share a category — the cheap,
    query-free graph structure behind the constellation's colour-threads."""
    edges = []
    for i in range(len(nodes)):
        for j in range(i + 1, len(nodes)):
            if nodes[i]["category"] and nodes[i]["category"] == nodes[j]["category"]:
                edges.append((i, j))
    return edges


def interest_graph(user):
    """(nodes, edges) for a user's interest constellation. Uses interests prefetched by
    ``attach_interest_nodes`` when present; otherwise issues a single query. Node order is
    deterministic (by slug) so the avatar is byte-stable."""
    nodes = getattr(user, "_interest_nodes", None)
    if nodes is None:
        rows = (
            UserInterest.objects.filter(user=user)
            .select_related("activity_type__category")
            .order_by("activity_type__slug")
        )
        nodes = [_avatar_node(r.activity_type) for r in rows]
    return nodes, _same_category_edges(nodes)


def attach_interest_nodes(users):
    """Bulk-load interest nodes for many users in ONE query and cache them on each user as
    ``_interest_nodes``, so rendering a list of constellation avatars doesn't N+1. Returns the
    same list for convenience."""
    users = list(users)
    if not users:
        return users
    by_id = {u.id: [] for u in users}
    rows = (
        UserInterest.objects.filter(user__in=users)
        .select_related("activity_type__category")
        .order_by("activity_type__slug")
    )
    for r in rows:
        if r.user_id in by_id:
            by_id[r.user_id].append(_avatar_node(r.activity_type))
    for u in users:
        u._interest_nodes = by_id[u.id]
    return users


def interest_avatar_svg(user, *, px=80):
    """The user's generated avatar SVG: their interest constellation, or the identicon fallback
    when they have not declared any interests yet — so every account always has an avatar."""
    nodes, edges = interest_graph(user)
    seed = getattr(user, "username", None) or str(getattr(user, "pk", "") or "?")
    if nodes:
        return constellation_svg(seed, nodes, edges, px=px)
    return identicon_svg(seed, px=px)


def interest_avatar_data_uri(user, *, px=80):
    """``interest_avatar_svg`` as a base64 ``data:`` URI for an ``<img src>`` / JSON payload."""
    b64 = base64.b64encode(interest_avatar_svg(user, px=px).encode("utf-8")).decode("ascii")
    return f"data:image/svg+xml;base64,{b64}"


def recompute_activity_embedding(activity) -> None:
    ActivityEmbedding.objects.update_or_create(
        activity=activity, defaults={"vector": activity_vector(activity)}
    )


def recommend_activities(user, *, limit=20, near_point=None, radius_m=None):
    """Upcoming, cohort-appropriate activities the user hasn't joined, ranked by interest
    similarity. Falls back to soonest-first when we have no interest signal (cold start)."""
    candidates = (
        visible_activities(user)
        .filter(status=Activity.Status.OPEN, starts_at__gte=timezone.now())
        .exclude(memberships__user=user, memberships__state=Membership.State.MEMBER)
    )
    if near_point is not None and radius_m:
        candidates = candidates.filter(place__location__distance_lte=(near_point, D(m=radius_m)))

    uvec = user_vector(user)
    if not any(uvec):  # cold start — no declared interests or joined activities yet
        return list(
            with_counts(candidates.select_related("place", "activity_type", "owner")).order_by(
                "starts_at"
            )[:limit]
        )

    # F5: only when request-only coordinates are present do we over-fetch + Python re-rank by
    # distance. With no coordinates this stays the exact pure-interest ranking (byte-identical).
    proximity = near_point is not None and bool(radius_m)
    ranked = (
        ActivityEmbedding.objects.filter(activity__in=candidates)
        .annotate(distance=CosineDistance("vector", uvec))
        .select_related("activity", "activity__place", "activity__activity_type", "activity__owner")
    )
    if proximity:
        # Distance() on the geography(4326) column returns metres (.m). Distinct alias from the
        # cosine `distance` annotation. Over-fetch so the Python re-rank has headroom.
        ranked = ranked.annotate(
            geo_distance=Distance("activity__place__location", near_point)
        ).order_by("distance")[: limit * REC_OVERFETCH]
    else:
        ranked = ranked.order_by("distance")[:limit]

    pref = get_access_preference(user) if proximity else None
    activities = []
    for embedding in ranked:
        act = embedding.activity
        act.rec_distance = embedding.distance  # raw cosine distance — % match stays honest
        if proximity:
            metres = embedding.geo_distance.m if embedding.geo_distance is not None else None
            act.rec_near = metres is not None and metres <= NEAR_SUFFIX_METRES
            act.rec_access_match = (
                matches_access_preference(accessibility_facts(act.place), pref) == "match"
            )
            # Blended SORT key only (never stored/serialized): nearer + a soft access lift.
            act._rec_score = _rec_score(embedding.distance, metres, act.rec_access_match)
        activities.append(act)
    if proximity:
        activities.sort(key=lambda a: a._rec_score, reverse=True)
        activities = activities[:limit]
    # Attach member/participant counts in ONE query so the serializer doesn't fall back to
    # a per-row COUNT (the W2-2 N+1) on the ranked recommendations path.
    if activities:
        annotated = {
            a.id: a for a in with_counts(Activity.objects.filter(id__in=[a.id for a in activities]))
        }
        for act in activities:
            counts = annotated.get(act.id)
            if counts is not None:
                act.member_n = counts.member_n
                act.participant_n = counts.participant_n
    return activities


def recommended_with_reasons(user, *, limit=8, near_point=None, radius_m=None):
    """``recommend_activities`` + the F17 HONEST per-card reason, attached as
    ``rec_reason`` / ``match_pct``. Moved out of the web home view (W2) so the web feed
    and the mobile feed API state the exact same truthful reason: "matches your interest
    in X" from the viewer's OWN declared interests, the genuine "% match" otherwise, and
    "soonest first" on cold start. F5 appends "· near you" / access-match suffixes."""
    recommended = recommend_activities(user, limit=limit, near_point=near_point, radius_m=radius_m)
    interest_names = dict(
        UserInterest.objects.filter(user=user).values_list(
            "activity_type__slug", "activity_type__name"
        )
    )
    for a in recommended:
        distance = getattr(a, "rec_distance", None)
        if distance is None:  # cold start — no vector signal (never a perfect-match 0.0)
            a.rec_reason = "soonest first"
            continue
        a.match_pct = max(0, min(100, round((1 - float(distance)) * 100)))
        if a.activity_type.slug in interest_names:
            a.rec_reason = f"matches your interest in {interest_names[a.activity_type.slug]}"
        else:
            a.rec_reason = f"{a.match_pct}% match"
        if getattr(a, "rec_near", False):
            a.rec_reason += " · near you"
        if getattr(a, "rec_access_match", False):
            a.rec_reason += " · matches your access needs"
    return recommended
