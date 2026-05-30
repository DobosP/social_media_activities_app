"""Web tests for wave-8: F29 (transparency + receipts), F34 (campaigns), F37 (partners)."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.donations.models import Campaign, Donation, SpendEntry
from apps.places.models import Partner, Place
from apps.web.templatetags.money import cents

pytestmark = pytest.mark.django_db

PW = "sup3r-secret-pw"


def _user(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _client(user=None):
    c = Client()
    if user:
        c.force_login(user)
    return c


def _completed(amount, *, donor=None, campaign=None, ref):
    return Donation.objects.create(
        amount_cents=amount,
        donor=donor,
        campaign=campaign,
        provider="dev",
        external_ref=ref,
        status=Donation.Status.COMPLETED,
    )


# --- F29 -------------------------------------------------------------------------------


def test_cents_filter():
    assert cents(1234) == "12.34"
    assert cents(0) == "0.00"
    assert cents(None) == "0.00"


def test_transparency_public_and_aggregate_only():
    donor = _user("td")
    _completed(5000, donor=donor, ref="t1")
    SpendEntry.objects.create(category="EU hosting", amount_cents=2000)
    body = _client().get("/transparency/").content.decode()  # anonymous
    assert "EU hosting" in body
    assert "50.00 EUR" in body  # raised, formatted via |cents
    assert "td" not in body  # NO donor name on the public page
    # No goal-bar / urgency framing between raised and allocated.
    assert "progressbar" not in body
    assert "of goal" not in body
    assert "countdown" not in body


def test_my_donations_requires_login():
    assert _client().get("/my-donations/").status_code in (301, 302)


def test_my_donations_is_self_only():
    a, b = _user("da"), _user("db")
    _completed(1000, donor=a, ref="mine-a")
    _completed(2000, donor=b, ref="mine-b")
    body = _client(a).get("/my-donations/").content.decode()
    assert "mine-a" in body
    assert "mine-b" not in body  # never another donor's receipt (self-only by construction)


# --- F34 -------------------------------------------------------------------------------


def test_donate_form_lists_only_active_campaigns():
    Campaign.objects.create(title="Active Gear", slug="gear", goal_cents=10000, is_active=True)
    Campaign.objects.create(title="Closed Drive", slug="closed", goal_cents=10000, is_active=False)
    body = _client(_user("df")).get("/donate/").content.decode()
    assert "Active Gear" in body
    assert "Closed Drive" not in body
    assert "General fund" in body  # default empty option


def test_campaigns_page_is_calm_and_static():
    c = Campaign.objects.create(title="Youth gear", slug="yg", goal_cents=10000)
    _completed(2500, campaign=c, ref="c1")
    body = _client().get("/campaigns/").content.decode()  # public
    assert "Youth gear" in body
    assert "25.00 of 100.00 EUR" in body  # raised of goal, integer-cents formatted
    assert 'role="progressbar"' in body  # a static, accessible bar
    # No dark patterns: no countdown/scarcity/vanity/auto-refresh/JS animation.
    for bad in ("countdown", "to go", "left!", "people donated", "donors gave", "http-equiv"):
        assert bad not in body
    assert "<script" not in body


# --- F37 -------------------------------------------------------------------------------


def _place():
    return Place.objects.create(
        name="Town Library", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


def test_partners_page_lists_only_verified():
    Partner.objects.create(name="Cluj Library", kind=Partner.Kind.LIBRARY, is_verified=True)
    Partner.objects.create(name="Pending NGO", kind=Partner.Kind.NGO, is_verified=False)
    body = _client().get("/partners/").content.decode()  # public
    assert "Cluj Library" in body
    assert "Pending NGO" not in body  # unverified never public
    assert "<img" not in body  # text-only, no logo/ad surface


def test_place_detail_shows_verified_partner_line():
    place = _place()
    Partner.objects.create(
        name="Cluj Library", kind=Partner.Kind.LIBRARY, place=place, is_verified=True
    )
    body = _client(_user("pp")).get(f"/places/{place.id}/").content.decode()
    assert "In partnership with" in body
    assert "Cluj Library" in body


def test_place_detail_hides_unverified_partner():
    place = _place()
    Partner.objects.create(name="Secret Org", kind=Partner.Kind.NGO, place=place, is_verified=False)
    body = _client(_user("pp2")).get(f"/places/{place.id}/").content.decode()
    assert "In partnership with" not in body
