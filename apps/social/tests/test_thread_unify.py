"""'One Thread' unification: social.Post is the single durable conversation, one hardened
write path (post_to_thread) shared by web/DRF/socket, a depth-1 quote-reply (reply_to) with a
render-derived snippet, bounded keyset pagination, author edit/soft-delete, and the
post_announcement blocked-fan-out fix. These pin every must-fix the design review raised."""

import pathlib

import pytest
from django.utils import timezone

from apps.notifications.models import Notification
from apps.safety.services import block_user
from apps.social import services as social
from apps.social.models import Activity, Membership

from .conftest import make_user


def _activity(owner, *, place, activity_type, status=Activity.Status.OPEN):
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    if status != Activity.Status.OPEN:
        activity.status = status
        activity.save(update_fields=["status"])
    return activity


def _join(activity, user, *, role=Membership.Role.MEMBER):
    return Membership.objects.create(
        activity=activity, user=user, role=role, state=Membership.State.MEMBER
    )


@pytest.fixture
def setup(place, activity_type):
    owner = make_user("owner1")
    member = make_user("member1")
    activity = _activity(owner, place=place, activity_type=activity_type)
    _join(activity, member)
    return owner, member, activity


# --- the single hardened write gate (status != CANCELLED, guardian, consent, block, policy) --


@pytest.mark.django_db
def test_post_to_completed_activity_succeeds(setup):
    owner, member, activity = setup
    activity.status = Activity.Status.COMPLETED
    activity.save(update_fields=["status"])
    post = social.post_to_thread(member, activity, "thanks for coming!")
    assert post.pk and post.body == "thanks for coming!"


@pytest.mark.django_db
def test_post_to_cancelled_activity_raises(setup):
    owner, member, activity = setup
    activity.status = Activity.Status.CANCELLED
    activity.save(update_fields=["status"])
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "hello?")


@pytest.mark.django_db
def test_guardian_cannot_post(place, activity_type):
    owner = make_user("go")
    guardian = make_user("guard")
    activity = _activity(owner, place=place, activity_type=activity_type)
    _join(activity, guardian, role=Membership.Role.GUARDIAN)
    with pytest.raises(social.NotEligible):
        social.post_to_thread(guardian, activity, "as a guardian")


@pytest.mark.django_db
def test_non_member_cannot_post(setup, place, activity_type):
    owner, member, activity = setup
    outsider = make_user("outsider1")
    with pytest.raises(social.NotAMember):
        social.post_to_thread(outsider, activity, "let me in")


@pytest.mark.django_db
def test_blocked_member_cannot_post(setup):
    owner, member, activity = setup
    block_user(member, owner)
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "still here")


@pytest.mark.django_db
def test_empty_body_rejected_by_policy(setup):
    owner, member, activity = setup
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "   ")


@pytest.mark.django_db
def test_rate_limit_blocks_floods(setup, settings):
    owner, member, activity = setup
    settings.THREAD_POST_RATE_LIMIT = 2
    settings.THREAD_POST_RATE_WINDOW_SECONDS = 3600
    social.post_to_thread(member, activity, "1")
    social.post_to_thread(member, activity, "2")
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "3")


# --- depth-1 reply_to + render-derived snippet ---------------------------------------------


@pytest.mark.django_db
def test_reply_points_at_parent(setup):
    owner, member, activity = setup
    parent = social.post_to_thread(owner, activity, "where do we meet?")
    reply = social.post_to_thread(member, activity, "north gate", reply_to=parent)
    assert reply.reply_to_id == parent.id


@pytest.mark.django_db
def test_reply_to_a_reply_reparents_to_ancestor(setup):
    owner, member, activity = setup
    top = social.post_to_thread(owner, activity, "top")
    r1 = social.post_to_thread(member, activity, "r1", reply_to=top)
    r2 = social.post_to_thread(owner, activity, "r2", reply_to=r1)
    # The tree can never exceed one level: r2 attaches to the TOP-LEVEL ancestor, not r1.
    assert r2.reply_to_id == top.id


@pytest.mark.django_db
def test_cannot_reply_to_hidden_post(setup):
    owner, member, activity = setup
    parent = social.post_to_thread(owner, activity, "secret")
    social.delete_own_post(owner, parent)
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "re", reply_to=parent)


@pytest.mark.django_db
def test_cannot_reply_to_announcement(setup):
    # An announcement is pinned and not part of the reply tree; a reply to it would be orphaned
    # out of thread_page. Must be refused at the single chokepoint (all three write surfaces).
    owner, member, activity = setup
    ann = social.post_announcement(owner, activity, "meet at the north gate")
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "got it", reply_to=ann)
    assert not activity.thread.posts.filter(body="got it").exists()


@pytest.mark.django_db
def test_non_integer_reply_to_raises_domain_error(setup):
    # A bad reply_to must raise a SocialError (caught everywhere), never an uncaught ValueError
    # that would tear down the WebSocket consumer.
    owner, member, activity = setup
    with pytest.raises(social.InvalidState):
        social.post_to_thread(member, activity, "hi", reply_to="not-a-number")


