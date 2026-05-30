"""Generated identicon avatars: deterministic (same seed -> same image), distinct across seeds,
safe SVG data-URIs, and reachable through the web template filter and the chat/profile surfaces."""

import pytest
from django.test import Client

from apps.accounts.avatars import identicon_data_uri, identicon_svg
from apps.accounts.models import User
from apps.web.templatetags.avatars import avatar_uri


def test_deterministic_same_seed_same_uri():
    assert identicon_data_uri("alice") == identicon_data_uri("alice")
    assert identicon_svg("alice") == identicon_svg("alice")


def test_distinct_seeds_distinct_avatars():
    assert identicon_data_uri("alice") != identicon_data_uri("bob")


def test_data_uri_is_svg_base64():
    uri = identicon_data_uri("carol")
    assert uri.startswith("data:image/svg+xml;base64,")


def test_svg_is_wellformed_and_coloured():
    svg = identicon_svg("dave")
    assert svg.startswith("<svg") and "viewBox" in svg and "</svg>" in svg
    assert "<rect" in svg and "hsl(" in svg  # a background + a deterministic fill colour


def test_empty_seed_does_not_crash():
    assert identicon_data_uri("").startswith("data:image/svg+xml;base64,")
    assert identicon_data_uri(None).startswith("data:image/svg+xml;base64,")


def test_template_filter_accepts_user_or_string():
    class _U:
        username = "erin"

    assert avatar_uri(_U()) == identicon_data_uri("erin")
    assert avatar_uri("erin") == identicon_data_uri("erin")  # seed string also works


@pytest.mark.django_db
def test_profile_shows_generated_avatar_when_no_photo():
    u = User.objects.create_user(username="zoe", password="pw-12345", display_name="Zoe")
    c = Client()
    c.force_login(u)
    html = c.get("/profile/").content.decode()
    assert "data:image/svg+xml;base64," in html  # the generated identicon stands in for a photo


@pytest.mark.django_db
def test_chat_config_carries_my_generated_avatar():
    u = User.objects.create_user(username="yan", password="pw-12345", display_name="Yan")
    c = Client()
    c.force_login(u)
    html = c.get("/messages/").content.decode()
    assert identicon_data_uri("yan") in html  # me.avatar embedded in the messaging config
