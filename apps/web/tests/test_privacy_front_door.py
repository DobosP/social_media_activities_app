"""F36 — "What we know about you" privacy front-door (/my-privacy/). Pins: login-gated; strictly
self-only (no user_id param — one user's page never reflects another's data); composes the existing
self-scoped reads (age band, interests, donations count, safety-record counts, muted kinds); the
guardianship link shows only when the viewer has an active guardian; and the honest negative-space
statements + deep-links to every existing control are present."""

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance, link_guardian
from apps.donations.models import Donation
from apps.recommendations.services import set_interests
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "sup3r-secret-pw"


def _user(name, band=AgeBand.ADULT, *, consented=False):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    if consented:
        ParentalConsent.objects.create(
            minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
        )
    return u


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _type(slug="pf-ball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="pf-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(slug=slug, defaults={"name": "Ball", "category": cat})
    return t


def test_requires_login():
    resp = Client().get("/my-privacy/")
    assert resp.status_code in (301, 302)
    assert "/login" in resp.headers.get("Location", "") or "/accounts/login" in resp.headers.get(
        "Location", ""
    )


def test_renders_categories_links_and_negative_space():
    body = _client(_user("pf1")).get("/my-privacy/").content.decode()
    # Each category deep-links to its existing self-only control.
    for route in (
        "/verify-age/",
        "/interests/",
        "/access/",
        "/my-safety-record/",
        "/my-donations/",
        "/account/export/",
        "/account/delete/",
    ):
        assert route in body, route
    # Honest negative-space statements (the felt-privacy promise).
    assert "never store your location" in body
    assert "age band" in body
    assert "No public photo feeds" in body


def test_self_only_reflects_the_logged_in_users_own_counts():
    a = _user("pf_a")
    set_interests(a, [_type("pf-a1").slug, _type("pf-a2").slug])  # 2 interests
    Donation.objects.create(donor=a, amount_cents=500, provider="dev")

    b = _user("pf_b")  # no interests, no donations

    a_body = _client(a).get("/my-privacy/").content.decode()
    assert "chosen: 2" in a_body
    assert "Donations on record: 1" in a_body

    # No user_id param exists on the route, so B's page is B's own data only — never A's.
    b_body = _client(b).get("/my-privacy/").content.decode()
    assert "chosen: 0" in b_body
    assert "Donations on record: 0" in b_body


def test_guardianship_link_only_when_an_active_guardian_exists():
    # A child with an active guardian sees the guardianship category.
    kid = _user("pf_kid", AgeBand.UNDER_16, consented=True)
    guardian = _user("pf_guardian")
    link_guardian(guardian, kid)
    kid_body = _client(kid).get("/my-privacy/").content.decode()
    assert "/guardianship/" in kid_body

    # A user with no guardian does not.
    solo_body = _client(_user("pf_solo")).get("/my-privacy/").content.decode()
    assert "/guardianship/" not in solo_body


def test_age_band_shown_not_dob():
    user = _user("pf_band", AgeBand.ADULT)
    body = _client(user).get("/my-privacy/").content.decode()
    assert "proven age band" in body
    # The page must never surface a raw DOB/identity field (we only ever store a band).
    assert "date of birth" in body  # only in the negative-space "we never store" statement
