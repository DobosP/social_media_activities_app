"""Avatar styles / signature avatars (ADR-0027): the orbits (Gen-2) renderer honours every
Gen-1 contract (deterministic, intensity==0 byte-identity, namespaced ids, no label/seed
leakage, salt-effective at any interest count); picking a style is uniqueness-guaranteed via
the fingerprint registry with salt retry; users who never pick render byte-identical to the
legacy pipeline; the fingerprint never reaches the audit log or any HTTP surface; and list
rendering stays non-N+1."""

import hashlib
import re
import xml.etree.ElementTree as ET

import pytest
from django.test import Client
from django.test.utils import CaptureQueriesContext

from apps.accounts import signature
from apps.accounts.avatars import (
    CANONICAL_PX,
    DEFAULT_GENERATION,
    FINGERPRINT_UID,
    GENERATIONS,
    constellation_svg,
    identicon_svg,
    orbits_svg,
    render_generation,
    signature_seed,
)
from apps.accounts.models import SignatureAvatar, User
from apps.accounts.signature import (
    AvatarStyleError,
    avatar_style_info,
    refresh_avatar_fingerprint,
    set_avatar_style,
)
from apps.safety.models import AuditLog
from apps.taxonomy.models import ActivityCategory, ActivityType

NODES = [
    {"slug": "basketball", "name": "Basketball", "category": "team_sport", "color": "#ff7a45"},
    {"slug": "football", "name": "Football", "category": "team_sport", "color": "#ff7a45"},
    {"slug": "hiking", "name": "Hiking", "category": "outdoor", "color": "#52c41a"},
]
EDGES = [(0, 1)]


# --- Orbits renderer: pure contract ---------------------------------------------------------


def test_orbits_deterministic_and_wellformed():
    a = orbits_svg("maria", NODES, EDGES, px=96)
    assert a == orbits_svg("maria", NODES, EDGES, px=96)
    root = ET.fromstring(a)
    assert root.tag.endswith("svg") and root.attrib["viewBox"] == "0 0 96 96"


def test_orbits_distinct_seeds_differ():
    assert orbits_svg("maria", NODES, EDGES) != orbits_svg("alex", NODES, EDGES)


def test_orbits_one_planet_per_interest_one_ring_per_category():
    svg = orbits_svg("maria", NODES, EDGES)
    # Each planet group carries a specular dot; rings are non-dashed ellipses.
    assert svg.count('opacity="0.85"/></g>') == len(NODES)
    assert svg.count("<ellipse") == 2  # team_sport + outdoor


def test_orbits_zero_interests_still_draws_sun_and_dust_ring():
    svg = orbits_svg("radu", [], [], px=96)
    assert "stroke-dasharray" in svg and "_sun" in svg


def test_orbits_salt_changes_bytes_at_any_interest_count():
    for nodes in ([], NODES[:1], NODES):
        s0 = orbits_svg(signature_seed("maria", 2, 0), nodes, [], px=96)
        s1 = orbits_svg(signature_seed("maria", 2, 1), nodes, [], px=96)
        assert s0 != s1, f"salt must re-roll the layout at n={len(nodes)}"


def test_orbits_intensity_zero_is_byte_identical_and_flourish_appends_only():
    base = orbits_svg("maria", NODES, EDGES, px=96)
    assert orbits_svg("maria", NODES, EDGES, px=96, intensity=0.0) == base
    lit = orbits_svg("maria", NODES, EDGES, px=96, intensity=0.8)
    assert lit != base
    # Append-only: everything before the flourish is untouched (public renders stay leak-free).
    assert lit.startswith(base[: -len("</svg>")])


def test_orbits_never_leaks_seed_or_labels():
    svg = orbits_svg("maria-secret-handle", NODES, EDGES)
    assert "maria" not in svg
    for node in NODES:
        assert node["name"] not in svg and node["slug"] not in svg


def test_orbits_uid_override_pins_the_id_namespace():
    a = orbits_svg("maria", NODES, EDGES, _uid_override=FINGERPRINT_UID)
    b = orbits_svg("alex", NODES, EDGES, _uid_override=FINGERPRINT_UID)
    assert f'id="{FINGERPRINT_UID}_bg"' in a and f'id="{FINGERPRINT_UID}_bg"' in b
    # Same fixed namespace, so any remaining difference is purely visual (seeded layout).
    assert a != b


