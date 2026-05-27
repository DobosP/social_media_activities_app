import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Role, User
from apps.accounts.services import apply_assurance
from apps.safety.models import ReasonCode
from apps.safety.services import file_report

pytestmark = pytest.mark.django_db


def _user(name, role=Role.USER):
    u = User.objects.create_user(username=name, password="pw", display_name=name, role=role)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_moderator_role_can_access_queue_without_admin():
    moderator = _user("mod1", Role.MODERATOR)
    assert moderator.is_staff is False and moderator.is_superuser is False
    client = APIClient()
    client.force_authenticate(moderator)
    assert client.get("/api/safety/moderation/reports/").status_code == 200


def test_plain_user_cannot_access_queue():
    client = APIClient()
    client.force_authenticate(_user("plain1"))
    assert client.get("/api/safety/moderation/reports/").status_code == 403


def test_moderator_can_resolve_report():
    moderator = _user("mod2", Role.MODERATOR)
    offender = _user("bad2")
    report = file_report(_user("victim2"), offender, ReasonCode.HARASSMENT)
    client = APIClient()
    client.force_authenticate(moderator)
    resp = client.post(
        f"/api/safety/moderation/reports/{report.id}/resolve/",
        {"decision": "ban", "reason": ReasonCode.HARASSMENT},
        format="json",
    )
    assert resp.status_code == 200, resp.content
    offender.refresh_from_db()
    assert offender.is_active is False
