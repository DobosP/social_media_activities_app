"""F31: per-kind mute + the DSA non-mutable carve-out (MODERATION/SYSTEM always deliver)."""

import pytest

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification, NotificationPreference
from apps.notifications.services import (
    get_muted_kinds,
    is_muted,
    notify,
    set_muted_kinds,
    why_reason,
)
from apps.safety.models import ReasonCode
from apps.safety.services import file_report

pytestmark = pytest.mark.django_db

K = Notification.Kind


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_muting_a_kind_suppresses_only_that_kind():
    u = _user("mute_u")
    set_muted_kinds(u, [K.EVENT_REMINDER])
    assert notify(u, K.EVENT_REMINDER, "soon") is None
    assert not Notification.objects.filter(recipient=u, kind=K.EVENT_REMINDER).exists()
    # A different, non-muted kind is still delivered.
    assert notify(u, K.JOIN_APPROVED, "in!") is not None
    assert Notification.objects.filter(recipient=u, kind=K.JOIN_APPROVED).exists()


def test_set_muted_kinds_drops_non_mutable_kinds():
    u = _user("drop_u")
    set_muted_kinds(u, [K.SYSTEM, K.MODERATION, K.EVENT_REMINDER])
    # The DSA kinds are refused at write time; only the mutable one persists.
    assert get_muted_kinds(u) == {K.EVENT_REMINDER.value}


def test_dsa_kinds_deliver_even_with_a_crafted_mute_row():
    u = _user("dsa_u")
    # Bypass set_muted_kinds to forge a row that tries to mute the legally-required kinds.
    NotificationPreference.objects.create(
        user=u, muted_kinds=[K.SYSTEM.value, K.MODERATION.value, K.EVENT_REMINDER.value]
    )
    assert notify(u, K.SYSTEM, "art16 ack") is not None  # Art.16 always delivers
    assert notify(u, K.MODERATION, "art17 sor") is not None  # Art.17 always delivers
    assert notify(u, K.EVENT_REMINDER, "soon") is None  # mutable kind stays muted
    assert is_muted(u, K.SYSTEM) is False
    assert is_muted(u, K.MODERATION) is False


def test_report_acknowledgement_survives_a_stale_system_mute():
    reporter, target = _user("rep_u"), _user("tgt_u")
    NotificationPreference.objects.create(user=reporter, muted_kinds=[K.SYSTEM.value])
    file_report(reporter, target, list(ReasonCode)[0])
    # The DSA Art.16 acknowledgement (a SYSTEM notice) must reach the reporter regardless.
    assert Notification.objects.filter(recipient=reporter, kind=K.SYSTEM).exists()


def test_is_muted_query_cost(django_assert_num_queries):
    u = _user("nq_u")
    set_muted_kinds(u, [K.EVENT_REMINDER])
    with django_assert_num_queries(1):  # one indexed PK lookup for a mutable kind
        assert is_muted(u, K.EVENT_REMINDER) is True
    with django_assert_num_queries(0):  # non-mutable kinds short-circuit before any query
        assert is_muted(u, K.SYSTEM) is False


def test_why_reason_present_for_every_kind():
    for k in K:
        assert why_reason(k)  # non-empty
    assert why_reason("event_reminder") == why_reason(K.EVENT_REMINDER)  # str or member