def test_constellation_uid_override_default_reproduces_legacy_bytes():
    legacy_uid = hashlib.sha256(f"maria|80|{len(NODES)}".encode()).hexdigest()[:8]
    svg = constellation_svg("maria", NODES, EDGES)
    assert f'id="{legacy_uid}_sky"' in svg
    pinned = constellation_svg("maria", NODES, EDGES, _uid_override=FINGERPRINT_UID)
    assert f'id="{FINGERPRINT_UID}_sky"' in pinned and legacy_uid not in pinned


def test_render_generation_dispatch():
    assert "<rect" in render_generation(1, "zoe", [], [])  # gen-1 n==0 -> identicon
    assert render_generation(1, "zoe", NODES, EDGES) == constellation_svg("zoe", NODES, EDGES)
    assert render_generation(2, "zoe", NODES, EDGES) == orbits_svg("zoe", NODES, EDGES)


def test_registry_shape():
    assert DEFAULT_GENERATION == 1 and set(GENERATIONS) == {1, 2}


# --- DB fixtures ------------------------------------------------------------------------------


@pytest.fixture
def types(db):
    cat = ActivityCategory.objects.create(slug="sig-sport", name="Sport")
    out = ActivityCategory.objects.create(slug="sig-outdoor", name="Outdoor")
    ActivityType.objects.create(slug="sig-bball", name="Basketball", category=cat)
    ActivityType.objects.create(slug="sig-foot", name="Football", category=cat)
    ActivityType.objects.create(slug="sig-hike", name="Hiking", category=out)
    return ["sig-bball", "sig-foot", "sig-hike"]


def _user(name):
    return User.objects.create_user(username=name, password="pw-12345", display_name=name)


# --- Picking a style: registry semantics ------------------------------------------------------


@pytest.mark.django_db
def test_pick_creates_row_with_real_fingerprint_and_generation():
    u = _user("sig-ana")
    row = set_avatar_style(u, 2)
    assert row.generation == 2 and row.salt == 0
    assert row.fingerprint != signature._placeholder_fingerprint(u)
    assert row.fingerprint == signature._canonical_fingerprint(u, 2, 0)


@pytest.mark.django_db
def test_pick_audits_generation_but_never_the_fingerprint():
    u = _user("sig-bob")
    row = set_avatar_style(u, 2)
    log = AuditLog.objects.filter(event="avatar.style_changed").latest("id")
    assert log.data.get("generation") == 2
    assert row.fingerprint not in str(log.data)  # GDPR: the permanent log must not re-identify


@pytest.mark.django_db
def test_repick_same_style_is_a_stable_noop():
    u = _user("sig-cri")
    row = set_avatar_style(u, 2)
    audits = AuditLog.objects.filter(event="avatar.style_changed").count()
    again = set_avatar_style(u, 2)
    assert (again.salt, again.fingerprint) == (row.salt, row.fingerprint)
    assert AuditLog.objects.filter(event="avatar.style_changed").count() == audits


@pytest.mark.django_db
def test_unknown_or_malformed_generation_rejected():
    u = _user("sig-dan")
    for bad in (99, "nope", None):
        with pytest.raises(AvatarStyleError):
            set_avatar_style(u, bad)
    assert not SignatureAvatar.objects.filter(user=u).exists()


@pytest.mark.django_db
def test_fingerprint_collision_bumps_salt(monkeypatch):
    """Force the canonical fingerprint to collide at salt 0 (the real event is the
    zero-interest identicon birthday, unreachable deterministically in a test)."""
    a, b = _user("sig-eva"), _user("sig-flo")
    set_avatar_style(a, 1)  # a's row occupies a's salt-0 fingerprint

    real = signature._canonical_fingerprint
    a_fp0 = SignatureAvatar.objects.get(user=a).fingerprint

    def colliding(user, generation, salt):
        if user == b and salt == 0:
            return a_fp0  # b's salt-0 canonical render "collides" with a's
        return real(user, generation, salt)

    monkeypatch.setattr(signature, "_canonical_fingerprint", colliding)
    row = set_avatar_style(b, 1)
    assert row.salt == 1
    assert row.fingerprint == real(b, 1, 1)


