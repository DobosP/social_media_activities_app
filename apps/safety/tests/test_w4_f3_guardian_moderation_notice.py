"""W4-F3 — guardian moderation-outcome notice (symmetric DSA loop for a minor).

When a moderator actions or dismisses a report and the offender or reporter is a CHILD, the minor
gets the DSA Art.16/17 detail but the legally-responsible guardian learned nothing. These tests pin
the new fan-out: a non-mutable SYSTEM pointer to /wards/ to each ACTIVE guardian of the affected
CHILD (offender AND reporter), deduped across that union, blocked/revoked guardians excluded, gated
on the CHILD cohort, carrying ZERO reason/identity/moderator detail.
"""

import pytest
from django.contrib.contenttypes.models import ContentType
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian, revoke_guardian
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.safety import services as safety
from apps.safety.models import ModerationAction, ReasonCode, Report
from apps.safety.services import _MODERATION_GUARDIAN_TITLE
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
SYSTEM = Notification.Kind.SYSTEM
Action = ModerationAction.Action


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _child(name):
    return _user(name, AgeBand.UNDER_16)


def _activity(owner, slug):
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"f3-{slug}", name="Sport")
    atype = ActivityType.objects.create(slug=f"f3-{slug}-ball", name="Ball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2031-06-01T10:00Z"
    )


def _report(reporter, target, reason=ReasonCode.HARASSMENT):
    return Report.objects.create(
        reporter=reporter,
        target_type=ContentType.objects.get_for_model(target),
        target_id=target.pk,
        reason=reason,
    )


def _sys_to(user):
    # The F3 guardian fan-out specifically (filtered by its title) — NOT the unrelated SYSTEM
    # reporter-ack that _notify_reporter also emits, so these assertions stay precise.
    return Notification.objects.filter(
        recipient=user, kind=SYSTEM, title=_MODERATION_GUARDIAN_TITLE
    )


def test_take_action_alerts_offender_active_guardian():
    mod = _user("f3-mod1")
    kid = _child("f3-off1")
    guardian = _user("f3-g1")
    link_guardian(guardian, kid)
    safety.take_action(mod, kid, Action.WARN, ReasonCode.GROOMING)
    assert _sys_to(guardian).count() == 1


def test_notice_is_a_pure_pointer_with_no_detail():
    # The body carries NO reason, NO who-did-what, NO moderator identity — only a /wards/ pointer.
    mod = _user("f3-mod2")
    kid = _child("f3-off2")
    guardian = _user("f3-g2")
    link_guardian(guardian, kid)
    safety.take_action(mod, kid, Action.SUSPEND, ReasonCode.GROOMING)
    notice = _sys_to(guardian).get()
    body = notice.body.lower()
    assert "grooming" not in body and "suspend" not in body
    assert "f3-mod2" not in body and "f3-off2" not in body  # no moderator/offender identity
    assert notice.url == "/wards/"


def test_take_action_resolves_content_target_owner():
    # A Post/Activity target is not a User — the affected minor is resolved via _affected_user.
    mod = _user("f3-mod3")
    owner = _child("f3-owner3")
    # A CHILD needs valid parental consent to create an activity (the content target here).
    ParentalConsent.objects.create(
        minor=owner, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    guardian = _user("f3-g3")
    link_guardian(guardian, owner)
    activity = _activity(owner, "ct")
    safety.take_action(mod, activity, Action.REMOVE, ReasonCode.OFF_PLATFORM)
    assert _sys_to(guardian).count() == 1


def test_take_action_alerts_child_reporter_guardian_when_offender_is_adult():
    # Offender is an adult (no guardian); the CHILD reporter's guardian must still be told.
    mod = _user("f3-mod4")
    offender = _user("f3-adult-off4")  # ADULT, no guardian
    reporter = _child("f3-rep4")
    rep_guardian = _user("f3-rg4")
    link_guardian(rep_guardian, reporter)
    report = _report(reporter, offender)
    safety.take_action(mod, offender, Action.WARN, ReasonCode.HARASSMENT, report=report)
    assert _sys_to(rep_guardian).count() == 1


def test_dismiss_report_alerts_child_reporter_guardian():
    # A dismissal has no offender — the CHILD reporter is the only minor, so their guardian is told.
    mod = _user("f3-mod5")
    offender = _user("f3-adult-off5")
    reporter = _child("f3-rep5")
    rep_guardian = _user("f3-rg5")
    link_guardian(rep_guardian, reporter)
    report = _report(reporter, offender)
    safety.dismiss_report(mod, report)
    assert _sys_to(rep_guardian).count() == 1


def test_guardian_of_both_offender_and_reporter_gets_one_notice():
    # Dedup spans the offender+reporter union: one guardian of both -> ONE notice for one outcome.
    mod = _user("f3-mod6")
    offender = _child("f3-off6")
    reporter = _child("f3-rep6")
    guardian = _user("f3-g6")
    link_guardian(guardian, offender)
    link_guardian(guardian, reporter)
    report = _report(reporter, offender)
    safety.take_action(mod, offender, Action.WARN, ReasonCode.HARASSMENT, report=report)
    assert _sys_to(guardian).count() == 1


def test_each_of_two_guardians_is_alerted():
    mod = _user("f3-mod7")
    kid = _child("f3-off7")
    g1, g2 = _user("f3-g7a"), _user("f3-g7b")
    link_guardian(g1, kid)
    link_guardian(g2, kid)
    safety.take_action(mod, kid, Action.WARN, ReasonCode.OTHER)
    assert _sys_to(g1).count() == 1
    assert _sys_to(g2).count() == 1


def test_teen_offender_guardian_not_alerted():
    # The fan-out is CHILD-only — a TEEN offender's guardian gets nothing (cohort gate).
    mod = _user("f3-mod8")
    teen = _user("f3-teen8", AgeBand.AGE_16_17)
    guardian = _user("f3-g8")
    link_guardian(guardian, teen)
    safety.take_action(mod, teen, Action.WARN, ReasonCode.OTHER)
    assert _sys_to(guardian).count() == 0


def test_blocked_guardian_is_excluded():
    mod = _user("f3-mod9")
    kid = _child("f3-off9")
    guardian = _user("f3-g9")
    link_guardian(guardian, kid)
    safety.block_user(kid, guardian)  # a blocked pair never sees each other's content
    safety.take_action(mod, kid, Action.WARN, ReasonCode.OTHER)
    assert _sys_to(guardian).count() == 0


def test_revoked_guardian_is_not_alerted():
    mod = _user("f3-mod10")
    kid = _child("f3-off10")
    guardian = _user("f3-g10")
    link_guardian(guardian, kid)
    revoke_guardian(guardian, kid)  # relationship no longer ACTIVE
    safety.take_action(mod, kid, Action.WARN, ReasonCode.OTHER)
    assert _sys_to(guardian).count() == 0


def test_all_adult_outcome_has_no_guardian_fanout():
    mod = _user("f3-mod11")
    offender = _user("f3-adult-off11")
    reporter = _user("f3-adult-rep11")
    report = _report(reporter, offender)
    safety.take_action(mod, offender, Action.WARN, ReasonCode.HARASSMENT, report=report)
    assert _sys_to(offender).count() == 0
    assert _sys_to(reporter).count() == 0
