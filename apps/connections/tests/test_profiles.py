"""Tiered profile visibility (ADR-0028): the tier truth table (vetoes are 404-equivalent
None; connection > shared context > stranger), the per-tier field discipline (nothing beyond
the matrix — never age band/cohort/progression/history), the minor clamp, the join-request
context, and the web/API surfaces incl. the hover partial and the roster block-filter."""

import re
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.connections import services as connections
from apps.connections.profiles import (
    TIER_CONNECTED,
    TIER_SHARED,
    TIER_STRANGER,
    profile_card,
    profile_tier,
    shares_group,
)
from apps.places.models import Place
from apps.safety.services import block_user
from apps.social.models import Group, GroupMembership, Membership
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _type(slug="prof-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="prof-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _activity(owner, slug="prof-bball", title="Pickup game"):
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title=title,
        starts_at=timezone.now() + timedelta(days=1),
    )


def _join(activity, user, *, role=Membership.Role.MEMBER, state=Membership.State.MEMBER):
    return Membership.objects.create(activity=activity, user=user, role=role, state=state)


def _share(a, b, slug="prof-bball", title="Pickup game"):
    act = _activity(a, slug, title)
    _join(act, b)
    return act


def _connect(a, b):
    _share(a, b)
    connections.request_connection(a, b)
    conn = connections.request_connection(b, a)  # reciprocal auto-accepts
    return conn


def _client(user):
    c = Client()
    c.force_login(user)
    return c


FORBIDDEN_CARD_PATTERNS = (
    "age_band",
    "cohort",
    "progression",
    "met_confirmed",
    "attendance",
    "last_seen",
    "date_joined",
    "fingerprint",
)


def _assert_field_discipline(card):
    text = str(card)
    for pat in FORBIDDEN_CARD_PATTERNS:
        assert pat not in text, f"card leaked forbidden field: {pat}"


# --- tier truth table -------------------------------------------------------------------------


def test_vetoes_are_none():
    a, b = _adult("prof-a"), _adult("prof-b")
    child = _child("prof-kid")
    # cross-cohort (adult vs child)
    assert profile_tier(a, child) is None
    # blocked either way
    _share(a, b)
    block_user(a, b)
    assert profile_tier(a, b) is None
    assert profile_tier(b, a) is None
    # self
    assert profile_tier(a, a) is None
    # unassigned cohort
    grey = User.objects.create_user(username="prof-grey", password=PW)
    assert profile_tier(a, grey) is None
    assert profile_tier(grey, a) is None
    # inactive target
    c = _adult("prof-c")
    c.is_active = False
    c.save(update_fields=["is_active"])
    assert profile_tier(a, c) is None


def test_stranger_shared_connected_ladder():
    a, b = _adult("prof-d"), _adult("prof-e")
    assert profile_tier(a, b) == TIER_STRANGER
    _share(a, b)
    assert profile_tier(a, b) == TIER_SHARED
    connections.request_connection(a, b)
    connections.request_connection(b, a)
    assert profile_tier(a, b) == TIER_CONNECTED


def _area():
    from apps.communities.models import Area

    area, _ = Area.objects.get_or_create(
        slug="prof-area", defaults={"city": "Cluj-Napoca", "name": "Cluj-Napoca"}
    )
    return area


def test_shared_group_grants_shared_tier():
    a, b = _adult("prof-f"), _adult("prof-g")
    t = _type()
    group = Group.objects.create(
        owner=a,
        area=_area(),
        category=t.category,
        activity_type=t,
        tier=Group.Tier.TYPE,
        cohort=a.cohort,
        title="Chess circle",
    )
    GroupMembership.objects.create(group=group, user=a, role=GroupMembership.Role.OWNER)
    GroupMembership.objects.create(group=group, user=b)
    assert shares_group(a, b) and profile_tier(a, b) == TIER_SHARED
    # a LEFT membership no longer counts
    GroupMembership.objects.filter(user=b).update(state=GroupMembership.State.LEFT)
    assert not shares_group(a, b) and profile_tier(a, b) == TIER_STRANGER


