"""Template filter for the generated identicon avatar. Usage: ``{{ user|avatar_uri }}`` or
``{{ username_string|avatar_uri }}`` in an ``<img src="...">``. Pure function of the seed — no DB
hit, no storage."""

from django import template

from apps.accounts.avatars import identicon_data_uri

register = template.Library()


@register.filter(name="avatar_uri")
def avatar_uri(value):
    """Identicon data-URI for a User (seeded by username) or a bare seed string."""
    seed = getattr(value, "username", value)
    return identicon_data_uri(str(seed or "?"))
