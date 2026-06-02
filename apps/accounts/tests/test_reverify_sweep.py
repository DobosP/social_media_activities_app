"""F6 re-verify-or-pause sweep: active enforcement of age-proof expiry for minors. Pins the
one-time expiring-soon nudge (minor + active guardians), the lapsed eviction + paused notice,
the per-proof at-most-once marker, the adult/no-expiry exclusions, and the mass-expiry guard."""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeAssurance, AgeBand, Cohort, User
from apps.accounts.services import apply_assurance, link_guardian, run_reverify_sweep
from apps.notifications.models import Notification

pytestmark = pytest.mark.django_db
SYSTEM = Notification.Kind.SYSTEM
NOTICE = AgeAssurance.ReverifyNotice


def _minor(name, *, expires_at, band=AgeBand.AGE_16_17):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    aa = AgeAssurance.objects.filter(user=u).order_by("-verified_at", "-id").first()
    aa.expires_at = expires_at
    aa.save(update_fields=["expires_at"])
    return u, aa


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _sys_to(user):
    return Notification.objects.filter(recipient=user, kind=SYSTEM)


def test_expiring_soon_nudges_minor_once():
    now = timezone.now()
    u, aa = _minor("rv_soon", expires_at=now + timedelta(days=5))  # within REVERIFY_REMINDER_DAYS
    run_reverify_sweep()
    assert _sys_to(u).count() == 1
    aa.refresh_from_db()
    assert aa.reverify_notice == NOTICE.SOON
    run_reverify_sweep()  # a second tick must NOT re-notify
    assert _sys_to(u).count() == 1


def test_expiring_soon_also_nudges_active_guardian():
    now = timezone.now()
    ward, _ = _minor("rv_ward", expires_at=now + timedelta(days=3), band=AgeBand.UNDER_16)
    guardian = _adult("rv_guard")
    link_guardian(guardian, ward)  # ACTIVE GuardianRelationship
    run_reverify_sweep()
    assert _sys_to(ward).count() == 1
    assert _sys_to(guardian).count() == 1  # the active guardian is nudged too


def test_lapsed_minor_is_paused_and_notified_once():
    now = timezone.now()
    u, aa = _minor("rv_exp", expires_at=now - timedelta(days=1))
    run_reverify_sweep()
    assert _sys_to(u).count() == 1
    aa.refresh_from_db()
    assert aa.reverify_notice == NOTICE.EXPIRED
    run_reverify_sweep()  # idempotent
    assert _sys_to(u).count() == 1


def test_lapsed_minor_is_evicted_from_a_group():
    from apps.communities.models import Area
    from apps.social import services as social
    from apps.social.models import GroupMembership
    from apps.taxonomy.models import ActivityCategory, ActivityType

    now = timezone.now()
    staff = _adult("rv_staff")
    staff.is_staff = True
    staff.save(update_fields=["is_staff"])
    cat, _ = ActivityCategory.objects.get_or_create(slug="rv-sport", defaults={"name": "Sport"})
    at, _ = ActivityType.objects.get_or_create(
        slug="rv-ball", defaults={"name": "Ball", "category": cat}
    )
    area = Area.objects.create(city="RV City", slug="rv-city", name="RV City")
    group = social.create_group(
        staff, area=area, title="Teen Ball", activity_type=at, cohort=Cohort.TEEN
    )
    teen, aa = _minor("rv_teen", expires_at=now + timedelta(days=30))  # valid -> can join
    social.join_group(teen, group.id)
    assert group.memberships.get(user=teen).state == GroupMembership.State.MEMBER
    aa.expires_at = now - timedelta(days=1)  # ...then the proof lapses
    aa.save(update_fields=["expires_at"])
    run_reverify_sweep()
    assert group.memberships.get(user=teen).state == GroupMembership.State.REMOVED


def test_no_expiry_proof_is_left_alone():
    u, _ = _minor("rv_noexp", expires_at=None)
    run_reverify_sweep()
    assert _sys_to(u).count() == 0


def test_adults_are_out_of_scope():
    now = timezone.now()
    a = _adult("rv_adult")
    aa = AgeAssurance.objects.filter(user=a).order_by("-verified_at", "-id").first()
    aa.expires_at = now - timedelta(days=1)  # an expired ADULT proof
    aa.save(update_fields=["expires_at"])
    run_reverify_sweep()
    assert _sys_to(a).count() == 0  # the sweep is minors-only


def test_reverify_starts_a_fresh_cycle():
    now = timezone.now()
    u, aa = _minor("rv_refresh", expires_at=now - timedelta(days=1))
    run_reverify_sweep()
    aa.refresh_from_db()
    assert aa.reverify_notice == NOTICE.EXPIRED
    # Re-verifying creates a NEW proof row, which starts un-notified (NONE) and isn't swept.
    apply_assurance(
        u,
        AssuranceResult(
            age_band=AgeBand.AGE_16_17, provider="dev", expires_at=now + timedelta(days=365)
        ),
    )
    run_reverify_sweep()
    new = AgeAssurance.objects.filter(user=u).order_by("-verified_at", "-id").first()
    assert new.reverify_notice == NOTICE.NONE


def test_mass_expiry_guard_caps_evictions_and_audits(settings):
    from apps.safety.models import AuditLog

    settings.REVERIFY_SWEEP_BATCH = 1
    now = timezone.now()
    for i in range(3):
        _minor(f"rv_mass_{i}", expires_at=now - timedelta(days=1))
    result = run_reverify_sweep()
    assert result["newly_expired"] == 3
    assert result["paused"] == 1  # capped at REVERIFY_SWEEP_BATCH
    assert AuditLog.objects.filter(event="accounts.reverify_mass_expiry_guard").exists()


def test_steady_state_does_not_refire_mass_expiry_guard(settings):
    """Regression: the mass-expiry guard must key on NEWLY-lapsed proofs this tick, not the
    standing backlog of already-paused minors — otherwise it false-alarms every night forever."""
    from apps.safety.models import AuditLog

    settings.REVERIFY_SWEEP_BATCH = 1  # drain one lapsed minor per tick
    now = timezone.now()
    for i in range(2):
        _minor(f"rv_steady_{i}", expires_at=now - timedelta(days=1))
    for _ in range(5):  # several ticks: every lapsed minor ends up paused (reverify_notice=EXPIRED)
        run_reverify_sweep()
    base = AuditLog.objects.filter(event="accounts.reverify_mass_expiry_guard").count()
    result = run_reverify_sweep()  # a further steady-state tick sees only standing EXPIRED rows
    assert result["newly_expired"] == 0
    assert result["paused"] == 0
    after = AuditLog.objects.filter(event="accounts.reverify_mass_expiry_guard").count()
    assert after == base  # the guard does NOT re-fire once the backlog is drained


def test_command_runs_on_empty_and_populated_db():
    from io import StringIO

    from django.core.management import call_command

    out = StringIO()
    call_command("reverify_sweep", stdout=out)
    assert "reverify_sweep" in out.getvalue()