def test_join_request_context_grants_shared_both_ways():
    organizer, requester = _adult("prof-h"), _adult("prof-i")
    act = _activity(organizer)
    _join(act, requester, state=Membership.State.REQUESTED)
    assert profile_tier(organizer, requester) == TIER_SHARED
    assert profile_tier(requester, organizer) == TIER_SHARED
    card = profile_card(organizer, requester)
    assert card["shared"]["join_request"] is True


def test_guardian_seat_never_creates_shared_context():
    a, b = _adult("prof-j"), _adult("prof-k")
    act = _activity(a)
    _join(act, b, role=Membership.Role.GUARDIAN)
    assert profile_tier(a, b) == TIER_STRANGER


# --- field discipline per tier ------------------------------------------------------------------


def test_stranger_card_is_minimal():
    a, b = _adult("prof-l"), _adult("prof-m")
    card = profile_card(a, b)
    assert card["tier"] == TIER_STRANGER
    assert set(card) == {"tier", "public_id", "display", "avatar", "minor"}
    assert card["avatar"].startswith("data:image/svg+xml;base64,")
    _assert_field_discipline(card)


def test_shared_card_fields():
    a, b = _adult("prof-n"), _adult("prof-o")
    _share(a, b, title="Sunday hoops")
    card = profile_card(a, b)
    assert card["tier"] == TIER_SHARED
    assert card["username"] == "prof-o" and card["verified"] is True
    assert card["shared"]["activities"] == ["Sunday hoops"]
    assert card["shared"]["activity_count"] == 1
    assert card["interests"] is None and card["show_photo"] is False
    assert card["can_connect"] is True and card["connected"] is False
    _assert_field_discipline(card)


def test_connected_card_adds_interests_and_photo_for_adults():
    from apps.recommendations.services import set_interests

    a, b = _adult("prof-p"), _adult("prof-q")
    _connect(a, b)
    set_interests(b, [_type().slug])
    card = profile_card(a, b)
    assert card["tier"] == TIER_CONNECTED
    assert card["connected"] is True and card["can_message"] is True
    assert card["interests"] == ["Basketball"]
    assert card["show_photo"] is True
    _assert_field_discipline(card)


def test_minor_clamp_never_adds_interests_or_photo():
    from apps.recommendations.services import set_interests

    a, b = _child("prof-r"), _child("prof-s")
    _share(a, b)
    connections.request_connection(a, b)
    connections.request_connection(b, a)
    assert profile_tier(a, b) == TIER_CONNECTED
    set_interests(b, [_type().slug])
    card = profile_card(a, b)
    assert card["minor"] is True
    assert card["interests"] is None and card["show_photo"] is False
    _assert_field_discipline(card)


# --- web surfaces ---------------------------------------------------------------------------------


def test_person_page_404_for_vetoes_and_renders_for_shared():
    a, b = _adult("prof-t"), _adult("prof-u")
    c = _client(a)
    stranger_page = c.get(f"/people/{b.public_id}/")
    assert stranger_page.status_code == 200  # owner decision: minimal card, not 404
    html = stranger_page.content.decode()
    assert "data:image/svg+xml;base64," in html  # the image is a MUST
    assert b.display_name in html
    assert "With you in" not in html

    _share(a, b, title="Sunday hoops")
    html = c.get(f"/people/{b.public_id}/").content.decode()
    assert "Sunday hoops" in html and "verified" in html

    block_user(a, b)
    assert c.get(f"/people/{b.public_id}/").status_code == 404
    # nonexistent id is the same 404
    assert c.get("/people/00000000-0000-0000-0000-000000000000/").status_code == 404


def test_person_page_self_redirects_to_profile():
    a = _adult("prof-v")
    resp = _client(a).get(f"/people/{a.public_id}/")
    assert resp.status_code == 302 and resp.url == "/profile/"


def test_hover_card_partial_and_rate_limit(settings):
    a, b = _adult("prof-w"), _adult("prof-x")
    _share(a, b, title="Sunday hoops")
    c = _client(a)
    resp = c.get(f"/people/{b.public_id}/card/")
    assert resp.status_code == 200
    html = resp.content.decode()
    assert "person-card" in html and "data:image/svg+xml;base64," in html
    assert "Sunday hoops" in html
    settings.PROFILE_CARD_RATE_LIMIT = 1
    fresh = _client(b)
    assert fresh.get(f"/people/{a.public_id}/card/").status_code == 200
    assert fresh.get(f"/people/{a.public_id}/card/").status_code == 429


