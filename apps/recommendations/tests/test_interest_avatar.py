"""Interest-graph avatar: build (nodes, edges) from a user's declared interests, render the
constellation through the avatar seam, fall back to the identicon with no interests, batch a list
of avatars without N+1, and flow through the |avatar_uri filter + the profile page."""

import base64

import pytest
from django.test import Client

from apps.accounts.avatars import identicon_data_uri
from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.recommendations import services
from apps.recommendations.services import (
    INTEREST_AVATAR_COLORS,
    attach_interest_nodes,
    interest_avatar_data_uri,
    interest_graph,
)
from apps.taxonomy.models import ActivityCategory, ActivityType
from apps.web.templatetags.avatars import avatar_uri

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _types():
    """Two team-sport types + one outdoor type. get_or_create keeps the palette-key category
    slugs even if the taxonomy seed already created them in the test DB."""
    team, _ = ActivityCategory.objects.get_or_create(
        slug="team_sport", defaults={"name": "Team Sport"}
    )
    outdoor, _ = ActivityCategory.objects.get_or_create(
        slug="outdoor", defaults={"name": "Outdoor"}
    )
    bball = ActivityType.objects.create(slug="av-bball", name="Basketball", category=team)
    foot = ActivityType.objects.create(slug="av-foot", name="Football", category=team)
    hike = ActivityType.objects.create(slug="av-hike", name="Hiking", category=outdoor)
    return bball, foot, hike


def test_interest_graph_nodes_slug_ordered_with_same_category_edges():
    u = _user("av-graph")
    _types()
    services.set_interests(u, ["av-bball", "av-foot", "av-hike"])
    nodes, edges = interest_graph(u)
    assert [n["slug"] for n in nodes] == ["av-bball", "av-foot", "av-hike"]  # deterministic order
    # colour comes from the category palette
    assert nodes[0]["color"] == INTEREST_AVATAR_COLORS["team_sport"]
    assert nodes[2]["color"] == INTEREST_AVATAR_COLORS["outdoor"]
    # the two team-sport interests are joined; the lone outdoor one is isolated
    assert edges == [(0, 1)]


def test_unknown_category_falls_back_to_default_colour():
    u = _user("av-unknown")
    cat = ActivityCategory.objects.create(slug="av-weird", name="Weird")
    ActivityType.objects.create(slug="av-thing", name="Thing", category=cat)
    services.set_interests(u, ["av-thing"])
    nodes, _ = interest_graph(u)
    assert nodes[0]["color"] == services._DEFAULT_AVATAR_COLOR


def test_no_interests_falls_back_to_identicon():
    u = _user("av-empty")
    # byte-identical to the old universal default, so cold-start accounts still get an avatar.
    assert interest_avatar_data_uri(u) == identicon_data_uri("av-empty")


def test_with_interests_renders_the_constellation():
    u = _user("av-stars")
    _types()
    services.set_interests(u, ["av-bball", "av-foot"])
    uri = interest_avatar_data_uri(u)
    assert uri.startswith("data:image/svg+xml;base64,")
    assert uri != identicon_data_uri("av-stars")
    svg = base64.b64decode(uri.split(",", 1)[1]).decode("utf-8")
    assert "radialGradient" in svg  # glowing stars
    assert "linearGradient" in svg  # the colour-lit edge between them


def test_avatar_does_not_leak_interest_names():
    u = _user("av-priv")
    _types()
    services.set_interests(u, ["av-bball", "av-foot", "av-hike"])
    svg = base64.b64decode(interest_avatar_data_uri(u).split(",", 1)[1]).decode("utf-8")
    assert "Basketball" not in svg and "av-bball" not in svg


def test_attach_interest_nodes_batches_then_renders_without_queries(django_assert_num_queries):
    _types()
    users = [_user(f"av-batch{i}") for i in range(4)]
    for u in users:
        services.set_interests(u, ["av-bball", "av-foot"])
    # Two fixed queries load every user's interests + avatar-style picks (ADR-0027) — constant
    # regardless of batch size; rendering then touches no DB (no N+1 on a list).
    with django_assert_num_queries(2):
        attach_interest_nodes(users)
    with django_assert_num_queries(0):
        uris = [interest_avatar_data_uri(u) for u in users]
    assert all(s.startswith("data:image/svg+xml;base64,") for s in uris)


def test_template_filter_renders_constellation_for_a_user():
    u = _user("av-filter")
    _types()
    services.set_interests(u, ["av-bball", "av-foot"])
    assert avatar_uri(u) == interest_avatar_data_uri(u)


def test_template_filter_falls_back_to_identicon_for_a_bare_string():
    assert avatar_uri("just-a-seed") == identicon_data_uri("just-a-seed")


def test_profile_page_embeds_the_users_constellation():
    u = _user("av-profile")
    _types()
    services.set_interests(u, ["av-bball", "av-foot"])
    c = Client()
    c.force_login(u)
    html = c.get("/profile/").content.decode()
    # the generated avatar shown when there is no uploaded photo is the interest constellation
    assert interest_avatar_data_uri(u) in html
