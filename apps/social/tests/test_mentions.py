"""@mentions: a calm highlight by default (tag-not-ping); an explicit author opt-in escalates to
a MENTION notification fanned out to PEER members only — never the author, a guardian, or a
blocked pair, and always still mutable by the recipient. Highlighting is HTML-safe (escape-first).
"""

import pytest
from django.test import Client
from django.utils import timezone

from apps.notifications.models import Notification
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import Membership

from .conftest import make_user


def _activity(owner, place, activity_type):
    return social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )


def _join(activity, user, role=Membership.Role.MEMBER):
    Membership.objects.create(
        activity=activity, user=user, role=role, state=Membership.State.MEMBER
    )


def _mentions(recipient):
    return Notification.objects.filter(recipient=recipient, kind=Notification.Kind.MENTION)


@pytest.mark.django_db
def test_resolve_finds_peers_case_insensitive_dedup_excludes_author(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    # case-insensitive, deduped, and the author naming themselves is dropped
    users = social.resolve_mentions(activity, "hi @BOB @bob and @alice", exclude_user=owner)
    assert [u.username for u in users] == ["bob"]


@pytest.mark.django_db
def test_resolve_excludes_guardians_and_non_members_and_emails(place, activity_type):
    owner = make_user("alice")
    grd = make_user("granny")
    make_user("mallory")  # a real user, but NOT a member of this activity
    activity = _activity(owner, place, activity_type)
    _join(activity, grd, role=Membership.Role.GUARDIAN)
    # guardian (supervisory, not a peer), a non-member, and an email's @ all resolve to nobody
    users = social.resolve_mentions(
        activity, "ping @granny @mallory write me at a@granny.com", exclude_user=owner
    )
    assert users == []


@pytest.mark.django_db
def test_ping_true_notifies_mentioned_peer_only(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    carol = make_user("carol")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    _join(activity, carol)
    social.post_to_thread(owner, activity, "over to @bob", ping=True)
    assert _mentions(bob).count() == 1
    assert _mentions(carol).count() == 0  # not mentioned
    assert _mentions(owner).count() == 0  # author never self-pings


@pytest.mark.django_db
def test_tag_not_ping_is_the_default(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    social.post_to_thread(owner, activity, "over to @bob")  # ping defaults False
    assert _mentions(bob).count() == 0  # tagged, not pinged


@pytest.mark.django_db
def test_ping_excludes_blocked_pair(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    block_user(bob, owner)  # bob blocked the author
    social.post_to_thread(owner, activity, "hey @bob", ping=True)
    assert _mentions(bob).count() == 0


@pytest.mark.django_db
def test_mention_is_mutable_recipient_can_silence(place, activity_type):
    from apps.notifications.services import set_muted_kinds

    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    set_muted_kinds(bob, [Notification.Kind.MENTION])
    social.post_to_thread(owner, activity, "yo @bob", ping=True)
    assert _mentions(bob).count() == 0  # the notify() mute gate suppressed it


@pytest.mark.django_db
def test_highlight_escapes_html_and_marks_only_real_peers(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    roster = social.mention_roster(activity)
    html = str(social.highlight_mentions("<script>x</script> hi @bob and @ghost", roster))
    assert "<script>" not in html  # body is escaped first — no injection
    assert "&lt;script&gt;" in html
    assert '<span class="mention">@bob</span>' in html  # real peer highlighted
    assert "@ghost" in html and '"mention">@ghost' not in html  # stranger stays plain text


@pytest.mark.django_db
def test_web_post_ping_checkbox_controls_notification(place, activity_type):
    owner = make_user("alice")
    bob = make_user("bob")
    activity = _activity(owner, place, activity_type)
    _join(activity, bob)
    c = Client()
    c.force_login(owner)
    # without the checkbox: tag only
    c.post(f"/activities/{activity.id}/post/", {"body": "hi @bob"})
    assert _mentions(bob).count() == 0
    # with the checkbox: a ping
    c.post(f"/activities/{activity.id}/post/", {"body": "hi again @bob", "ping": "on"})
    assert _mentions(bob).count() == 1
