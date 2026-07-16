"""ADR-0029 round-3 F3/F4: the reaction / dissent / concern / countless-footer surface wired to
GROUP threads (their primary home per the owner). Group posts now render through the SAME _post.html
partial as an activity, and the group_post_* endpoints mirror the activity trio + edit/delete under
the same shared write gate. These tests pin: the group e2e (member reacts/dissents/concerns via the
group endpoints), OWNER + MEMBER write parity, the non-member 404, the footer latch via recompute on
a group thread, the report link, the CHILD-group wall end-to-end, and the flattened one-open/one-tap
Respond menu (F1) on both surfaces (single <details>, no nested sheets, first-use concern intro).
"""

import re
from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.models import AgeBand, Cohort
from apps.communities.models import Area
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Membership, Post, PostConcern, PostDissent, PostReaction
from apps.social.tests.conftest import make_user
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


# --- helpers -----------------------------------------------------------------------------------


def _staff(username):
    u = make_user(username, AgeBand.ADULT)
    u.is_staff = True
    u.save(update_fields=["is_staff"])
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


@pytest.fixture
def area():
    return Area.objects.create(city="Cluj-Napoca", slug="grp-sent", name="Cluj-Napoca")


@pytest.fixture
def activity_type():
    cat, _ = ActivityCategory.objects.get_or_create(slug="gs-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="gs-bball", defaults={"name": "Basketball", "category": cat}
    )
    return atype


@pytest.fixture
def place():
    return Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def _adult_group(area, activity_type):
    """A staff-created ADULT group (owner auto-admitted MEMBER) + a joined adult member."""
    owner = _staff("gs_owner")
    group = social.create_group(
        owner, area=area, title="Cluj Basketball", activity_type=activity_type
    )
    member = make_user("gs_member", AgeBand.ADULT)
    social.join_group(member, group.id)
    return owner, member, group


def _child_group(area, activity_type):
    owner = _staff("gs_child_owner")
    group = social.create_group(
        owner, area=area, title="Kids Basketball", activity_type=activity_type, cohort=Cohort.CHILD
    )
    child = make_user("gs_child", AgeBand.UNDER_16, consented=True)
    social.join_group(child, group.id)
    return owner, child, group


def _url(group, post, action):
    return f"/groups/{group.id}/posts/{post.id}/{action}/"


# --- group e2e: member reacts / dissents / concerns via the group endpoints ---------------------


def test_group_member_can_react_dissent_concern_via_group_endpoints(area, activity_type):
    owner, member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(owner, group, "hi group")
    c = _client(member)
    facet = social.allowed_reactions()[0]

    r = c.post(_url(group, post, "react"), {"emoji": facet}, HTTP_X_REQUESTED_WITH="fetch")
    assert r.status_code == 200 and r.json()["ok"] is True
    assert PostReaction.objects.filter(post=post, user=member, emoji=facet).exists()

    r = c.post(_url(group, post, "dissent"), HTTP_X_REQUESTED_WITH="fetch")
    assert r.status_code == 200 and r.json() == {"ok": True, "mine": True}
    assert PostDissent.objects.filter(post=post, user=member).exists()

    r = c.post(_url(group, post, "concern"), HTTP_X_REQUESTED_WITH="fetch")
    assert r.status_code == 200 and r.json() == {"ok": True, "mine": True}
    assert PostConcern.objects.filter(post=post, user=member).exists()


def test_group_owner_and_member_both_write_capable(area, activity_type):
    # OWNER (staff, auto-admitted MEMBER) and a plain MEMBER can both react on a group post.
    owner, member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(member, group, "a peer post")
    facet = social.allowed_reactions()[0]
    for actor in (owner, member):
        r = _client(actor).post(
            _url(group, post, "react"), {"emoji": facet}, HTTP_X_REQUESTED_WITH="fetch"
        )
        assert r.status_code == 200 and r.json()["ok"] is True
    assert PostReaction.objects.filter(post=post, emoji=facet).count() == 2


def test_group_dissent_no_js_redirects_and_persists(area, activity_type):
    # The no-JS path (no X-Requested-With) toggles and 302-redirects back to the anchored post.
    owner, member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(owner, group, "hi")
    r = _client(member).post(_url(group, post, "dissent"))
    assert r.status_code == 302 and r.url == f"/groups/{group.id}/#post-{post.id}"
    assert PostDissent.objects.filter(post=post, user=member).exists()


def test_same_cohort_non_member_is_rejected_by_the_write_gate(area, activity_type):
    # A same-cohort non-member CAN retrieve a discoverable adult group (group_by_id is a discovery
    # surface), so the MEMBERSHIP wall is the service gate: toggle_reaction raises NotAMember ->
    # JSON 400 (no row). Mirrors the activity trio (membership is not a 404 here).
    owner, _member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(owner, group, "hi")
    outsider = make_user("gs_outsider", AgeBand.ADULT)
    r = _client(outsider).post(
        _url(group, post, "react"),
        {"emoji": social.allowed_reactions()[0]},
        HTTP_X_REQUESTED_WITH="fetch",
    )
    assert r.status_code == 400 and r.json()["ok"] is False
    assert not PostReaction.objects.filter(post=post, user=outsider).exists()


def test_cross_cohort_viewer_gets_a_clean_404(area, activity_type):
    # A CHILD viewer can never even retrieve an ADULT group (visible_groups is cohort-walled) -> the
    # endpoint 404s before the service is reached: a cross-cohort id is never a content leak.
    owner, _member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(owner, group, "hi")
    child = make_user("gs_cross_child", AgeBand.UNDER_16, consented=True)
    r = _client(child).post(_url(group, post, "dissent"), HTTP_X_REQUESTED_WITH="fetch")
    assert r.status_code == 404
    assert not PostDissent.objects.filter(post=post, user=child).exists()


