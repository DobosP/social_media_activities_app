"""F35: one-click GDPR Art.20 data download. Self-scoped JSON file attachment reusing the
hardened build_user_export — never another user's data, never card/payment details."""

import json

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance

pytestmark = pytest.mark.django_db


def _adult(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_export_downloads_own_data_as_json_attachment():
    u = _adult("dl_me")
    _adult("dl_other")  # a second user whose data must NOT appear
    client = Client()
    client.force_login(u)
    resp = client.get("/account/export/")
    assert resp.status_code == 200
    assert resp["Content-Type"].startswith("application/json")
    assert "attachment" in resp["Content-Disposition"]
    assert str(u.public_id) in resp["Content-Disposition"]  # filename carries the user's own id
    data = json.loads(resp.content)
    assert data["profile"]["username"] == "dl_me"
    assert "owned_groups" in data and "group_memberships" in data  # the full hardened payload
    # Strictly self-scoped: the other user's identity never appears.
    assert "dl_other" not in resp.content.decode()


def test_export_requires_login():
    resp = Client().get("/account/export/")
    assert resp.status_code in (301, 302)  # @login_required -> redirect to login
