from datetime import timedelta

import pytest
from django.utils import timezone
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.safety.models import ModerationAction, ReasonCode, Report
from apps.safety.services import (
    block_user,
    blocked_user_ids,
    file_report,
    lift_expired_suspensions,
    take_action,
)

pytestmark = pytest.mark.django_db


def _user(name, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name, is_staff=staff)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_blocked_user_ids_is_symmetric():
    a, b, c = _user("a"), _user("b"), _user("c")
    block_user(a, b)
    block_user(c, a)
    assert blocked_user_ids(a) == {b.id, c.id}
    assert blocked_user_ids(b) == {a.id}


def test_lift_expired_suspension_reactivates():
    mod, offender = _user("m", staff=True), _user("o")
    take_action(
        mod,
        offender,
        ModerationAction.Action.SUSPEND,
        ReasonCode.SPAM,
        expires_at=timezone.now() - timedelta(hours=1),
    )
    offender.refresh_from_db()
    assert offender.is_active is False

    assert lift_expired_suspensions() == 1
    offender.refresh_from_db()
    assert offender.is_active is True
    # Idempotent — already lifted.
    assert lift_expired_suspensions() == 0


def test_expired_suspension_not_lifted_if_also_banned():
    mod, offender = _user("m2", staff=True), _user("o2")
    take_action(
        mod,
        offender,
        ModerationAction.Action.SUSPEND,
        ReasonCode.SPAM,
        expires_at=timezone.now() - timedelta(hours=1),
    )
    take_action(mod, offender, ModerationAction.Action.BAN, ReasonCode.GROOMING)
    lift_expired_suspensions()
    offender.refresh_from_db()
    assert offender.is_active is False  # ban keeps it deactivated


def test_moderation_api_list_and_resolve_ban():
    staff = _user("staff", staff=True)
    offender = _user("bad")
    report = file_report(_user("victim"), offender, ReasonCode.HARASSMENT)

    client = APIClient()
    client.force_authenticate(staff)
    listing = client.get("/api/safety/moderation/reports/?status=open")
    assert listing.status_code == 200
    assert any(r["id"] == report.id for r in listing.json())

    resp = client.post(
        f"/api/safety/moderation/reports/{report.id}/resolve/",
        {"decision": "ban", "reason": ReasonCode.HARASSMENT},
        format="json",
    )
    assert resp.status_code == 200, resp.content
    report.refresh_from_db()
    offender.refresh_from_db()
    assert report.status == Report.Status.ACTIONED
    assert offender.is_active is False


def test_moderation_api_requires_staff():
    plain = _user("plain")
    client = APIClient()
    client.force_authenticate(plain)
    assert client.get("/api/safety/moderation/reports/").status_code == 403


def test_resolve_dismiss():
    staff = _user("staff2", staff=True)
    report = file_report(_user("rep"), _user("tgt"), ReasonCode.SPAM)
    client = APIClient()
    client.force_authenticate(staff)
    resp = client.post(
        f"/api/safety/moderation/reports/{report.id}/resolve/",
        {"decision": "dismiss", "notes": "not actionable"},
        format="json",
    )
    assert resp.status_code == 200
    report.refresh_from_db()
    assert report.status == Report.Status.DISMISSED