# --- footer latch on a group thread via recompute -----------------------------------------------


def test_group_thread_footer_latches_via_recompute(area, activity_type, settings):
    settings.SENTIMENT_K_ADULT = 1  # one reactor + the 2-member audience (>= 2k) latches
    from apps.social.sentiment import recompute_post_sentiment
    from apps.social.services import REACTION_FACETS

    owner, member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(owner, group, "hi")
    facet = social.allowed_reactions()[0]
    social.toggle_reaction(member, post, facet)
    recompute_post_sentiment()

    html = _client(owner).get(f"/groups/{group.id}/").content.decode()  # author-parity render
    lines = re.findall(r'<p class="sentiment-line">(.*?)</p>', html)
    assert lines == [REACTION_FACETS[facet][2]]
    assert not any(ch.isdigit() for line in lines for ch in line)  # never a count/percentage


# --- CHILD-group wall: no footer, no dissent/concern UI, endpoints reject ------------------------


def test_child_group_renders_no_footer_no_dissent_concern_keeps_react_picker(area, activity_type):
    owner, child, group = _child_group(area, activity_type)
    # A raw post row (a minor thread is announcement-only, so bypass the write gate just to prove
    # the TEMPLATE cohort wall strips dissent/concern even when a post is present).
    post = Post.objects.create(thread=group.thread, author=child, body="hello")

    html = _client(child).get(f"/groups/{group.id}/").content.decode()
    assert f'id="post-{post.id}"' in html  # the post rendered through _post.html
    assert 'class="sentiment-line"' not in html  # never a footer on a CHILD thread
    assert "concern-intro" not in html  # no first-use education
    assert "I see this differently" not in html  # no dissent row
    assert "doesn't seem to fit here" not in html  # no concern row
    assert 'class="rx-pick"' in html  # the countless appreciation picker stays available


def test_child_group_dissent_and_concern_endpoints_rejected(area, activity_type):
    owner, child, group = _child_group(area, activity_type)
    post = Post.objects.create(thread=group.thread, author=child, body="hello")
    c = _client(child)
    for action, model in (("dissent", PostDissent), ("concern", PostConcern)):
        r = c.post(_url(group, post, action), HTTP_X_REQUESTED_WITH="fetch")
        assert r.status_code == 400 and r.json()["ok"] is False
        assert not model.objects.filter(post=post, user=child).exists()


# --- report link resolves for a group post ------------------------------------------------------


def test_group_post_report_link_resolves(area, activity_type):
    owner, member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(owner, group, "hi")
    html = _client(member).get(f"/groups/{group.id}/").content.decode()
    # The Report link (sole DSA Art-16 channel) targets this group post via the generic report view.
    assert f"/report/?type=post&amp;id={post.id}" in html


# --- flattened one-open/one-tap Respond menu (F1) on BOTH surfaces -------------------------------


def _adult_activity(place, activity_type):
    owner = make_user("gs_act_owner", AgeBand.ADULT)
    activity = social.create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Pickup game",
        starts_at=timezone.now() + timedelta(days=1),
    )
    member = make_user("gs_act_member", AgeBand.ADULT)
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return owner, member, activity


def _assert_flattened_menu(html):
    # ONE Respond menu, NO nested details / sheets (the old markup is gone), all three rows visible.
    assert html.count('class="respond-menu"') == 1
    assert "concern-item" not in html  # old nested <details> removed
    assert "dissent-sheet" not in html  # old sheet removed
    assert 'class="dissent-row"' in html
    assert 'class="concern-row"' in html
    assert "Reply with your view" in html  # primary dissent affordance
    assert "Quietly note" in html  # one-tap secondary dissent
    assert 'class="concern-intro' in html  # first-use education renders server-side (no-JS)
    assert "data-label-withdraw" in html  # the JS toggle-off label ships in the DOM


def test_flattened_respond_menu_on_activity(place, activity_type):
    owner, member, activity = _adult_activity(place, activity_type)
    social.post_to_thread(owner, activity, "hi")
    html = _client(member).get(f"/activities/{activity.id}/").content.decode()
    _assert_flattened_menu(html)


def test_flattened_respond_menu_on_group(area, activity_type):
    owner, member, group = _adult_group(area, activity_type)
    social.post_to_thread(owner, group, "hi")
    html = _client(member).get(f"/groups/{group.id}/").content.decode()
    _assert_flattened_menu(html)


def test_own_dissent_state_echoes_in_group_respond_menu_no_js(area, activity_type):
    # F2 no-JS reload: after a dissent toggle-ON the withdraw label + reply nudge render serverside.
    owner, member, group = _adult_group(area, activity_type)
    post = social.post_to_thread(owner, group, "hi")
    social.toggle_dissent(member, post)
    html = _client(member).get(f"/groups/{group.id}/").content.decode()
    assert "Noted quietly — tap to withdraw" in html
    assert "Want to say why? Reply." in html


# --- query-count guard: the F2 own-state helper is bulk (bounded, N-independent) ----------------


def test_dissent_concern_mine_is_two_queries_regardless_of_post_count(
    area, activity_type, django_assert_num_queries
):
    owner, member, group = _adult_group(area, activity_type)
    posts = [social.post_to_thread(owner, group, f"m{i}") for i in range(4)]
    with django_assert_num_queries(2):  # one PostDissent + one PostConcern query, never per-post
        social.dissent_concern_mine(posts, member)
