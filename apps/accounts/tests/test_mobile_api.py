"""W10 mobile-readiness: opaque token obtain/use/revoke + the self-scoped settings API
(mutes can never include the non-mutable MODERATION/SYSTEM kinds)."""

import pytest
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.notifications.services import get_muted_kinds

pytestmark = pytest.mark.django_db


def _user(name="mob-user", password="pw12345"):
    u = User.objects.create_user(username=name, password=password, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_token_obtain_use_and_revoke():
    user = _user()
    api = APIClient()
    bad = api.post("/api/auth/token/", {"username": user.username, "password": "wrong"})
    assert bad.status_code == 400
    ok = api.post("/api/auth/token/", {"username": user.username, "password": "pw12345"})
    assert ok.status_code == 200
    token = ok.json()["token"]

    bearer = APIClient()
    bearer.credentials(HTTP_AUTHORIZATION=f"Token {token}")
    me = bearer.get("/api/accounts/me/")
    assert me.status_code == 200 and me.json()["username"] == user.username

    # revoke = mobile logout; the token stops working immediately
    assert bearer.delete("/api/auth/token/").status_code == 204
    assert bearer.get("/api/accounts/me/").status_code == 401


def test_me_settings_roundtrip_and_non_mutable_guard():
    user = _user("mob-settings")
    api = APIClient()
    api.force_authenticate(user)
    initial = api.get("/api/accounts/me/settings/").json()
    assert initial["muted_kinds"] == []
    assert initial["access"]["needs_step_free"] is False

    resp = api.put(
        "/api/accounts/me/settings/",
        {
            # MODERATION can never be muted — set_muted_kinds silently drops it.
            "muted_kinds": [Notification.Kind.EVENT_REMINDER, Notification.Kind.MODERATION],
            "access": {"needs_step_free": True},
        },
        format="json",
    )
    assert resp.status_code == 200
    body = resp.json()
    assert Notification.Kind.EVENT_REMINDER in body["muted_kinds"]
    assert Notification.Kind.MODERATION not in body["muted_kinds"]
    assert body["access"]["needs_step_free"] is True
    assert Notification.Kind.MODERATION not in get_muted_kinds(user)


def test_me_settings_hearing_loop_is_readable_and_not_wiped_on_roundtrip():
    # F32: the DRF settings surface must read AND write needs_hearing_loop exactly like the web
    # /access/ form (same service). A round-trip that omitted it used to silently wipe it.
    from apps.places.services import get_access_preference, set_access_preference

    user = _user("mob-hearing")
    set_access_preference(user, needs_hearing_loop=True)  # as if set via the web form
    api = APIClient()
    api.force_authenticate(user)

    # GET exposes it (was previously absent from the dict).
    assert api.get("/api/accounts/me/settings/").json()["access"]["needs_hearing_loop"] is True

    # A client that reads-modifies-writes the FULL access object keeps it set.
    resp = api.put(
        "/api/accounts/me/settings/",
        {"access": {"needs_step_free": True, "needs_hearing_loop": True}},
        format="json",
    )
    assert resp.status_code == 200
    assert resp.json()["access"]["needs_hearing_loop"] is True
    assert get_access_preference(user).needs_hearing_loop is True
