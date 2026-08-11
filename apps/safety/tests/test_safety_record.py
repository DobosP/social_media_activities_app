"""F19: a user's own DSA Art.16/17 record — self-scoped, no leak of others' data."""

import datetime as dt

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import ModerationAction, ReasonCode
from apps.safety.services import file_report, safety_record_for, take_action
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _activity(owner):
    cat, _ = ActivityCategory.objects.get_or_create(slug="sr-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="sr-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


def test_shows_own_account_decision():
    user, mod = _user("sr_u"), _user("sr_mod")
    take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM, notes="internal note")
    record = safety_record_for(user)
    assert len(record["decisions"]) == 1
    d = record["decisions"][0]
    assert d["scope"] == "your account"
    assert "internal note" not in str(d)  # moderator notes are never projected


def test_shows_decision_on_own_activity():
    user, mod = _user("sr_u2"), _user("sr_mod2")
    activity = _activity(user)
    take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    record = safety_record_for(user)
    assert any(d["scope"] == "one of your activities" for d in record["decisions"])


def test_shows_own_reports():
    user, target = _user("sr_rep"), _user("sr_tgt")
    file_report(user, target, ReasonCode.HARASSMENT, "they were rude")
    record = safety_record_for(user)
    assert len(record["reports"]) == 1
    assert record["reports"][0]["status_label"]


def test_does_not_show_other_users_decisions():
    user, other, mod = _user("sr_me"), _user("sr_other"), _user("sr_m3")
    take_action(mod, other, ModerationAction.Action.WARN, ReasonCode.SPAM)
    assert safety_record_for(user)["decisions"] == []


def test_no_moderator_identity_leak():
    user, mod = _user("sr_me2"), _user("sr_secretmod")
    take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.HARASSMENT, notes="x")
    record = safety_record_for(user)
    blob = str(record)
    assert "sr_secretmod" not in blob  # neither username nor display_name of the moderator
    # The suspension row is present and flagged active (not expired / not lifted).
    assert record["decisions"][0]["is_sanction"] is True
    assert record["decisions"][0]["is_active"] is True


