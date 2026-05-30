"""Thread reactions: ANONYMOUS + COUNTLESS — the read surface exposes only the distinct emojis
present (never how many, never who), the viewer's own toggles, and the same membership/consent
gate as posting. (Encrypted-DM reactions are who+what, but live client-side — not tested here.)"""

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
def test_present_emojis_are_distinct_and_countless(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    third = make_user("rx_third")
    Membership.objects.create(
        activity=activity, user=third, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    post = social.post_to_thread(owner, activity, "hi")
    e = social.allowed_reactions()[0]
    # three different members react with the SAME emoji
    social.toggle_reaction(owner, post, e)
    social.toggle_reaction(member, post, e)
    social.toggle_reaction(third, post, e)
    # the read surface shows the emoji ONCE — no count of the three reactors
    assert social.post_reaction_emojis(post) == [e]


@pytest.mark.django_db
def test_reactions_for_posts_shows_only_own_not_others(place, activity_type):
    owner, member, activity = _setup(place, activity_type)
    post = social.post_to_thread(owner, activity, "hi")
    e0, e1 = social.allowed_reactions()[0], social.allowed_reactions()[1]
    social.toggle_reaction(owner, post, e0)
    social.toggle_reaction(member, post, e1)
    # the MEMBER's view: 'present' = both emojis (distinct); 'mine' = only the member's own
    rx = social.reactions_for_posts([post], member)[post.id]
    assert set(rx["present"]) == {e0, e1}
    assert rx["mine"] == {e1}  # never reveals that OWNER used e0 as "mine"


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
    assert e in html  # the emoji chip renders
    # ...and there is no "1" / count next to it — the present list is countless by construction
    assert ">1<" not in html.split(e, 1)[1][:30]
