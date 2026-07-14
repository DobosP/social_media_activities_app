"""Thread reactions: ANONYMOUS + COUNTLESS — the per-post read surface exposes only the viewer's
OWN facet toggles (ADR-0029 removed the public distinct-facet set; the aggregate now lives solely
in the batched sentiment footer), never how many/who, under the same membership/consent gate as
posting. (Encrypted-DM reactions are who+what, but live client-side — not tested here.)"""

import re

import pytest
from django.test import Client
from django.utils import timezone

from apps.social import services as social
from apps.social.models import Activity, Membership, PostReaction

from .conftest import make_user


def _setup(place, activity_type):
    owner = make_user("rx_owner")
    member = make_user("rx_member")
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    Membership.objects.create(
        activity=activity, user=member, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return owner, member, activity


@pytest.mark.django_db
def test_toggle_adds_then_removes(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "nice")
    emoji = social.allowed_reactions()[0]
    assert social.toggle_reaction(member, post, emoji) is True
    assert PostReaction.objects.filter(post=post, user=member, emoji=emoji).exists()
    assert social.toggle_reaction(member, post, emoji) is False
    assert not PostReaction.objects.filter(post=post, user=member).exists()


@pytest.mark.django_db
def test_reaction_read_surface_is_mine_only_and_countless(place, activity_type):
    # ADR-0029: the per-post read surface exposes NEITHER a count NOR the distinct-facet "present"
    # set (it surfaced at n=1 — a small-roster leak). Only the viewer's OWN toggles come back; the
    # aggregate lives solely in the batched sentiment footer.
    owner, member, activity = _setup(place, activity_type)
    third = make_user("rx_third")
    Membership.objects.create(
        activity=activity, user=third, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    # three different members react with the SAME facet
    social.toggle_reaction(owner, post, e)
    social.toggle_reaction(member, post, e)
    social.toggle_reaction(third, post, e)
    rx = social.reactions_for_posts([post], member)[post.id]
    assert "present" not in rx  # no distinct-facet set, ever
    assert rx["mine"] == {e}  # only the viewer's own


@pytest.mark.django_db
def test_reactions_for_posts_shows_only_own_not_others(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e0, e1 = social.allowed_reactions()[0], social.allowed_reactions()[1]
    social.toggle_reaction(owner, post, e0)
    social.toggle_reaction(member, post, e1)
    # the MEMBER's view: 'mine' = only the member's own facet; the owner's facet is never revealed.
    rx = social.reactions_for_posts([post], member)[post.id]
    assert "present" not in rx
    assert rx["mine"] == {e1}  # never reveals that OWNER used e0


@pytest.mark.django_db
def test_gate_non_member_invalid_emoji_hidden_cancelled(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    outsider = make_user("rx_out")
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    with pytest.raises(social.NotAMember):
        social.toggle_reaction(outsider, post, e)
    # a supervisory guardian is read-only — cannot react
    guardian = make_user("rx_grd")
    Membership.objects.create(
        activity=activity,
        user=guardian,
        role=Membership.Role.GUARDIAN,
        state=Membership.State.MEMBER,
    )
    with pytest.raises(social.NotEligible):
        social.toggle_reaction(guardian, post, e)
    with pytest.raises(social.InvalidState):
        social.toggle_reaction(member, post, "🦄")  # custom emoji not in the fixed set
    # hidden post
    hidden = social.post_to_thread(owner, activity, "secret")
    social.delete_own_post(owner, hidden)
    with pytest.raises(social.InvalidState):
        social.toggle_reaction(member, hidden, e)
    # cancelled activity
    activity.status = Activity.Status.CANCELLED
    activity.save(update_fields=["status"])
    with pytest.raises(social.InvalidState):
        social.toggle_reaction(member, post, e)


@pytest.mark.django_db
def test_blocked_member_cannot_react(place, activity_type):
    # A block leaves Membership intact, so the reaction path must re-check it (parity with
    # post_to_thread) — otherwise a blocked member's emoji would surface on the owner's posts.
    from apps.safety.services import block_user

    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    block_user(member, owner)
    with pytest.raises(social.InvalidState):
        social.toggle_reaction(member, post, e)
    assert not PostReaction.objects.filter(post=post, user=member).exists()


@pytest.mark.django_db
def test_hidden_activity_cannot_react(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    activity.is_hidden = True
    activity.save(update_fields=["is_hidden"])
    with pytest.raises(social.InvalidState):
        social.toggle_reaction(member, post, e)


@pytest.mark.django_db
def test_add_path_is_idempotent_under_a_race(place, activity_type):
    # toggle_reaction's add path uses get_or_create so a duplicate insert (a racing double-tap
    # that landed a row between this request's filter and create) is a benign no-op instead of an
    # IntegrityError that would poison the surrounding atomic block. The unique constraint holds.
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    # simulate the racing request having already inserted the row, then this request adds:
    PostReaction.objects.create(post=post, user=member, emoji=e)
    _, created = PostReaction.objects.get_or_create(post=post, user=member, emoji=e)
    assert created is False
    assert PostReaction.objects.filter(post=post, user=member, emoji=e).count() == 1


@pytest.mark.django_db
def test_web_react_toggles_and_shows_no_count(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    c = Client()
    c.force_login(member)
    r = c.post(f"/activities/{activity.id}/post/{post.id}/react/", {"emoji": e})
    assert r.status_code == 302
    assert PostReaction.objects.filter(post=post, user=member, emoji=e).exists()
    page = Client()
    page.force_login(owner)
    html = page.get(f"/activities/{activity.id}/").content.decode()
    assert e in html  # the facet slug renders in the picker
    # ...and there is no "1" / count next to it — the surface is countless by construction
    assert ">1<" not in html.split(e, 1)[1][:30]


@pytest.mark.django_db
def test_web_react_json_response_is_mine_only(place, activity_type):
    # ADR-0029: activity_post_react's JSON contract dropped "present" entirely — the live (fetch)
    # client only ever learns the VIEWER's own toggle state, never a distinct-facet set/who-list.
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    c = Client()
    c.force_login(member)
    r = c.post(
        f"/activities/{activity.id}/post/{post.id}/react/",
        {"emoji": e},
        HTTP_X_REQUESTED_WITH="fetch",
    )
    assert r.status_code == 200
    body = r.json()
    assert body == {"ok": True, "mine": [e]}
    assert "present" not in body


@pytest.mark.django_db
def test_sentiment_footer_renders_latched_sentence_with_no_digits(place, activity_type, settings):
    # A latched appreciation sentence is the ONLY new aggregate surface (ADR-0029) — it must never
    # carry a count/percentage. Shrink the threshold so one reactor + a 2-member audience latches.
    settings.SENTIMENT_K_ADULT = 1
    from apps.social.sentiment import recompute_post_sentiment
    from apps.social.services import REACTION_FACETS

    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    social.toggle_reaction(member, post, e)
    recompute_post_sentiment()

    page = Client()
    page.force_login(owner)  # author-parity: the author's render carries the same footer
    html = page.get(f"/activities/{activity.id}/").content.decode()
    sentence = REACTION_FACETS[e][2]
    lines = re.findall(r'<p class="sentiment-line">(.*?)</p>', html)
    assert lines == [sentence]
    assert not any(ch.isdigit() for line in lines for ch in line)  # no count/percentage, ever
