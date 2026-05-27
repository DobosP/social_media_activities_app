import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance


@pytest.mark.django_db
def test_me_requires_auth():
    assert APIClient().get("/api/accounts/me/").status_code in (401, 403)


@pytest.mark.django_db
def test_me_returns_profile():
    user = User.objects.create_user(username="me1", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))

    client = APIClient()
    client.force_authenticate(user)
    resp = client.get("/api/accounts/me/")

    assert resp.status_code == 200
    data = resp.json()
    assert data["username"] == "me1"
    assert data["cohort"] == "adult"
    assert data["can_participate"] is True
    assert data["requires_parental_consent"] is False
