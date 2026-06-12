"""W3: the Settings hub consolidates language / preferences / privacy / account-danger
controls, the top bar slims to an avatar-button menu, and the language switcher moves
to the footer. The cohort badge is no longer broadcast in the chrome (W7)."""

import pytest

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance

pytestmark = pytest.mark.django_db


def _user(name="settings-user"):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def test_settings_requires_login(client):
    resp = client.get("/settings/")
    assert resp.status_code == 302
    assert "/login/" in resp["Location"]


def test_settings_page_has_all_controls(client):
    client.force_login(_user())
    page = client.get("/settings/").content.decode()
    # language form posts to set_language
    assert "/i18n/setlang/" in page
    # account danger zone
    assert "/account/export/" in page
    assert "/account/delete/" in page
    # preference + privacy links
    for path in ["/display/", "/notifications/preferences/", "/access/", "/my-privacy/"]:
        assert path in page, path


def test_nav_shows_avatar_menu_and_no_cohort_badge(client):
    client.force_login(_user("settings-nav"))
    page = client.get("/").content.decode()
    assert "nav-avatar" in page  # generated-avatar account button
    assert "/settings/" in page
    # the chrome no longer announces the viewer's cohort on every page (W7)
    assert "Signed in &middot; Adult" not in page
    # language switcher lives in the footer once, not in the top nav
    assert page.count("lang-switch") == 1
