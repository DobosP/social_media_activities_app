from datetime import timedelta

import pytest
from django.utils import timezone

from apps.accounts.models import AgeBand, Cohort, ParentalConsent, User


@pytest.mark.django_db
def test_create_user_and_superuser():
    user = User.objects.create_user(username="kid123", password="pw")
    assert user.username == "kid123"
    assert user.check_password("pw")
    assert user.public_id is not None

    su = User.objects.create_superuser(username="admin", password="pw")
    assert su.is_staff and su.is_superuser
    assert su.age_band == AgeBand.ADULT


@pytest.mark.django_db
def test_recompute_cohort_and_consent_flag():
    kid = User.objects.create_user(username="kid", password="pw", age_band=AgeBand.UNDER_16)
    kid.recompute_cohort()
    assert kid.cohort == Cohort.CHILD
    assert kid.requires_parental_consent is True

    adult = User.objects.create_user(username="adult", password="pw", age_band=AgeBand.ADULT)
    adult.recompute_cohort()
    assert adult.cohort == Cohort.ADULT
    assert adult.requires_parental_consent is False


@pytest.mark.django_db
def test_parental_consent_validity():
    kid = User.objects.create_user(username="kid2", password="pw", age_band=AgeBand.UNDER_16)
    consent = ParentalConsent.objects.create(
        minor=kid, guardian_identifier="guardian-ref", status=ParentalConsent.Status.ACTIVE
    )
    assert consent.is_valid() is True

    consent.expires_at = timezone.now() - timedelta(days=1)
    assert consent.is_valid() is False

    consent.expires_at = None
    consent.status = ParentalConsent.Status.REVOKED
    assert consent.is_valid() is False