@pytest.mark.django_db
def test_salt_exhaustion_raises(monkeypatch):
    u, holder = _user("sig-gia"), _user("sig-hol")
    SignatureAvatar.objects.create(user=holder, generation=1, fingerprint="f" * 64)
    monkeypatch.setattr(signature, "_canonical_fingerprint", lambda *a, **k: "f" * 64)
    with pytest.raises(AvatarStyleError):
        set_avatar_style(u, 1)
    assert not SignatureAvatar.objects.filter(user=u).exists()  # atomic: nothing half-minted


@pytest.mark.django_db
def test_erasure_cascades_the_row():
    from apps.accounts.services import erase_user

    u = _user("sig-ion")
    fp = set_avatar_style(u, 2).fingerprint
    erase_user(u, u)
    assert not User.objects.filter(username="sig-ion").exists()
    # GDPR Art.17: the pick and its fingerprint die with the account.
    assert not SignatureAvatar.objects.filter(fingerprint=fp).exists()


# --- Interest edits re-fingerprint (and only for users who picked) ----------------------------


@pytest.mark.django_db
def test_set_interests_refreshes_fingerprint_for_picked_users(types):
    from apps.recommendations.services import set_interests

    u = _user("sig-jan")
    set_interests(u, types[:1])
    row = set_avatar_style(u, 2)
    fp1 = row.fingerprint
    set_interests(u, types)  # more interests -> different canonical render
    row.refresh_from_db()
    assert row.fingerprint != fp1
    assert row.fingerprint == signature._canonical_fingerprint(u, 2, row.salt)


@pytest.mark.django_db
def test_set_interests_never_mints_for_unpicked_users(types):
    from apps.recommendations.services import set_interests

    u = _user("sig-kat")
    set_interests(u, types)
    assert not SignatureAvatar.objects.filter(user=u).exists()  # seeding stays side-effect-free


@pytest.mark.django_db
def test_refresh_is_noop_without_row():
    u = _user("sig-lia")
    assert refresh_avatar_fingerprint(u) is None
    assert not SignatureAvatar.objects.filter(user=u).exists()


# --- Render resolution: legacy byte-stability + picked styles ---------------------------------


@pytest.mark.django_db
def test_unpicked_users_render_byte_identical_to_legacy(types):
    from apps.recommendations.services import interest_avatar_svg, set_interests

    u = _user("sig-mia")
    assert interest_avatar_svg(u) == identicon_svg("sig-mia")  # no interests -> identicon
    set_interests(u, types)
    from apps.recommendations.services import interest_graph

    nodes, edges = interest_graph(u)
    assert interest_avatar_svg(u) == constellation_svg("sig-mia", nodes, edges)


@pytest.mark.django_db
def test_picked_generation_renders_everywhere(types):
    from apps.recommendations.services import interest_avatar_svg, set_interests

    u = _user("sig-nel")
    set_interests(u, types)
    set_avatar_style(u, 2)
    u = User.objects.get(pk=u.pk)  # drop per-instance caches
    from apps.recommendations.services import interest_graph

    nodes, edges = interest_graph(u)
    expected = orbits_svg(signature_seed("sig-nel", 2, 0), nodes, edges)
    assert interest_avatar_svg(u) == expected


@pytest.mark.django_db
def test_gen1_pick_is_a_visual_noop_for_legacy_users(types):
    """Review MED regression pin: Generation 1 is labelled "current" for row-less users, so
    picking it must not change their picture anywhere — gen1/salt0 seed IS the bare seed."""
    from apps.recommendations.services import interest_avatar_svg, set_interests

    plain = _user("sig-oto")
    before = interest_avatar_svg(plain)
    set_avatar_style(plain, 1)
    plain = User.objects.get(pk=plain.pk)
    assert interest_avatar_svg(plain) == before == identicon_svg("sig-oto")

    rich = _user("sig-uta")
    set_interests(rich, types)
    before = interest_avatar_svg(rich)
    set_avatar_style(rich, 1)
    rich = User.objects.get(pk=rich.pk)
    assert interest_avatar_svg(rich) == before


@pytest.mark.django_db
def test_gen1_preview_matches_live_avatar_for_legacy_users():
    """Preview honesty: the style marked "current" must preview as what the user sees NOW."""
    from apps.recommendations.services import avatar_style_previews, interest_avatar_svg

    u = _user("sig-pre")
    previews = {p["generation"]: p for p in avatar_style_previews(u, px=80)}
    assert previews[1]["current"] is True
    import base64 as b64

    shown = b64.b64decode(previews[1]["uri"].split(",", 1)[1]).decode()
    assert shown == interest_avatar_svg(u, px=80)


