"""P3 recommendations: declared interests + a content-based activity embedding.

Privacy by design (per docs/SAFETY.md): recommendations are computed ONLY from a user's
*declared* interests and the activities they've *joined* — never from behavioural tracking.
The embedding is a deterministic feature-hash of the activity taxonomy (no external model,
no PII), stored as a pgvector for cosine-similarity ranking.
"""

from django.conf import settings
from django.db import models
from pgvector.django import HnswIndex, VectorField

from apps.social.models import Activity
from apps.taxonomy.models import ActivityCategory, ActivityType

# Fixed embedding dimension. Taxonomy slugs are feature-hashed into this space, so the
# dimension is stable even as new activity types/categories are added.
EMBEDDING_DIM = 64


class UserInterest(models.Model):
    """An activity type a user has explicitly said they're interested in."""

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="interests"
    )
    activity_type = models.ForeignKey(
        ActivityType, on_delete=models.CASCADE, related_name="interested_users"
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(fields=["user", "activity_type"], name="uq_user_interest"),
        ]

    def __str__(self):
        return f"{self.user} -> {self.activity_type.slug}"


class TopicPreference(models.Model):
    """A topic (taxonomy CATEGORY, e.g. "sport", "reading") a user wants the suggestion feed to
    steer toward — the user's hand on the algorithm (inv.2: no black-box, no behavioural
    inference). STATED, never inferred from behaviour, exactly like ``UserInterest``; the
    difference is granularity (category vs activity type) and intent (it tunes *what is
    surfaced*, not what is joinable).

    It is a SOFT signal only: it re-orders and honestly labels cohort-visible suggestions but
    NEVER hides anything (mirrors ``places.AccessPreference``) — so it can't quietly narrow a
    child's world or become an engagement lever. For a CHILD ward an active guardian may set it
    too ("the responsible person controls the feed"); the HARD child-safety category ENVELOPE
    stays ``accounts.GuardianGuardrail.allowed_categories`` (join/create gate), a separate
    concern. One row per (user, category)."""

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="topic_preferences"
    )
    category = models.ForeignKey(
        ActivityCategory, on_delete=models.CASCADE, related_name="preferring_users"
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(fields=["user", "category"], name="uq_user_topic_pref"),
        ]

    def __str__(self):
        return f"{self.user} -> #{self.category.slug}"


class ActivityEmbedding(models.Model):
    """The content vector for an activity (derived from its taxonomy), for similarity."""

    activity = models.OneToOneField(Activity, on_delete=models.CASCADE, related_name="embedding")
    vector = VectorField(dimensions=EMBEDDING_DIM)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [
            # ANN index so cosine-similarity ranking stays sub-linear as the
            # embedding table grows (matches the cosine distance used in ranking).
            HnswIndex(
                name="actemb_vector_hnsw",
                fields=["vector"],
                m=16,
                ef_construction=64,
                opclasses=["vector_cosine_ops"],
            ),
        ]

    def __str__(self):
        return f"embedding({self.activity_id})"
