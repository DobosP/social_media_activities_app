"""Taxonomy domain helpers.

The category-ancestry walk lives here as the SINGLE source of truth: both the recommendations
embedding tokens (``apps.recommendations.embeddings``) and the W3-F2 child category-allowlist
gate (``apps.social.services.category_envelope_allows``) consume it, so the embedding signal and
the child-safety gate can never drift apart.
"""

# Depth cap guards against a (malformed) cyclic category.parent chain.
CATEGORY_ANCESTRY_MAX_DEPTH = 16


def category_ancestry_slugs(activity_type) -> list[str]:
    """The activity type's own category slug plus its parent-category ancestry, nearest-first
    (depth-capped). Empty when the type has no category. Pure read; no DB writes."""
    slugs: list[str] = []
    category = activity_type.category
    depth = 0
    while category is not None and depth < CATEGORY_ANCESTRY_MAX_DEPTH:
        slugs.append(category.slug)
        category = category.parent
        depth += 1
    return slugs
