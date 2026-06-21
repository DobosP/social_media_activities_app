"""DSA Art.17 pre-auth redress surface (/account/restricted/) + the F19 logged-in contest.

A suspended/banned account (is_active=False) can't get a session, so it proves credentials here to
READ why and to CONTEST — without ever being logged in.
"""

import re

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.safety.models import ModerationAction, ModerationAppeal, ReasonCode
from apps.safety.services import take_action

pytestmark = pytest.mark.django_db


def _user(name, staff=False):
    u = User.objects.create_user(username=name, password="pw", display_name=name, is_staff=staff)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _suspend(user, mod, reason=ReasonCode.HARASSMENT):
    action = take_action(mod, user, ModerationAction.Action.SUSPEND, reason)
    user.refresh_from_db()
    assert user.is_active is False
    return action


def test_get_shows_credential_form():
    resp = Client().get("/account/restricted/")
    assert resp.status_code == 200
    assert b'name="password"' in resp.content


def test_wrong_credentials_reveal_nothing():
    mod = _user("ar_mod", staff=True)
    user = _user("ar_user")
    _suspend(user, mod)
    resp = Client().post("/account/restricted/", {"username": "ar_user", "password": "WRONG"})
    assert resp.status_code == 200
    # (apostrophe in "couldn't" is HTML-escaped, so match an apostrophe-free substring)
    assert b"verify those details" in resp.content
    # No statement of reasons / reason label leaked to a failed credential attempt.
    assert b"Harassment" not in resp.content


def test_valid_credentials_show_statement_without_logging_in():
    mod = _user("ar_mod2", staff=True)
    user = _user("ar_user2")
    _suspend(user, mod)
    client = Client()
    resp = client.post("/account/restricted/", {"username": "ar_user2", "password": "pw"})
    assert resp.status_code == 200
    assert b"Harassment" in resp.content  # the reason is shown
    assert b'name="appeal_token"' in resp.content  # a contest is offered
    # CRUCIAL: proving credentials here must NOT create an authenticated session.
    assert "_auth_user_id" not in client.session


def test_active_account_reveals_no_moderation_detail():
    _user("ar_active")  # never sanctioned, still active
    resp = Client().post("/account/restricted/", {"username": "ar_active", "password": "pw"})
    assert resp.status_code == 200
    assert b"active" in resp.content.lower()
    assert b'name="appeal_token"' not in resp.content


def test_self_deactivated_shows_no_statement():
    user = _user("ar_self")
    user.is_active = False  # deactivated, but NOT by a moderation action
    user.save(update_fields=["is_active"])
    resp = Client().post("/account/restricted/", {"username": "ar_self", "password": "pw"})
    assert resp.status_code == 200
    assert b'name="appeal_token"' not in resp.content
    assert b"no moderation decision" in resp.content.lower()


def test_appeal_via_token_files_the_appeal():
    mod = _user("ar_mod3", staff=True)
    user = _user("ar_user3")
    action = _suspend(user, mod)
    client = Client()
    page = client.post("/account/restricted/", {"username": "ar_user3", "password": "pw"})
    token = re.search(rb'name="appeal_token" value="([^"]+)"', page.content).group(1).decode()
    resp = client.post(
        "/account/restricted/",
        {"appeal_token": token, "statement": "I believe this was a mistake"},
    )
    assert resp.status_code == 200
    assert b"received" in resp.content.lower()
    appeal = ModerationAppeal.objects.get(action=action)
    assert appeal.appellant == user
    assert appeal.statement == "I believe this was a mistake"


def test_brute_force_lockout():
    mod = _user("ar_mod4", staff=True)
    _suspend(_user("ar_user4"), mod)
    client = Client()
    # Exhaust the failure budget (LOGIN_FAILURE_LIMIT defaults to 10).
    for _ in range(10):
        client.post("/account/restricted/", {"username": "ar_user4", "password": "x"})
    resp = client.post("/account/restricted/", {"username": "ar_user4", "password": "pw"})
    assert b"Too many attempts" in resp.content


# --- F19 logged-in contest ----------------------------------------------------------------


def test_safety_record_contest_files_appeal():
    mod = _user("sc_mod", staff=True)
    user = _user("sc_user")
    # A WARN keeps the account active, so the user can contest from /my-safety-record/ logged-in.
    action = take_action(mod, user, ModerationAction.Action.WARN, ReasonCode.SPAM)
    client = Client()
    client.force_login(user)
    resp = client.post(
        "/my-safety-record/contest/",
        {"action_id": action.id, "statement": "the warning was unfair"},
        follow=True,
    )
    assert resp.status_code == 200
    assert ModerationAppeal.objects.filter(action=action, appellant=user).exists()


def test_safety_record_contest_404_for_other_users_action():
    mod = _user("sc_mod2", staff=True)
    user = _user("sc_user2")
    other = _user("sc_other2")
    action = take_action(mod, other, ModerationAction.Action.WARN, ReasonCode.SPAM)
    client = Client()
    client.force_login(user)
    resp = client.post(
        "/my-safety-record/contest/", {"action_id": action.id, "statement": "not mine"}
    )
    assert resp.status_code == 404
    assert ModerationAppeal.objects.count() == 0