@pytest.mark.django_db
def test_unknown_generation_in_db_degrades_to_default_not_500():
    """Forward-compat: a deprecated pick still in a DB row renders the default look and
    reads as the default style — never a KeyError on public surfaces or /me."""
    from apps.recommendations.services import interest_avatar_svg

    u = _user("sig-old")
    SignatureAvatar.objects.create(user=u, generation=77, fingerprint="a" * 64)
    u = User.objects.get(pk=u.pk)
    assert interest_avatar_svg(u) == identicon_svg("sig-old")  # salt-0 default == legacy bytes
    assert avatar_style_info(u)["generation"] == DEFAULT_GENERATION
    c = Client()
    assert c.login(username="sig-old", password="pw-12345")
    assert c.get("/api/accounts/me/").status_code == 200


@pytest.mark.django_db
def test_refresh_keeps_an_established_salt(types, monkeypatch):
    """Layout continuity: a collision-bumped salt is tried FIRST on refresh, so an interest
    edit never resets it back to a lower free salt."""
    from apps.recommendations.services import set_interests

    holder, u = _user("sig-hold2"), _user("sig-salt2")
    set_avatar_style(holder, 1)
    real = signature._canonical_fingerprint
    blocked = SignatureAvatar.objects.get(user=holder).fingerprint

    def colliding(user, generation, salt):
        if user == u and salt == 0 and not UserInterest_exists(user):
            return blocked  # collide at salt 0 only while u has no interests
        return real(user, generation, salt)

    from apps.recommendations.models import UserInterest

    def UserInterest_exists(user):
        return UserInterest.objects.filter(user=user).exists()

    monkeypatch.setattr(signature, "_canonical_fingerprint", colliding)
    row = set_avatar_style(u, 1)
    assert row.salt == 1  # bumped past the collision
    set_interests(u, types)  # visuals change; salt-0 fingerprint is now free again
    row.refresh_from_db()
    assert row.salt == 1  # current salt tried first -> established layout family kept


@pytest.mark.django_db
def test_export_carries_style_generation_but_no_internals():
    from apps.accounts.export import build_user_export

    u = _user("sig-exp")
    set_avatar_style(u, 2)
    export = build_user_export(u)
    assert export["privacy_settings"]["avatar_style"] == {"generation": 2, "name": "Orbits"}
    row = SignatureAvatar.objects.get(user=u)
    text = str(export)
    assert row.fingerprint not in text and "salt" not in str(export["privacy_settings"])


@pytest.mark.django_db
def test_attach_interest_nodes_keeps_list_rendering_query_free(types, django_assert_num_queries):
    from django.db import connection

    from apps.recommendations.services import (
        attach_interest_nodes,
        interest_avatar_data_uri,
        set_interests,
    )

    users = [_user(f"sig-batch{i}") for i in range(4)]
    for u in users[:2]:
        set_interests(u, types[:2])
    set_avatar_style(users[1], 2)
    fresh = list(User.objects.filter(username__startswith="sig-batch"))
    with CaptureQueriesContext(connection) as ctx:
        attach_interest_nodes(fresh)
    assert len(ctx) == 2  # interests + style picks, regardless of batch size
    with django_assert_num_queries(0):  # rendering after attach hits only the caches
        for u in fresh:
            interest_avatar_data_uri(u)


# --- Canonical fingerprint properties ----------------------------------------------------------


@pytest.mark.django_db
def test_canonical_fingerprint_is_visual_only_and_size_pinned(types):
    """Two users with identical interests + a pinned uid differ ONLY through the seeded
    layout — and the fingerprint sees exactly the canonical render at CANONICAL_PX."""
    from apps.recommendations.services import set_interests

    a, b = _user("sig-pia"), _user("sig-rux")
    for u in (a, b):
        set_interests(u, types)
    fp_a = signature._canonical_fingerprint(a, 2, 0)
    assert fp_a == signature._canonical_fingerprint(a, 2, 0)
    assert fp_a != signature._canonical_fingerprint(b, 2, 0)
    from apps.recommendations.services import interest_graph

    nodes, edges = interest_graph(a)
    svg = render_generation(
        2,
        signature_seed("sig-pia", 2, 0),
        nodes,
        edges,
        px=CANONICAL_PX,
        intensity=0.0,
        _uid_override=FINGERPRINT_UID,
    )
    assert fp_a == hashlib.sha256(svg.encode()).hexdigest()


