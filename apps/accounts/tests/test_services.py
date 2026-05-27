import pytest

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort, ParentalConsent, User
from apps.accounts.services import apply_assurance, assign_cohort, can_participate


def test_assign_cohort():
    assert assign_cohort(AgeBand.UNDER_16) == Cohort.CHILD
    assert assign_cohort(AgeBand.AGE_16_17) == Cohort.TEEN
    assert assign_cohort(AgeBand.ADULT) == Cohort.ADULT
    assert assign_cohort("nonsense") == Cohort.UNASSIGNED


@pytest.mark.django_db
def test_apply_assurance_sets_band_and_cohort():
    user = User.objects.create_user(username="u1", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    user.refresh_from_db()
    assert user.age_band == AgeBand.ADULT
    assert user.cohort == Cohort.ADULT
    assert user.is_identity_verified is True
    assert user.identity_verified_at is not None
    assert user.age_assurances.count() == 1


@pytest.mark.django_db
def test_minor_needs_consent_to_participate():
    minor = User.objects.create_user(username="mn", password="pw")
    apply_assurance(minor, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    assert can_participate(minor) is False  # verified, but no consent yet

    ParentalConsent.objects.create(
        minor=minor, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    assert can_participate(minor) is True


@pytest.mark.django_db
def test_adult_can_participate_and_unverified_cannot():
    adult = User.objects.create_user(username="ad", password="pw")
    apply_assurance(adult, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    assert can_participate(adult) is True

    unverified = User.objects.create_user(username="nv", password="pw", age_band=AgeBand.ADULT)
    assert can_participate(unverified) is False
