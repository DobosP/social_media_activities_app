"""Recommendation domain logic: manage interests, (re)compute embeddings, and rank
upcoming activities by interest similarity — always within the viewer's cohort."""

from django.contrib.gis.db.models.functions import Distance
from django.contrib.gis.measure import D
from django.utils import timezone
from pgvector.django import CosineDistance

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
