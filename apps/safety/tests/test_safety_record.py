"""F19: a user's own DSA Art.16/17 record — self-scoped, no leak of others' data."""

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
