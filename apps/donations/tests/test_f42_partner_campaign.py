"""F42 — a campaign may credit a verified civic Partner, shown as a one-line text credit on
/campaigns/. Gated to Partner.objects.public() at write time (clean + admin) AND read time
(active_campaigns_with_progress), so a de-verified/inactive/deleted partner is never credited.
"""

import pytest
from django.contrib.auth import get_user_model
from django.core.exceptions import ValidationError
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand
from apps.donations.models import Campaign, Donation
from apps.donations.services import active_campaigns_with_progress
from apps.places.models import Partner

pytestmark = pytest.mark.django_db
User = get_user_model()
CREDIT = "In partnership with"
BLURB = "Cluj City Library reading hour"


def _partner(name="Cluj City Library", *, verified=True, active=True, blurb=BLURB, website=""):
    return Partner.objects.create(
        name=name,
        kind=Partner.Kind.LIBRARY,
        blurb=blurb,
        website=website,
        is_verified=verified,
        is_active=active,
    )


def _campaign(slug="reading", *, partner=None, active=True):
    return Campaign.objects.create(
        title="Saturday reading hour",
        slug=slug,
        goal_cents=100000,
        is_active=active,
        partner=partner,
    )


def _campaigns_page():
    return Client().get("/campaigns/").content.decode()


# --- read-time gate (the robust chokepoint) --------------------------------------------------


def test_public_partner_is_credited():
    _campaign(partner=_partner())
    rows = active_campaigns_with_progress()
    assert rows[0]["partner_name"] == "Cluj City Library"
    assert rows[0]["partner_blurb"] == BLURB
    html = _campaigns_page()
    assert CREDIT in html and "Cluj City Library" in html and BLURB in html


@pytest.mark.parametrize("verified,active", [(False, True), (True, False), (False, False)])
def test_non_public_partner_is_not_credited(verified, active):
    # Named at write time but later de-verified/deactivated: the read gate drops the credit.
    _campaign(partner=_partner(verified=verified, active=active))
    rows = active_campaigns_with_progress()
    assert rows[0]["partner_name"] == ""
    assert CREDIT not in _campaigns_page()


def test_no_partner_means_no_credit():
    _campaign(partner=None)
    rows = active_campaigns_with_progress()
    assert rows[0]["partner_name"] == ""
    assert CREDIT not in _campaigns_page()


def test_partner_website_renders_as_a_sanitised_link():
    _campaign(partner=_partner(website="https://example.org"))
    assert active_campaigns_with_progress()[0]["partner_website"] == "https://example.org"
    assert 'href="https://example.org"' in _campaigns_page()


def test_malicious_partner_website_is_never_a_live_link():
    # Even a javascript: URL forced past Partner.save()'s strip (via .update) must be neutralised
    # at render by |safe_href — the load-bearing XSS defence on the credit's href.
    p = _partner(website="https://ok.example")
    c = _campaign(partner=p)
    Partner.objects.filter(pk=p.pk).update(website="javascript:alert(1)")
    html = _campaigns_page()
    assert "javascript:" not in html  # no live scheme reaches the page
    assert CREDIT in html and p.name in html  # the name still shows, just not as a link
    assert 'href="javascript:alert(1)"' not in html
    assert c.partner_id == p.pk  # sanity: still credited


def test_name_only_partner_has_no_dangling_separator():
    _campaign(partner=_partner(blurb=""))
    rows = active_campaigns_with_progress()
    assert rows[0]["partner_name"] == "Cluj City Library"
    assert rows[0]["partner_blurb"] == ""
    html = _campaigns_page()
    assert CREDIT in html and "Cluj City Library" in html
    assert "Cluj City Library &mdash;" not in html  # no separator with an empty blurb


def test_inactive_campaign_is_not_listed_so_partner_credit_is_moot():
    _campaign(slug="closed", partner=_partner(), active=False)
    assert active_campaigns_with_progress() == []  # campaign-level gating short-circuits first
    assert CREDIT not in _campaigns_page()


# --- write-time gate (defence in depth) ------------------------------------------------------


@pytest.mark.parametrize("verified,active", [(False, True), (True, False)])
def test_clean_rejects_non_public_partner(verified, active):
    # Both halves of public() (verified AND active) must block naming — not just verified.
    bad = _partner(verified=verified, active=active)
    c = Campaign(title="x", slug="x", goal_cents=100000, partner=bad)
    with pytest.raises(ValidationError):
        c.full_clean()


def test_clean_allows_public_partner():
    good = _partner()
    c = Campaign(title="ok", slug="ok", goal_cents=100000, partner=good)
    c.full_clean()  # must not raise


def test_admin_formfield_limits_partner_to_public():
    from django.contrib.admin.sites import AdminSite

    from apps.donations.admin import CampaignAdmin

    public = _partner("Verified Lib")
    hidden = _partner("Pending NGO", verified=False)
    admin = CampaignAdmin(Campaign, AdminSite())
    field = Campaign._meta.get_field("partner")
    formfield = admin.formfield_for_foreignkey(field, request=None)
    ids = set(formfield.queryset.values_list("id", flat=True))
    assert public.id in ids and hidden.id not in ids


# --- SET_NULL safety + no donor PII ----------------------------------------------------------


def test_deleting_partner_leaves_campaign_general_fund_safe():
    p = _partner()
    c = _campaign(partner=p)
    p.delete()
    c.refresh_from_db()
    assert c.partner_id is None  # SET_NULL — campaign survives, just uncredited
    assert active_campaigns_with_progress()[0]["partner_name"] == ""
    assert CREDIT not in _campaigns_page()


def test_credit_does_not_expose_donor_pii():
    donor = User.objects.create_user(username="secretdonor", password="pw", display_name="Donor X")
    apply = AssuranceResult(age_band=AgeBand.ADULT, provider="dev")
    from apps.accounts.services import apply_assurance

    apply_assurance(donor, apply)
    c = _campaign(partner=_partner())
    Donation.objects.create(
        amount_cents=5000,
        donor=donor,
        campaign=c,
        provider="dev",
        external_ref="f42-ref",
        status=Donation.Status.COMPLETED,
    )
    html = _campaigns_page()
    assert CREDIT in html  # partner credited
    assert "secretdonor" not in html and "Donor X" not in html  # but never a donor identity
