"""W2-F33: the GET confirmation step on /account/delete/ shows an honest counts-only erasure
preview (and fixes the old my_privacy link that GET-hit a POST-only endpoint -> 405). POST still
performs the irreversible erase."""

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


def test_get_shows_preview_not_405():
    u = _adult("del_me")
    client = Client()
    client.force_login(u)
    resp = client.get("/account/delete/")
    assert resp.status_code == 200  # was a 405 before F33 (GET on a @require_POST view)
    body = resp.content.decode()
    assert "What gets permanently deleted" in body
    assert "What lawfully stays" in body
    # The audit-survivor honesty line is present (de-identified, permanent audit log).
    assert "audit log" in body.lower()


def test_my_privacy_delete_link_resolves_to_preview():
    u = _adult("del_link")
    client = Client()
    client.force_login(u)
    privacy = client.get("/my-privacy/").content.decode()
    assert "/account/delete/" in privacy  # the link target
    assert client.get("/account/delete/").status_code == 200  # and it no longer 405s


def test_post_still_erases_and_logs_out():
    u = _adult("del_confirm")
    uid = u.id
    client = Client()
    client.force_login(u)
    resp = client.post("/account/delete/")
    assert resp.status_code == 302
    assert not User.objects.filter(id=uid).exists()


def test_preview_requires_login():
    resp = Client().get("/account/delete/")
    assert resp.status_code in (301, 302)  # @login_required -> redirect
