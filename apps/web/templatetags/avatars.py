"""Template filter for the generated avatar. Usage: ``{{ user|avatar_uri }}`` (a User → their
interest-graph *constellation*, see ``apps.recommendations.services.interest_graph``) or
``{{ seed_string|avatar_uri }}`` (a bare seed → the identicon fallback).

For a *list* of users, call ``apps.recommendations.services.attach_interest_nodes(...)`` in the
view first so each avatar renders from prefetched interests instead of one query per row."""

from django import template

from apps.accounts.avatars import identicon_data_uri

register = template.Library()


@register.filter(name="avatar_uri")
def avatar_uri(value):
    """Data-URI avatar for a User (interest constellation, or identicon when no interests declared)
    or a bare seed string (identicon)."""
    if getattr(value, "pk", None) is not None:
        # Lazy import: recommendations.services pulls in the recommendation stack, which must not be
        # imported while the template-tag library is being registered at app startup.
        from apps.recommendations.services import interest_avatar_data_uri

        return interest_avatar_data_uri(value)
    seed = getattr(value, "username", value)
    return identicon_data_uri(str(seed or "?"))
