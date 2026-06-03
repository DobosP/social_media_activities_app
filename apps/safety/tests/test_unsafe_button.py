"""F8 — one-tap "I feel unsafe" safety button (service layer). Pins: a real OFF_PLATFORM report is
filed against the activity (with a sentinel that can't collide with a slow-path report); a CHILD
reporter's ACTIVE guardians get a non-mutable SYSTEM alert (blocked + revoked guardians excluded);
TEEN/ADULT reporters trigger no guardian fan-out; the action is idempotent per (reporter, activity)
while being handled or within the cooldown; a resolved-and-cooled report can be re-raised; and it is
rate-limited. The return value reports exactly how many guardians were actually alerted."""

import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, GuardianRelationship, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian, revoke_guardian
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.safety import services as safety
from apps.safety.models import ReasonCode, Report
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
SYSTEM = Notification.Kind.SYSTEM


def _user(name, band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _child(name):
    return _user(name, AgeBand.UNDER_16)


def _activity(owner_name="ub_owner"):
    owner = _user(owner_name, AgeBand.UNDER_16, consented=True)  # a CHILD-cohort activity
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat, _ = ActivityCategory.objects.get_or_create(slug="ub-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="ub-ball", defaults={"name": "Ball", "category": cat}
    )
    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2031-06-01T10:00Z"
    )


def _sys_to(user):
    return Notification.objects.filter(recipient=user, kind=SYSTEM)


def test_child_report_files_off_platform_and_alerts_active_guardian():
    activity = _activity()
    kid = _child("ub_kid")
    guardian = _user("ub_parent")
    link_guardian(guardian, kid)

    result = safety.file_unsafe_report(kid, activity)

    assert result.report.reason == ReasonCode.OFF_PLATFORM
    assert result.report.target == activity
    assert result.report.detail  # a fixed server sentinel (not child free text)
    assert result.repeat is False
    assert result.guardians_alerted == 1
    assert _sys_to(guardian).count() == 1  # the active guardian got a SYSTEM (non-mutable) alert


def test_idempotent_while_open_no_duplicate_report_or_guardian_storm():
    activity = _activity()
    kid = _child("ub_kid2")
    guardian = _user("ub_parent2")
    link_guardian(guardian, kid)

    first = safety.file_unsafe_report(kid, activity)
    second = safety.file_unsafe_report(kid, activity)  # re-tap while still being handled

    assert first.report.pk == second.report.pk  # same report, not a new one
    assert second.repeat is True
    assert second.guardians_alerted == 0  # not re-alerted
    assert Report.objects.filter(reporter=kid, target_id=activity.pk).count() == 1
    assert _sys_to(guardian).count() == 1  # guardian alerted once, not stormed


def test_slow_path_report_does_not_suppress_guardian_alert():
    """Finding 1: a user's own free-text OFF_PLATFORM slow-path report must NOT make the panic
    button idempotent — the sentinel-scoped dedup keeps the guardian alert firing."""
    activity = _activity()
    kid = _child("ub_kid_sp")
    guardian = _user("ub_parent_sp")
    link_guardian(guardian, kid)
    # The detailed slow path: a free-text OFF_PLATFORM report, no sentinel, no guardian alert.
    slow = safety.file_report(kid, activity, ReasonCode.OFF_PLATFORM, detail="they were mean")
    assert _sys_to(guardian).count() == 0

    result = safety.file_unsafe_report(kid, activity)  # the panic button must still fire
    assert result.repeat is False
    assert result.report.pk != slow.pk  # a distinct panic report, not the slow-path one
    assert result.guardians_alerted == 1
    assert _sys_to(guardian).count() == 1


def test_idempotent_while_reviewing_no_restorm():
    """Finding 2: a moderator picking the report up (REVIEWING) must NOT re-arm the fast path."""
    activity = _activity()
    kid = _child("ub_kid_rev")
    guardian = _user("ub_parent_rev")
    link_guardian(guardian, kid)

    first = safety.file_unsafe_report(kid, activity)
    first.report.status = Report.Status.REVIEWING
    first.report.save(update_fields=["status"])

    second = safety.file_unsafe_report(kid, activity)
    assert second.repeat is True
    assert second.report.pk == first.report.pk
    assert Report.objects.filter(reporter=kid, target_id=activity.pk).count() == 1
    assert _sys_to(guardian).count() == 1  # no re-storm during review