@pytest.mark.django_db
def test_snippet_is_derived_live_from_current_parent(setup):
    owner, member, activity = setup
    parent = social.post_to_thread(owner, activity, "original text")
    reply = social.post_to_thread(member, activity, "ok", reply_to=parent)
    assert social.reply_snippet(reply)["text"] == "original text"
    social.edit_post(owner, parent, "edited text")
    reply.refresh_from_db()
    assert social.reply_snippet(reply)["text"] == "edited text"
    social.delete_own_post(owner, parent)
    reply.refresh_from_db()
    assert "removed" in social.reply_snippet(reply)["text"].lower()


# --- author edit / soft-delete -------------------------------------------------------------


@pytest.mark.django_db
def test_edit_own_post_marks_edited(setup):
    owner, member, activity = setup
    post = social.post_to_thread(member, activity, "typo helo")
    social.edit_post(member, post, "typo hello")
    post.refresh_from_db()
    assert post.body == "typo hello"
    assert social._is_edited(post) is True


@pytest.mark.django_db
def test_cannot_edit_others_post(setup):
    owner, member, activity = setup
    post = social.post_to_thread(owner, activity, "owners")
    with pytest.raises(social.NotEligible):
        social.edit_post(member, post, "hijacked")


@pytest.mark.django_db
def test_delete_own_post_soft_hides_and_audits(setup):
    owner, member, activity = setup
    from apps.safety.models import AuditLog

    post = social.post_to_thread(member, activity, "oops")
    social.delete_own_post(member, post)
    post.refresh_from_db()
    assert post.is_hidden is True  # retained, not destroyed
    assert AuditLog.objects.filter(event="post.self_deleted").exists()


@pytest.mark.django_db
def test_cannot_delete_others_post(setup):
    owner, member, activity = setup
    post = social.post_to_thread(owner, activity, "owners")
    with pytest.raises(social.NotEligible):
        social.delete_own_post(member, post)


# --- bounded keyset pagination -------------------------------------------------------------


@pytest.mark.django_db
def test_thread_page_is_bounded_and_keyset_paginates(setup, settings):
    owner, member, activity = setup
    settings.THREAD_POST_RATE_LIMIT = 1000
    for i in range(5):
        social.post_to_thread(member, activity, f"m{i}")
    page, has_older, cursor = social.thread_page(activity, limit=3)
    assert len(page) == 3
    assert has_older is True
    assert [p.body for p in page] == ["m2", "m3", "m4"]  # oldest->newest of the newest window
    older, has_older2, _ = social.thread_page(activity, before=cursor, limit=3)
    assert [p.body for p in older] == ["m0", "m1"]
    assert has_older2 is False


@pytest.mark.django_db
def test_thread_page_groups_replies_and_excludes_hidden(setup):
    owner, member, activity = setup
    top = social.post_to_thread(owner, activity, "top")
    social.post_to_thread(member, activity, "reply", reply_to=top)
    hidden = social.post_to_thread(member, activity, "removed")
    social.delete_own_post(member, hidden)
    page, _, _ = social.thread_page(activity)
    bodies = [p.body for p in page]
    assert "top" in bodies and "removed" not in bodies  # hidden excluded
    top_rendered = next(p for p in page if p.body == "top")
    assert [r.body for r in top_rendered.replies.all()] == ["reply"]


# --- post_announcement blocked fan-out fix -------------------------------------------------


@pytest.mark.django_db
def test_announcement_skips_blocked_member(place, activity_type):
    owner = make_user("ao")
    blocker = make_user("blocker")
    normal = make_user("normal")
    activity = _activity(owner, place=place, activity_type=activity_type)
    _join(activity, blocker)
    _join(activity, normal)
    block_user(blocker, owner)  # blocker blocks the owner
    social.post_announcement(owner, activity, "meet at 5")
    # The blocked pair gets NO announcement notification; the normal member does.
    assert not Notification.objects.filter(
        recipient=blocker, kind=Notification.Kind.ANNOUNCEMENT
    ).exists()
    assert Notification.objects.filter(
        recipient=normal, kind=Notification.Kind.ANNOUNCEMENT
    ).exists()


# --- the single-write-path guarantee (load-bearing for the safety win) ---------------------


def test_post_objects_create_only_in_social_services():
    """Outside migrations/tests, a thread Post may be created ONLY by post_to_thread /
    post_announcement (apps/social/services.py). A second write surface would defeat the
    unified gate. Source-level guard against a future bypass."""
    root = pathlib.Path(__file__).resolve().parents[3]  # .../apps/social/tests/<file> -> repo
    offenders = []
    for path in (root / "apps").rglob("*.py"):
        if "/migrations/" in str(path) or "/tests/" in str(path):
            continue
        if path == root / "apps" / "social" / "services.py":
            continue
        text = path.read_text(encoding="utf-8")
        if "Post.objects.create(" in text:
            offenders.append(str(path.relative_to(root)))
    assert offenders == [], f"Post created outside the single write path: {offenders}"
