"""W3-F4: active enforcement of parental-consent expiry + the renewal nudge.

Pins the upstream (grant now sets a default term + resets the marker), the one-time expiring-soon
nudge to ACTIVE guardians, the lapsed eviction + notice (minor + guardians) when the LAST valid
consent expires, the multi-consent "still valid" exclusion, the per-consent at-most-once markers,
the grandfathered no-expiry case, and the mass-lapse guard.
"""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort, ParentalConsent, User
from apps.accounts.services import (
    apply_assurance,
    can_participate,
    grant_parental_consent,
    link_guardian,
    run_consent_renewal_sweep,
)
from apps.notifications.models import Notification

pytestmark = pytest.mark.django_db
SYSTEM = Notification.Kind.SYSTEM
RN = ParentalConsent.RenewalNotice


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    return u


def _consented(child_name, guardian_name, *, expires_at):
    """A CHILD with an ACTIVE guardian + a granted consent whose expiry is forced to the arg."""
    guardian = _adult(guardian_name)
    child = _child(child_name)
    link_guardian(guardian, child)
    consent = grant_parental_consent(guardian, child)
    consent.expires_at = expires_at
    consent.save(update_fields=["expires_at"])
    return child, guardian, consent


def _sys_to(user):
    return Notification.objects.filter(recipient=user, kind=SYSTEM)


def test_grant_sets_default_consent_term(settings):
    # Upstream: a fresh grant now gets a finite term (CONSENT_VALIDITY_DAYS) + an un-nudged marker.
    guardian, child = _adult("cs_def_g"), _child("cs_def_c")
    link_guardian(guardian, child)
    consent = grant_parental_consent(guardian, child)
    assert consent.expires_at is not None
    assert consent.renewal_notice == RN.NONE
    assert (consent.expires_at - timezone.now()).days >= settings.CONSENT_VALIDITY_DAYS - 1


def test_expiring_soon_nudges_guardian_once():
    now = timezone.now()
    child, guardian, consent = _consented(
        "cs_soon", "cs_soon_g", expires_at=now + timedelta(days=5)
    )
    Notification.objects.all().delete()  # isolate the sweep's notices
    run_consent_renewal_sweep()
    assert _sys_to(guardian).count() == 1
    consent.refresh_from_db()
    assert consent.renewal_notice == RN.SOON
    run_consent_renewal_sweep()  # a second tick must NOT re-notify
    assert _sys_to(guardian).count() == 1


def test_lapsed_consent_evicts_participation_and_notifies_once():
    now = timezone.now()
    child, guardian, consent = _consented("cs_exp", "cs_exp_g", expires_at=now - timedelta(days=1))
    # Lazy gate already fails on an expired consent; the sweep is the ACTIVE cleanup + notice.
    assert can_participate(child) is False
    Notification.objects.all().delete()
    r = run_consent_renewal_sweep()
    assert r["paused"] == 1
    consent.refresh_from_db()
    assert consent.status == ParentalConsent.Status.EXPIRED  # the handled-marker
    assert _sys_to(child).count() == 1  # the minor is told
    assert _sys_to(guardian).count() == 1  # the guardian is told (they renew)
    r2 = run_consent_renewal_sweep()  # idempotent: no ACTIVE consent left to re-process
    assert r2["paused"] == 0
    assert _sys_to(child).count() == 1


def test_minor_keeps_participation_while_one_consent_valid():
    now = timezone.now()
    g1, g2, child = _adult("cs_2g1"), _adult("cs_2g2"), _child("cs_2c")
    link_guardian(g1, child)
    link_guardian(g2, child)
    c1 = grant_parental_consent(g1, child)
    ParentalConsent.objects.filter(pk=c1.pk).update(expires_at=now - timedelta(days=1))  # lapsed
    grant_parental_consent(g2, child)  # a second, still-valid consent (default ~1y term)
    assert can_participate(child) is True
    r = run_consent_renewal_sweep()
    assert r["paused"] == 0  # NOT evicted while any consent is valid
    assert can_participate(child) is True
    c1.refresh_from_db()
    # Only a FULL lapse flips status -> the lone lapsed row stays ACTIVE; is_valid() handles it.
    assert c1.status == ParentalConsent.Status.ACTIVE