def test_shows_decision_on_newest_post_of_a_high_volume_author():
    # Regression: the own-content prefilter used to be a python-side ``[:1000]`` slice, and
    # because Post.Meta.ordering is ["created_at"] it kept the OLDEST posts — so a decision on
    # a prolific member's NEWEST post silently vanished from their DSA Art.16/17 record, and
    # with it the action_id the contest form needs. The bound must be the newest-first action
    # query, not the id set.
    from apps.social.models import Post

    user, mod = _user("sr_vol"), _user("sr_volmod")
    activity = _activity(user)
    Post.objects.bulk_create(
        [Post(thread=activity.thread, author=user, body=f"filler {i}") for i in range(1000)]
    )
    # auto_now_add gives the fillers "now"; age them so the target below is unambiguously the
    # newest and the old slice would have consumed its entire budget on the fillers.
    Post.objects.filter(author=user).update(created_at=timezone.now() - dt.timedelta(days=1))
    newest = Post.objects.create(thread=activity.thread, author=user, body="the reported one")

    take_action(mod, newest, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    record = safety_record_for(user)
    assert [d["scope"] for d in record["decisions"]] == ["one of your posts"]
    assert record["decisions"][0]["can_appeal"] is True


def test_shows_decision_on_activity_beyond_the_old_500_cap():
    # The Activity branch had the same truncation, and worse: Activity declares no Meta.ordering,
    # so the old [:500] slice dropped an ARBITRARY, planner-dependent 500 — two page loads could
    # disagree about which of your own decisions exist.
    from apps.social.models import Activity

    user, mod = _user("sr_act"), _user("sr_actmod")
    seed = _activity(user)
    Activity.objects.bulk_create(
        [
            Activity(
                owner=user,
                place=seed.place,
                activity_type=seed.activity_type,
                title=f"filler {i}",
                starts_at=seed.starts_at,
                cohort=seed.cohort,
            )
            for i in range(500)
        ]
    )
    target = Activity.objects.filter(owner=user).order_by("-id").first()

    take_action(mod, target, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    record = safety_record_for(user)
    assert [d["scope"] for d in record["decisions"]] == ["one of your activities"]


def test_safety_record_marks_author_deleted_removed_post():
    # "content_author_deleted" answers "will reversing THIS decision restore the content?" —
    # True ONLY for a REMOVE-on-Post whose post the author also deleted themselves AND which
    # is still hidden.
    from django.contrib.contenttypes.models import ContentType

    from apps.social.models import Post
    from apps.social.services import delete_own_post, post_to_thread

    # Explicit far-out pk: the collision post below must have id == user.id, and a sequence-
    # assigned user id could someday coincide with a sequence-assigned post id (flaky). A pk
    # no sequence reaches in a test session makes the collision deterministic and safe.
    user = User.objects.create_user(id=9_876_543, username="sr_ad", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    mod = _user("sr_admod")
    activity = _activity(user)
    deleted = post_to_thread(user, activity, "my own words")
    delete_own_post(user, deleted)
    removed_deleted = take_action(mod, deleted, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    kept = post_to_thread(user, activity, "perfectly fine")
    removed_kept = take_action(mod, kept, ModerationAction.Action.REMOVE, ReasonCode.SPAM)
    warned_deleted = take_action(mod, deleted, ModerationAction.Action.WARN, ReasonCode.SPAM)
    # Author-deleted but LIVE: an operator un-hide in PostAdmin (or a migration-0039
    # republished row) clears is_hidden while is_author_deleted stays — the page must not
    # claim "the message stays deleted" about a live post (agrees with _reverse_action,
    # which only declines the un-hide while the post IS hidden). Posted BEFORE the activity-
    # level REMOVE below: a hidden activity refuses new posts.
    unhidden = post_to_thread(user, activity, "deleted then operator-unhidden")
    delete_own_post(user, unhidden)
    removed_unhidden = take_action(mod, unhidden, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    Post.objects.filter(pk=unhidden.pk).update(is_hidden=False)
    on_activity = take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    on_account = take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM)
    # False-positive guard: a USER-targeted action whose target_id collides with an
    # author-deleted post id must never inherit the flag (a user-typed target_id is a user id).
    collision_post = Post.objects.create(
        id=user.id,
        thread=activity.thread,
        author=user,
        body="id collides with the account",
        is_hidden=True,
        is_author_deleted=True,
    )
    # The collision post carries its own post-typed REMOVE, so user.id IS in the author-deleted
    # id set — which makes the colliding user-typed row below stay False ONLY because of the
    # projection's target-type check (delete that check and this matrix fails). Without this
    # row the guard is inert: the id set never contains user.id in the first place.
    removed_collision_post = take_action(
        mod, collision_post, ModerationAction.Action.REMOVE, ReasonCode.OTHER
    )
    colliding = ModerationAction.objects.create(
        moderator=mod,
        target_type=ContentType.objects.get_for_model(User),
        target_id=user.id,
        action=ModerationAction.Action.REMOVE,
        reason=ReasonCode.SPAM,
    )
    flags = {
        d["action_id"]: d["content_author_deleted"] for d in safety_record_for(user)["decisions"]
    }
    assert flags == {
        removed_deleted.id: True,  # REMOVE on a self-deleted post — flagged
        removed_kept.id: False,  # ordinary REMOVE-on-post
        warned_deleted.id: False,  # a WARN reversal restores nothing, so no flag
        on_activity.id: False,  # activity scope
        on_account.id: False,  # account scope
        removed_unhidden.id: False,  # author-deleted but LIVE — nothing "stays deleted"
        removed_collision_post.id: True,  # genuinely author-deleted + hidden post
        colliding.id: False,  # the id-collision guard (user-typed row, same target_id)
    }


def test_safety_record_reports_totals_and_truncation():
    # Honest truncation: the record (and the Art.20 export built from it) must say when the
    # shown lists are capped, with exact totals.
    user, mod = _user("sr_tt"), _user("sr_ttm")
    activity = _activity(user)
    take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM)
    take_action(mod, user, ModerationAction.Action.SUSPEND, ReasonCode.SPAM)
    take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    for _ in range(3):
        file_report(user, mod, ReasonCode.SPAM)
    rec = safety_record_for(user, limit=2)
    assert len(rec["decisions"]) == 2
    assert rec["decisions_total"] == 3
    assert rec["decisions_truncated"] is True
    assert len(rec["reports"]) == 2
    assert rec["reports_total"] == 3
    assert rec["reports_truncated"] is True
    full = safety_record_for(user)
    assert full["decisions_total"] == 3
    assert full["decisions_truncated"] is False
    assert full["reports_total"] == 3
    assert full["reports_truncated"] is False


def test_author_deleted_marking_and_totals_query_cost_is_flat():
    # The flag costs ONE query however many decisions are shown, and the totals a fixed four
    # (3 per-scope decision counts + 1 report count) — never an OR-ed count (seq-scan/SubPlan
    # lesson, see safety_record_for). "Flat" is asserted directly: the SAME query count at two
    # different decision counts — a pinned exact N would flake under randomized suite order
    # (warm-cache drift) without proving flatness.
    from django.db import connection
    from django.test.utils import CaptureQueriesContext

    from apps.social.services import delete_own_post, post_to_thread

    user, mod = _user("sr_qc"), _user("sr_qcm")
    activity = _activity(user)

    def _add_removed_deleted_posts(tags):
        for tag in tags:
            post = post_to_thread(user, activity, f"p{tag}")
            delete_own_post(user, post)
            take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)

    _add_removed_deleted_posts("ab")
    safety_record_for(user)  # warm the ContentType cache so both counts see the same caches
    with CaptureQueriesContext(connection) as small:
        safety_record_for(user)
    _add_removed_deleted_posts("cde")
    with CaptureQueriesContext(connection) as large:
        assert len(safety_record_for(user)["decisions"]) == 5
    assert len(small) == len(large)


def test_query_is_bounded(django_assert_max_num_queries):
    user, mod = _user("sr_nq"), _user("sr_nqm")
    activity = _activity(user)
    take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM)
    take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    file_report(user, mod, ReasonCode.SPAM)
    # A small constant number of queries regardless of row counts (no per-row N+1). Headroom
    # covers the honest-truncation totals (3 per-scope decision counts + 1 report count) and
    # the author-deleted marking query on top of the original 8.
    with django_assert_max_num_queries(13):
        rec = safety_record_for(user)
        _ = (len(rec["decisions"]), len(rec["reports"]))
