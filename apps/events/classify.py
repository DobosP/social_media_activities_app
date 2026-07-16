"""Classify a free-text event (title/description) to an ActivityType using the
taxonomy's own names/slugs/aliases — so a "Maratonul Internațional" becomes `marathon`
and "Zilele Clujului" becomes `city_day`, making events filterable by activity."""

import re

from apps.taxonomy.models import ActivityType

_ROEDU_CATEGORY_SLUGS = {
    "theatre": "theatre_show",
    "concert": "concert",
    "exhibition": "museum_visit",
    "film": "open_air_cinema",
    "festival": "festival",
    "conference": "community_event",
    "workshop": "workshop",
    "literature": "reading",
    "dance": "dance_social",
    "opera": "theatre_show",
    "family": "community_event",
}


def _keyword_index() -> list[tuple[str, ActivityType]]:
    index: list[tuple[str, ActivityType]] = []
    for activity in ActivityType.objects.filter(is_active=True):
        terms = {activity.slug.replace("_", " "), activity.name.lower()}
        terms.update(a.lower() for a in (activity.aliases or []))
        for term in terms:
            term = term.strip().lower()
            if len(term) >= 3:
                index.append((term, activity))
    # Match longer (more specific) keywords first.
    index.sort(key=lambda kv: len(kv[0]), reverse=True)
    return index


def classify_activity(text: str) -> ActivityType | None:
    """Return the best-matching ActivityType for the text, or None."""
    if not text:
        return None
    haystack = text.lower()
    for keyword, activity in _keyword_index():
        # Leading word-boundary + prefix match, so inflected forms match too
        # (e.g. Romanian "maratonul" → "maraton", "alergare" → "alergare").
        if re.search(rf"(?<!\w){re.escape(keyword)}", haystack):
            return activity
    return None


def classify_roedu_activity(category: str, title: str) -> ActivityType | None:
    """Map the producer's stable event category before falling back to title text.

    Broad ``sports`` and ``other`` categories deliberately do not map to an
    invented generic type: the title classifier may still find an exact sport.
    """
    category = (category or "").strip().lower()
    slug = _ROEDU_CATEGORY_SLUGS.get(category)
    if slug:
        resolved = ActivityType.objects.filter(slug=slug, is_active=True).first()
        if resolved is not None:
            return resolved
    return classify_activity(title)
