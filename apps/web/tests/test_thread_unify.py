"""Web-surface tests for the unified 'One Thread' stream: one Messages section, reply prefill,
author edit/soft-delete round-trips, bounded 'Older messages' link, and the membership wall on
the keyset cursor + permalink (a non-member must never read thread content)."""

from datetime import timedelta

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.social import services as social
from apps.social.models import Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="tu-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="tu-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return social.create_activity(
        owner,
        place=place,
        activity_type=atype,
        title="Pickup game",
        starts_at=timezone.now() + timedelta(days=1),
    )


def _member(activity, user):
    return activity.memberships.create(
        user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def test_activity_detail_footers_no_n_plus_one(settings, django_assert_max_num_queries):
    # R4: sentiment_footers_for must resolve each thread owner ONCE (not per post), so the detail
    # stream renders in a bounded number of queries no matter how many posts/replies carry a footer.
    # Adding more posts below must NOT raise the query count (that's what a footer N+1 would do).
    from apps.social import sentiment
    from apps.social.models import PostReaction

    settings.SENTIMENT_K_ADULT = 2  # latch on 2 reactors + audience >= 4
    owner = _user("qf_owner")
    activity = _activity(owner)
    members = [_user(f"qf_m{i}") for i in range(4)]  # audience = owner + 4 = 5
    for m in members:
        _member(activity, m)
    # Several top-level posts, each with a reply, each reacted enough to latch an appreciation line.
    for i in range(4):
        parent = social.post_to_thread(owner, activity, f"parent {i}")
        reply = social.post_to_thread(members[0], activity, f"reply {i}", reply_to=parent)
        for p in (parent, reply):
            for m in members[:2]:
                PostReaction.objects.create(post=p, user=m, emoji="helped_me")
    sentiment.recompute_post_sentiment(now=timezone.now())  # materialise the footer rows

    c = _client(owner)
    # Headroom bound: the detail view runs many unrelated queries (membership, votes, attachments,
    # reactions, mentions...). The point of this test is only that it does NOT scale with the number
    # of footer-bearing posts — so we pin a generous ceiling and rely on the "add more posts" note
    # above; a per-post owner_object read would blow well past this on 8 rendered posts.
    with django_assert_max_num_queries(60):
        resp = c.get(f"/activities/{activity.id}/")
    assert resp.status_code == 200
    assert "People found this helpful." in resp.content.decode()


def test_member_posts_and_replies_via_web():
    owner = _user("tu_owner")
    member = _user("tu_member")
    activity = _activity(owner)
    _member(activity, member)
    parent = social.post_to_thread(owner, activity, "where do we meet?")

    resp = _client(member).post(
        f"/activities/{activity.id}/post/",
        {"body": "north gate", "reply_to": parent.id},
    )
    assert resp.status_code == 302
    reply = activity.thread.posts.get(body="north gate")
    assert reply.reply_to_id == parent.id


def test_reply_link_prefills_compose():
    owner = _user("tu_o2")
    member = _user("tu_m2")
    activity = _activity(owner)
    _member(activity, member)
    parent = social.post_to_thread(owner, activity, "bring water?")
    page = _client(member).get(f"/activities/{activity.id}/?reply_to={parent.id}").content.decode()
    assert "Replying to" in page
    assert f'value="{parent.id}"' in page  # hidden reply_to is pre-targeted


def test_author_edits_and_deletes_own_post_via_web():
    owner = _user("tu_o3")
    member = _user("tu_m3")
    activity = _activity(owner)
    _member(activity, member)
    post = social.post_to_thread(member, activity, "helo")

    edit = _client(member).post(
        f"/activities/{activity.id}/post/{post.id}/edit/", {"body": "hello"}
    )
    assert edit.status_code == 302
    post.refresh_from_db()
    assert post.body == "hello"

    delete = _client(member).post(f"/activities/{activity.id}/post/{post.id}/delete/")
    assert delete.status_code == 302
    post.refresh_from_db()
    assert post.is_hidden is True


def test_non_author_cannot_edit_via_web():
    owner = _user("tu_o4")
    member = _user("tu_m4")
    activity = _activity(owner)
    _member(activity, member)
    post = social.post_to_thread(owner, activity, "owners message")
    _client(member).post(f"/activities/{activity.id}/post/{post.id}/edit/", {"body": "hijack"})
    post.refresh_from_db()
    assert post.body == "owners message"  # unchanged


def test_older_messages_link_when_bounded(settings):
    settings.SOCIAL_THREAD_POST_LIMIT = 3
    settings.THREAD_POST_RATE_LIMIT = 1000
    owner = _user("tu_o5")
    activity = _activity(owner)
    for i in range(5):
        social.post_to_thread(owner, activity, f"msg{i}")
    page = _client(owner).get(f"/activities/{activity.id}/").content.decode()
    assert "Older messages" in page
    assert "before=" in page


def test_malformed_before_cursor_does_not_500():
    owner = _user("tu_cur")
    activity = _activity(owner)
    social.post_to_thread(owner, activity, "hi")
    # A crafted non-numeric ?before= must degrade to the first page, never a 500.
    resp = _client(owner).get(f"/activities/{activity.id}/?before=not-a-number")
    assert resp.status_code == 200
    assert "hi" in resp.content.decode()


def test_delete_of_moderator_hidden_post_shows_info_message():
    # A Delete click on an already-moderator-hidden post records the author's own deletion but
    # visibly changes nothing — without a notice the click is a silent no-op that quietly
    # forecloses republication on a later overturn. Both delete endpoints must say what happened.
    from apps.communities.models import Area
    from apps.safety.models import ModerationAction, ReasonCode
    from apps.safety.services import take_action

    mod = _user("fh_mod")
    # Activity thread endpoint.
    owner = _user("fh_owner")
    member = _user("fh_member")
    activity = _activity(owner)
    _member(activity, member)
    post = social.post_to_thread(member, activity, "mine")
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    resp = _client(member).post(f"/activities/{activity.id}/post/{post.id}/delete/", follow=True)
    body = resp.content.decode()
    assert "This message had already been hidden by a moderation decision." in body
    assert "review the decision in your safety record" in body
    post.refresh_from_db()
    assert post.is_author_deleted is True

    # Group thread endpoint (same copy — parity).
    gowner = _user("fh_gowner")
    gowner.is_staff = True  # ADULT groups are staff-curated by default
    gowner.save(update_fields=["is_staff"])
    area = Area.objects.create(city="Cluj-Napoca", slug="fh-area", name="Cluj-Napoca")
    cat, _ = ActivityCategory.objects.get_or_create(slug="tu-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="tu-bball", defaults={"name": "Basketball", "category": cat}
    )
    group = social.create_group(gowner, area=area, title="Cluj Basketball", activity_type=atype)
    gmember = _user("fh_gmember")
    social.join_group(gmember, group.id)
    gpost = social.post_to_thread(gmember, group, "mine in the group")
    take_action(mod, gpost, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    gresp = _client(gmember).post(f"/groups/{group.id}/posts/{gpost.id}/delete/", follow=True)
    gbody = gresp.content.decode()
    assert "This message had already been hidden by a moderation decision." in gbody
    gpost.refresh_from_db()
    assert gpost.is_author_deleted is True


def test_delete_of_admin_hidden_post_shows_no_moderation_claim():
    # An admin manual hide (PostAdmin) sets is_hidden with NO ModerationAction, so the author's
    # safety record shows nothing — the delete must stamp provenance silently, never flash
    # "hidden by a moderation decision… review the decision in your safety record" (a claim
    # about a decision that does not exist).
    from apps.social.models import Post

    owner = _user("ah_owner")
    member = _user("ah_member")
    activity = _activity(owner)
    _member(activity, member)
    post = social.post_to_thread(member, activity, "mine")
    Post.objects.filter(pk=post.pk).update(is_hidden=True)  # as PostAdmin would
    resp = _client(member).post(f"/activities/{activity.id}/post/{post.id}/delete/", follow=True)
    assert "hidden by a moderation decision" not in resp.content.decode()
    post.refresh_from_db()
    assert post.is_author_deleted is True


def test_repeat_self_delete_shows_no_message():
    # An idempotent second delete of an ordinarily self-deleted post must stay silent — the
    # moderation notice on a double-click would be alarming nonsense.
    owner = _user("rs_owner")
    member = _user("rs_member")
    activity = _activity(owner)
    _member(activity, member)
    post = social.post_to_thread(member, activity, "mine")
    c = _client(member)
    c.post(f"/activities/{activity.id}/post/{post.id}/delete/")
    resp = c.post(f"/activities/{activity.id}/post/{post.id}/delete/", follow=True)
    assert "already been hidden by a moderation decision" not in resp.content.decode()


def test_delete_while_contesting_shows_refusal_and_keeps_the_remedy():
    # F2 refusal end-to-end: with the author's own contest of the hiding REMOVE still PENDING,
    # the delete is refused (rendered via the normal SocialError flash), nothing is stamped,
    # and the appeal is untouched.
    from apps.safety.models import ModerationAction, ModerationAppeal, ReasonCode
    from apps.safety.services import file_appeal, take_action

    mod = _user("wc_mod")
    owner = _user("wc_owner")
    member = _user("wc_member")
    activity = _activity(owner)
    _member(activity, member)
    post = social.post_to_thread(member, activity, "mine")
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    appeal = file_appeal(member, action, "the removal was wrong")
    resp = _client(member).post(f"/activities/{activity.id}/post/{post.id}/delete/", follow=True)
    body = resp.content.decode()
    assert "contesting the moderation decision that hid this message" in body
    # The promise is conditional — a SECOND standing REMOVE could also be holding the post,
    # so the copy must not assert the message "will come back" unqualified.
    assert "nothing else is holding the message" in body
    post.refresh_from_db()
    appeal.refresh_from_db()
    assert post.is_author_deleted is False
    assert appeal.status == ModerationAppeal.Status.PENDING


def test_non_member_cannot_read_thread_via_cursor_or_permalink():
    owner = _user("tu_o6")
    activity = _activity(owner)
    secret = social.post_to_thread(owner, activity, "secret coordination text")
    # A different same-cohort user who is NOT a member can open the (cohort-visible) page but
    # must NOT see any thread content, even via the keyset cursor or the post anchor.
    stranger = _user("tu_stranger")
    page = (
        _client(stranger)
        .get(f"/activities/{activity.id}/?before={secret.id}#post-{secret.id}")
        .content.decode()
    )
    assert "secret coordination text" not in page
    assert "private to members" in page
