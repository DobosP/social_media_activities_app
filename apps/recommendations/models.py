"""P3 recommendations: declared interests + a content-based activity embedding.

Privacy by design (per docs/SAFETY.md): recommendations are computed ONLY from a user's
*declared* interests and the activities they've *joined* — never from behavioural tracking.
The embedding is a deterministic feature-hash of the activity taxonomy (no external model,
no PII), stored as a pgvector for cosine-similarity ranking.
"""

from django.conf import settings
from django.db import models
from pgvector.django import VectorField

from apps.social.models import Activity
from apps.taxonomy.models import ActivityType

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


class ActivityEmbedding(models.Model):
    """The content vector for an activity (derived from its taxonomy), for similarity."""

    activity = models.OneToOneField(Activity, on_delete=models.CASCADE, related_name="embedding")
    vector = VectorField(dimensions=EMBEDDING_DIM)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self):
        return f"embedding({self.activity_id})"