def test_no_expiry_consent_is_grandfathered():
    # A consent granted before W3-F4 (no expiry) never lapses or nudges.
    guardian, child = _adult("cs_noexp_g"), _child("cs_noexp")
    link_guardian(guardian, child)
    consent = grant_parental_consent(guardian, child)
    ParentalConsent.objects.filter(pk=consent.pk).update(expires_at=None)
    Notification.objects.all().delete()
    r = run_consent_renewal_sweep()
    assert r == {"nudged": 0, "paused": 0, "newly_lapsed": 0}
    assert can_participate(child) is True


def test_aged_up_user_with_stale_consent_is_not_evicted():
    # Regression (review HIGH): a former CHILD who re-verified UP to ADULT keeps a stale ACTIVE
    # child-era consent row (apply_assurance recomputes cohort but doesn't purge consents). With a
    # finite term it would otherwise be swept — but they no longer need consent and can_participate
    # is True, so the cohort-gated sweep must leave them entirely alone (no evict, no false notice).
    now = timezone.now()
    child, guardian, consent = _consented("cs_up", "cs_up_g", expires_at=now - timedelta(days=1))
    apply_assurance(child, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    child.refresh_from_db()
    assert child.cohort == Cohort.ADULT
    assert can_participate(child) is True  # consent is irrelevant to an adult
    Notification.objects.all().delete()
    r = run_consent_renewal_sweep()
    assert r["paused"] == 0 and r["newly_lapsed"] == 0  # out of scope (not CHILD cohort)
    consent.refresh_from_db()
    assert consent.status == ParentalConsent.Status.ACTIVE  # NOT flipped to EXPIRED
    assert Notification.objects.filter(recipient=child, kind=SYSTEM).count() == 0  # no false notice


def test_renewal_resets_marker_and_starts_fresh_term():
    now = timezone.now()
    child, guardian, consent = _consented(
        "cs_renew", "cs_renew_g", expires_at=now + timedelta(days=5)
    )
    run_consent_renewal_sweep()
    consent.refresh_from_db()
    assert consent.renewal_notice == RN.SOON
    grant_parental_consent(guardian, child)  # the guardian renews -> fresh term, marker reset
    consent.refresh_from_db()
    assert consent.renewal_notice == RN.NONE
    assert consent.expires_at > now + timedelta(days=300)


def test_lapsed_consent_evicts_from_a_group():
    from apps.communities.models import Area
    from apps.social import services as social
    from apps.social.models import GroupMembership
    from apps.taxonomy.models import ActivityCategory, ActivityType

    now = timezone.now()
    staff = _adult("cs_staff")
    staff.is_staff = True
    staff.save(update_fields=["is_staff"])
    cat, _ = ActivityCategory.objects.get_or_create(slug="cs-sport", defaults={"name": "Sport"})
    at, _ = ActivityType.objects.get_or_create(
        slug="cs-ball", defaults={"name": "Ball", "category": cat}
    )
    area = Area.objects.create(city="CS City", slug="cs-city", name="CS City")
    group = social.create_group(
        staff, area=area, title="Kids Ball", activity_type=at, cohort=Cohort.CHILD
    )
    child, guardian, consent = _consented(
        "cs_evict", "cs_evict_g", expires_at=now + timedelta(days=30)
    )  # valid -> can join
    social.join_group(child, group.id)
    assert group.memberships.get(user=child).state == GroupMembership.State.MEMBER
    consent.expires_at = now - timedelta(days=1)  # ...then consent lapses
    consent.save(update_fields=["expires_at"])
    run_consent_renewal_sweep()
    assert group.memberships.get(user=child).state == GroupMembership.State.REMOVED


def test_mass_lapse_guard_caps_evictions_and_audits(settings):
    from apps.safety.models import AuditLog

    settings.CONSENT_SWEEP_BATCH = 1
    now = timezone.now()
    for i in range(3):
        _consented(f"cs_mass_{i}", f"cs_mass_g_{i}", expires_at=now - timedelta(days=1))
    r = run_consent_renewal_sweep()
    assert r["newly_lapsed"] == 3
    assert r["paused"] == 1  # capped at CONSENT_SWEEP_BATCH
    assert AuditLog.objects.filter(event="accounts.consent_mass_lapse_guard").exists()


def test_command_runs():
    from io import StringIO

    from django.core.management import call_command

    out = StringIO()
    call_command("consent_renewal_sweep", stdout=out)
    assert "consent_renewal_sweep" in out.getvalue()
