"""Deterministic, PII-free content embeddings via feature hashing of the taxonomy.

A token set (the activity type + its category ancestors) is hashed into a fixed-dimension
vector and L2-normalised. No external model, no behavioural data — just the declared
taxonomy, so the same inputs always produce the same vector.
"""

import hashlib
import math

from apps.taxonomy.services import category_ancestry_slugs

from .models import EMBEDDING_DIM


def _slugs_for_type(activity_type) -> list[str]:
    """The type slug plus its category ancestry, as namespaced tokens. The ancestry walk is the
    shared ``taxonomy.category_ancestry_slugs`` helper, so the embedding tokens and the W3-F2
    child category-allowlist gate can never drift."""
    return [f"type:{activity_type.slug}"] + [
        f"cat:{slug}" for slug in category_ancestry_slugs(activity_type)
    ]


def hash_embed(tokens) -> list[float]:
    """Feature-hash tokens into an L2-normalised EMBEDDING_DIM vector (zeros if empty)."""
    vec = [0.0] * EMBEDDING_DIM
    for token in tokens:
        digest = hashlib.sha256(token.encode("utf-8")).digest()
        index = int.from_bytes(digest[:4], "big") % EMBEDDING_DIM
        vec[index] += 1.0 if digest[4] & 1 else -1.0
    norm = math.sqrt(sum(v * v for v in vec))
    if norm > 0:
        vec = [v / norm for v in vec]
    return vec


def activity_vector(activity) -> list[float]:
    return hash_embed(_slugs_for_type(activity.activity_type))


def user_vector(user) -> list[float]:
    """Built from declared interests + the types of activities the user has joined."""
    from apps.social.models import Membership

    from .models import UserInterest

    tokens: list[str] = []
    interests = UserInterest.objects.filter(user=user).select_related(
        "activity_type", "activity_type__category"
    )
    for interest in interests:
        tokens += _slugs_for_type(interest.activity_type)

    joined = Membership.objects.filter(user=user, state=Membership.State.MEMBER).select_related(
        "activity__activity_type", "activity__activity_type__category"
    )
    for membership in joined:
        tokens += _slugs_for_type(membership.activity.activity_type)

    return hash_embed(tokens)
