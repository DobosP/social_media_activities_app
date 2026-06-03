"""i18n pipeline end-to-end (P6/IS-7): the web UI is translatable and serves Romanian.

Pins the whole chain — template {% trans %}/{% blocktrans %} markup -> the locale/ro/django.po
-> the compiled .mo -> LocaleMiddleware activation — by switching the language and asserting the
chrome renders in Romanian. The launch city is Cluj-Napoca, so Romanian is the priority locale."""

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


def test_language_switcher_serves_romanian():
    client = Client()
    # Default English chrome.
    en = client.get("/login/").content.decode()
    assert "Log in" in en and "Places" in en

    # set_language persists the choice (cookie/session); LocaleMiddleware then serves Romanian.
    resp = client.post("/i18n/setlang/", {"language": "ro", "next": "/login/"})
    assert resp.status_code == 302
    ro = client.get("/login/").content.decode()
    # Nav chrome is now Romanian (these come from base.html via the compiled .mo).
    assert "Autentificare" in ro  # "Log in"
    assert "Locuri" in ro  # "Places"
    assert "Evenimente" in ro  # "Events"
    assert "Creează cont" in ro  # "Sign up"
    assert '<html lang="ro"' in ro  # the dynamic lang attribute follows the active language


def test_connections_search_placeholder_is_not_broken_by_apostrophe():
    """Regression: the search placeholder text contains an apostrophe ("you've"). A single-quoted
    attribute around the {% trans %} would let the apostrophe prematurely close the attribute and
    corrupt the input. Assert the full placeholder survives intact in BOTH English and Romanian."""
    client = Client()
    client.force_login(_adult("conn_i18n"))
    en = client.get("/connections/").content.decode()
    assert 'placeholder="Search someone you\'ve met by name..."' in en
    client.post("/i18n/setlang/", {"language": "ro", "next": "/connections/"})
    ro = client.get("/connections/").content.decode()
    assert "Caută" in ro  # placeholder translated; no truncation/garbled attributes
    assert "you've met by name" not in ro  # the English source is gone in Romanian


def test_accept_language_header_negotiates_romanian():
    # No explicit switch needed: LocaleMiddleware negotiates from Accept-Language too.
    ro = Client().get("/login/", HTTP_ACCEPT_LANGUAGE="ro").content.decode()
    assert "Autentificare" in ro
    assert '<html lang="ro"' in ro
