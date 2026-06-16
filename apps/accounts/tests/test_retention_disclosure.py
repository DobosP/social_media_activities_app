"""W3-F16: the data-retention clock. A self-scoped, durations-only disclosure of how long each
category of a user's data is kept — DERIVED from the live settings + the user's own age proof,
including the disabled/null cases, so it can never publish a false GDPR Art.5(e) claim."""

from datetime import timedelta

import pytest
from django.utils import timezone

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, retention_disclosure

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT, *, expires_at=None):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev", expires_at=expires_at))
    if band == AgeBand.UNDER_16:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _by_cat(rows):
    return {r["category"]: r["ttl_description"] for r in rows}


def test_pins_live_settings(settings):
    settings.GUARDIAN_INVITE_TTL_DAYS = 7
    settings.API_TOKEN_MAX_AGE_DAYS = 90
    rows = _by_cat(retention_disclosure(_user("ret1")))
    assert "7 days" in rows["Guardian invitations"]
    assert "90 days" in rows["Device app access"]


def test_messaging_zero_means_no_automatic_deletion(settings):
    settings.MESSAGING_RETENTION_DAYS = 0  # the honest disabled branch
    text = _by_cat(retention_disclosure(_user("ret2")))["Private (encrypted) messages"]
    assert "no automatic deletion" in text
    assert "days after they're sent" not in text  # never a false fixed number


def test_messaging_positive_days_disclosed(settings):
    settings.MESSAGING_RETENTION_DAYS = 30
    text = _by_cat(retention_disclosure(_user("ret3")))["Private (encrypted) messages"]
    assert "30 days" in text


def test_age_expiry_null_vs_set():
    # Null expiry -> honest "no set expiry"; a real expiry -> the actual date, never invented.
    null_text = _by_cat(retention_disclosure(_user("ret4")))["Age verification"]
    assert "no set expiry" in null_text

    exp = timezone.now() + timedelta(days=200)
    set_text = _by_cat(retention_disclosure(_user("ret5", expires_at=exp)))["Age verification"]
    assert "expires on" in set_text
    assert exp.strftime("%Y") in set_text


def test_minor_photo_floor_is_a_day_adult_is_an_hour(settings):
    settings.MEDIA_EPHEMERAL_MIN_TTL_SECONDS = 3600
    settings.MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS = 86400
    adult = _by_cat(retention_disclosure(_user("ret6")))["Disappearing photos"]
    child = _by_cat(retention_disclosure(_user("ret7", AgeBand.UNDER_16)))["Disappearing photos"]
    assert "1 hour" in adult
    assert "1 day" in child


def test_age_expiry_uses_operative_assurance_on_verified_at_tie():
    # When two age proofs share a verified_at, the disclosure must pick the SAME row the platform
    # treats as current (is_assurance_current's -id tiebreaker), not a stale one.
    from apps.accounts.models import AgeAssurance

    user = _user("ret_tie")
    same = timezone.now()
    # Two assurances, identical verified_at, different expiry; the higher-id row is operative.
    AgeAssurance.objects.filter(user=user).update(verified_at=same, expires_at=None)
    operative = AgeAssurance.objects.create(
        user=user,
        provider="dev",
        method="dev",
        age_band=user.age_band,
        verified_at=same,
        expires_at=same + timedelta(days=365),
    )
    text = _by_cat(retention_disclosure(user))["Age verification"]
    assert "expires on" in text  # the operative (higher-id) row's real expiry, not the null one
    assert operative.expires_at.strftime("%Y") in text


def test_disclosure_is_durations_only_no_pii():
    # Self-scoped, no PII/location: every row is exactly a (category, duration) pair, nothing else.
    rows = retention_disclosure(_user("ret8"))
    assert rows  # non-empty
    assert all(set(r) == {"category", "ttl_description"} for r in rows)
