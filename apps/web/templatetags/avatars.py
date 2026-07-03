"""Template filter for the generated avatar. Usage: ``{{ user|avatar_uri }}`` (a User → their
interest-graph *constellation*, see ``apps.recommendations.services.interest_graph``) or
``{{ seed_string|avatar_uri }}`` (a bare seed → the identicon fallback).

For a *list* of users, call ``apps.recommendations.services.attach_interest_nodes(...)`` in the
view first so each avatar renders from prefetched interests instead of one query per row."""

from django import template
from django.utils.safestring import mark_safe

from apps.accounts.avatars import activity_accent_svg, identicon_data_uri

register = template.Library()


@register.simple_tag
def activity_accent(activity):
    """Inline procedural SVG banner for an activity card (the focused Cards browse mode). Decorative
    generative art used when an activity has no cover photo. Deterministic from the activity's
    type + title.
    mark_safe is safe here: activity_accent_svg emits only numbers + hsl() colours + a hashed id
    namespace — no part of the (untrusted) seed string is ever written into the SVG markup."""
    atype = getattr(activity, "activity_type", None)
    slug = getattr(atype, "slug", "") or ""
    seed = f"{slug}:{getattr(activity, 'title', '')}"
    return mark_safe(activity_accent_svg(seed))  # noqa: S308 - server-composed SVG, no user HTML


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