# --- HTTP surfaces: API + web, and the no-fingerprint guarantee --------------------------------


def _login(username):
    _user(username)
    c = Client()
    assert c.login(username=username, password="pw-12345")
    return c


def _assert_no_fingerprint(payload):
    text = str(payload)
    assert "fingerprint" not in text
    assert not re.search(r"\b[0-9a-f]{64}\b", text), "no 64-hex serial may reach a surface"


@pytest.mark.django_db
def test_api_get_and_pick_avatar_style():
    c = _login("sig-api")
    r = c.get("/api/accounts/me/avatar-style/")
    assert r.status_code == 200
    body = r.json()
    assert body["generation"] == 1  # legacy default reads as Generation 1
    assert {o["generation"] for o in body["available"]} == {1, 2}
    assert [p["generation"] for p in body["previews"]] == [1, 2]
    assert all(p["uri"].startswith("data:image/svg+xml;base64,") for p in body["previews"])
    _assert_no_fingerprint(body)

    r = c.post("/api/accounts/me/avatar-style/", {"generation": 2})
    assert r.status_code == 200 and r.json()["generation"] == 2
    _assert_no_fingerprint(r.json())
    assert SignatureAvatar.objects.get(user__username="sig-api").generation == 2

    assert c.post("/api/accounts/me/avatar-style/", {"generation": 99}).status_code == 400


@pytest.mark.django_db
def test_me_payload_carries_style_but_no_fingerprint():
    c = _login("sig-me")
    body = c.get("/api/accounts/me/").json()
    assert body["avatar_style"]["generation"] == 1
    assert body["avatar_style"]["generation_name"] == "Constellation"
    _assert_no_fingerprint(body)


@pytest.mark.django_db
def test_api_style_rate_limit(settings):
    settings.AVATAR_STYLE_RATE_LIMIT = 1
    c = _login("sig-rate")
    assert c.post("/api/accounts/me/avatar-style/", {"generation": 2}).status_code == 200
    assert c.post("/api/accounts/me/avatar-style/", {"generation": 1}).status_code == 429


@pytest.mark.django_db
def test_web_profile_shows_picker_and_pick_flow_works():
    c = _login("sig-web")
    page = c.get("/profile/").content.decode()
    assert "Your avatar style" in page and "Orbits" in page and "Constellation" in page
    _assert_no_fingerprint(page)
    r = c.post("/profile/avatar-style/", {"generation": 2})
    assert r.status_code == 302
    assert SignatureAvatar.objects.get(user__username="sig-web").generation == 2
    page = c.get("/profile/").content.decode()
    assert "Orbits" in page
    _assert_no_fingerprint(page)


@pytest.mark.django_db
def test_web_pick_of_unknown_style_shows_error_not_row():
    c = _login("sig-weberr")
    r = c.post("/profile/avatar-style/", {"generation": 42})
    assert r.status_code == 302
    assert not SignatureAvatar.objects.filter(user__username="sig-weberr").exists()


@pytest.mark.django_db
def test_avatar_style_info_shape():
    u = _user("sig-info")
    info = avatar_style_info(u)
    assert info == {
        "generation": 1,
        "generation_name": "Constellation",
        "available": [
            {"generation": 1, "name": "Constellation"},
            {"generation": 2, "name": "Orbits"},
        ],
    }


# --- Concurrency: two simultaneous first picks converge to one row ----------------------------


@pytest.mark.django_db(transaction=True)
def test_concurrent_first_picks_converge_to_one_row():
    import threading

    from django.db import connections

    u = _user("sig-race")
    errors = []

    def pick(gen):
        try:
            set_avatar_style(User.objects.get(pk=u.pk), gen)
        except Exception as exc:  # pragma: no cover - failure recorded for the assert below
            errors.append(exc)
        finally:
            connections.close_all()

    t1 = threading.Thread(target=pick, args=(1,))
    t2 = threading.Thread(target=pick, args=(2,))
    t1.start(), t2.start()
    t1.join(timeout=20), t2.join(timeout=20)
    assert not errors
    rows = SignatureAvatar.objects.filter(user=u)
    assert rows.count() == 1
    assert rows.get().generation in (1, 2)
