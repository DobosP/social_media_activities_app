"""Recommendation domain logic: manage interests, (re)compute embeddings, and rank
upcoming activities by interest similarity — always within the viewer's cohort."""

from django.contrib.gis.measure import D
from django.utils import timezone
from pgvector.django import CosineDistance

from apps.social.models import Activity, Membership
from apps.social.services import visible_activities, with_counts
from apps.taxonomy.models import ActivityType

from .embeddings import activity_vector, user_vector
from .models import ActivityEmbedding, UserInterest


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

    ranked = (
        ActivityEmbedding.objects.filter(activity__in=candidates)
        .annotate(distance=CosineDistance("vector", uvec))
        .select_related("activity", "activity__place", "activity__activity_type", "activity__owner")
        .order_by("distance")[:limit]
    )
    activities = []
    for embedding in ranked:
        embedding.activity.rec_distance = embedding.distance
        activities.append(embedding.activity)
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
