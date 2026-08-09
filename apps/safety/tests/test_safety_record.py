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


def test_query_is_bounded(django_assert_max_num_queries):
    user, mod = _user("sr_nq"), _user("sr_nqm")
    activity = _activity(user)
    take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM)
    take_action(mod, activity, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    file_report(user, mod, ReasonCode.SPAM)
    # A small constant number of queries regardless of row counts (no per-row N+1).
    with django_assert_max_num_queries(8):
        rec = safety_record_for(user)
        _ = (len(rec["decisions"]), len(rec["reports"]))