def test_resolved_and_cooled_report_can_be_reraised(settings):
    """Finding 2: once resolved AND past the cooldown, a genuinely-recurring fear raises a fresh
    alert (a panic button must not be permanently silenced after one dismissal)."""
    settings.UNSAFE_REPORT_COOLDOWN_SECONDS = 0
    activity = _activity()
    kid = _child("ub_kid_re")
    guardian = _user("ub_parent_re")
    link_guardian(guardian, kid)

    first = safety.file_unsafe_report(kid, activity)
    first.report.status = Report.Status.DISMISSED  # moderator resolved it
    first.report.save(update_fields=["status"])

    second = safety.file_unsafe_report(kid, activity)  # still scared, after resolution + cooldown
    assert second.repeat is False
    assert second.report.pk != first.report.pk  # a fresh report
    assert _sys_to(guardian).count() == 2  # re-alerted


def test_revoked_guardian_is_not_alerted():
    activity = _activity()
    kid = _child("ub_kid3")
    guardian = _user("ub_parent3")
    link_guardian(guardian, kid)
    revoke_guardian(guardian, kid)  # link no longer ACTIVE

    result = safety.file_unsafe_report(kid, activity)
    assert result.guardians_alerted == 0
    assert _sys_to(guardian).count() == 0  # only ACTIVE guardians are alerted


def test_blocked_guardian_is_excluded():
    activity = _activity()
    kid = _child("ub_kid4")
    guardian = _user("ub_parent4")
    link_guardian(guardian, kid)
    safety.block_user(kid, guardian)  # a blocked pair never sees each other's content

    result = safety.file_unsafe_report(kid, activity)
    assert result.guardians_alerted == 0  # the only guardian was blocked-excluded
    assert _sys_to(guardian).count() == 0


def test_teen_reporter_has_no_guardian_fanout_even_with_guardian():
    """Finding 4: teens self-manage — no guardian fan-out, and the result must say so (0) so the
    view never falsely promises a teen that a guardian was told."""
    activity = _activity()
    teen = _user("ub_teen", AgeBand.AGE_16_17)  # cohort TEEN
    guardian = _user("ub_teenparent")
    link_guardian(guardian, teen)

    result = safety.file_unsafe_report(teen, activity)
    assert result.report.reason == ReasonCode.OFF_PLATFORM
    assert result.guardians_alerted == 0
    assert _sys_to(guardian).count() == 0


def test_adult_reporter_has_no_guardian_fanout():
    activity = _activity()
    adult = _user("ub_adult")
    result = safety.file_unsafe_report(adult, activity)
    assert result.report.reason == ReasonCode.OFF_PLATFORM
    assert result.guardians_alerted == 0
    assert not GuardianRelationship.objects.filter(ward=adult).exists()


def test_rate_limited_after_cap(settings):
    settings.UNSAFE_REPORT_RATE_LIMIT = 2
    settings.UNSAFE_REPORT_RATE_WINDOW_SECONDS = 3600
    reporter = _user("ub_spammer")
    # Each DISTINCT activity is a fresh report (so it consumes the rate budget, unlike idempotent
    # re-taps on one activity). The 3rd distinct report must be refused.
    safety.file_unsafe_report(reporter, _activity("ub_o1"))
    safety.file_unsafe_report(reporter, _activity("ub_o2"))
    with pytest.raises(safety.RateLimited):
        safety.file_unsafe_report(reporter, _activity("ub_o3"))


def test_idempotent_retap_does_not_burn_rate_budget(settings):
    settings.UNSAFE_REPORT_RATE_LIMIT = 1
    settings.UNSAFE_REPORT_RATE_WINDOW_SECONDS = 3600
    activity = _activity()
    kid = _child("ub_kid5")
    # First files (consumes the 1 budget); re-taps on the SAME activity are idempotent and must NOT
    # raise RateLimited (the existing-report check short-circuits before allow_action).
    safety.file_unsafe_report(kid, activity)
    safety.file_unsafe_report(kid, activity)
    safety.file_unsafe_report(kid, activity)  # would raise if it hit the rate limiter
    assert Report.objects.filter(reporter=kid, target_id=activity.pk).count() == 1
