import pytest
from django.utils.translation import gettext, override
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance

pytestmark = pytest.mark.django_db


def test_core_messages_translate_to_romanian():
    with override("ro"):
        assert gettext("User is not eligible to join this activity.") == (
            "Utilizatorul nu este eligibil să se alăture acestei activități."
        )
        assert gettext("The owner cannot leave their own activity.") == (
            "Organizatorul nu poate părăsi propria activitate."
        )


def test_english_passthrough():
    msg = "User is not eligible to join this activity."
    with override("en"):
        assert gettext(msg) == msg


def test_api_honours_accept_language_header():
    user = User.objects.create_user(username="i18n-user", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    client = APIClient()
    client.force_authenticate(user)

    resp = client.put(
        "/api/recommendations/interests/",
        {"interests": "not-a-list"},
        format="json",
        HTTP_ACCEPT_LANGUAGE="ro",
    )
    assert resp.status_code == 400
    assert resp.json()["detail"] == (
        "„interests” trebuie să fie o listă de identificatori (slug) de tipuri."
    )


def test_api_defaults_to_english():
    user = User.objects.create_user(username="i18n-user-en", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    client = APIClient()
    client.force_authenticate(user)

    resp = client.put(
        "/api/recommendations/interests/",
        {"interests": "not-a-list"},
        format="json",
        HTTP_ACCEPT_LANGUAGE="en",
    )
    assert resp.status_code == 400
    assert resp.json()["detail"] == "`interests` must be a list of type slugs."