def test_activity_roster_is_block_filtered_and_carries_avatars():
    a, b, d = _adult("prof-y"), _adult("prof-z"), _adult("prof-aa")
    act = _share(a, b)
    _join(act, d)
    c = _client(a)
    html = c.get(f"/activities/{act.pk}/").content.decode()
    assert b.display_name in html and d.display_name in html
    assert f'data-hovercard-user="{b.public_id}"' in html
    assert html.count("avatar avatar-xs") >= 3  # every member row carries the image
    block_user(a, d)
    html = c.get(f"/activities/{act.pk}/").content.decode()
    assert d.display_name not in html  # mutual invisibility on the DISPLAYED roster
    assert b.display_name in html


def test_api_person_card_matches_service_and_404s(settings):
    a, b = _adult("prof-ab"), _adult("prof-ac")
    _share(a, b, title="Sunday hoops")
    c = _client(a)
    body = c.get(f"/api/connections/people/{b.public_id}/").json()
    assert body["tier"] == TIER_SHARED and body["shared"]["activities"] == ["Sunday hoops"]
    assert not re.search(r"\b(age_band|cohort|progression)\b", str(body))
    block_user(b, a)
    assert c.get(f"/api/connections/people/{b.public_id}/").status_code == 404
    assert c.get(f"/api/connections/people/{a.public_id}/").status_code == 404  # self


def test_activity_detail_avatar_batch_stays_constant():
    """Constant-DELTA pin (review): the query count must not grow with member count — a
    broken attach batch would add ~2 queries per extra member and fail this exactly."""
    from django.db import connection
    from django.test.utils import CaptureQueriesContext

    a = _adult("prof-ad")
    act = _activity(a)
    _join(act, _adult("prof-m0"))
    c = _client(a)
    c.get(f"/activities/{act.pk}/")  # warm lazy one-offs (session, content types)
    with CaptureQueriesContext(connection) as small:
        c.get(f"/activities/{act.pk}/")
    for i in range(1, 6):
        _join(act, _adult(f"prof-m{i}"))
    with CaptureQueriesContext(connection) as large:
        c.get(f"/activities/{act.pk}/")
    assert len(large) == len(small), (
        f"query count grew with member count: {len(small)} -> {len(large)}"
    )


def test_stranger_with_blank_display_name_never_leaks_username():
    """Review MED: the username handle is a SHARED-tier field; a stranger card for a user
    with no display name shows a neutral placeholder instead."""
    a = _adult("prof-ae")
    b = _adult("prof-af")
    b.display_name = ""
    b.save(update_fields=["display_name"])
    card = profile_card(a, b)
    assert card["tier"] == TIER_STRANGER
    assert card["display"] == "A member"
    assert "prof-af" not in str(card)
    # once SHARED, the handle fallback is permitted again
    _share(a, b)
    assert profile_card(a, b)["display"] == "prof-af"


def test_profile_card_budget_is_shared_across_all_surfaces(settings):
    """Review MED: one anti-scrape budget for page + hover partial + API — exhausting it on
    one surface brakes the others."""
    settings.PROFILE_CARD_RATE_LIMIT = 2
    a, b = _adult("prof-ag"), _adult("prof-ah")
    _share(a, b)
    c = _client(a)
    assert c.get(f"/people/{b.public_id}/").status_code == 200
    assert c.get(f"/api/connections/people/{b.public_id}/").status_code == 200
    assert c.get(f"/people/{b.public_id}/card/").status_code == 429  # budget spent
    assert c.get(f"/people/{b.public_id}/").status_code == 429
    assert c.get(f"/api/connections/people/{b.public_id}/").status_code == 429


def test_shared_context_overflow_comes_from_service():
    a, b = _adult("prof-ai"), _adult("prof-aj")
    for i in range(5):
        _share(a, b, slug=f"prof-ovf{i}", title=f"Meetup {i}")
    card = profile_card(a, b)
    assert len(card["shared"]["activities"]) == 3
    assert card["shared"]["activity_count"] == 5
    assert card["shared"]["activity_overflow"] == 2
