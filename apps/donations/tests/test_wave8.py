"""F29 (spend ledger) + F34 (earmarked campaigns) at the donations service/model layer."""

import pytest
from django.db import IntegrityError, transaction
from django.test import override_settings

from apps.donations.models import Campaign, Donation, SpendEntry
from apps.donations.services import (
    DonationError,
    active_campaigns_with_progress,
    campaign_progress,
    completed_total_cents,
    spend_by_category,
    spend_total_cents,
    start_donation,
)

pytestmark = pytest.mark.django_db

DEV = "apps.donations.providers.DevPaymentProvider"


def _completed(amount, *, campaign=None, ref, currency="EUR"):
    return Donation.objects.create(
        amount_cents=amount,
        currency=currency,
        campaign=campaign,
        provider="dev",
        external_ref=ref,
        status=Donation.Status.COMPLETED,
    )


# --- F29: spend ledger -----------------------------------------------------------------


def test_spend_by_category_aggregates_eur_only():
    SpendEntry.objects.create(category="Hosting", amount_cents=1000)
    SpendEntry.objects.create(category="Hosting", amount_cents=500)
    SpendEntry.objects.create(category="Safety", amount_cents=2000)
    SpendEntry.objects.create(category="Hosting", amount_cents=300, currency="USD")  # excluded
    rows = spend_by_category("EUR")
    by = {r["category"]: r["total_cents"] for r in rows}
    assert by == {"Hosting": 1500, "Safety": 2000}
    assert rows[0]["category"] == "Safety"  # ordered by -total_cents


def test_spend_total_cents():
    SpendEntry.objects.create(category="A", amount_cents=1000)
    SpendEntry.objects.create(category="B", amount_cents=2000)
    assert spend_total_cents("EUR") == 3000
    assert spend_total_cents("USD") == 0


def test_spendentry_nonneg_constraint():
    with pytest.raises(IntegrityError), transaction.atomic():
        SpendEntry.objects.create(category="X", amount_cents=-1)


# --- F34: campaigns --------------------------------------------------------------------


@override_settings(DONATIONS_PROVIDER=DEV)
def test_general_fund_path_unchanged():
    donation, _ = start_donation(None, 500)
    assert donation.campaign is None  # default = general fund


@override_settings(DONATIONS_PROVIDER=DEV)
def test_earmark_active_campaign():
    c = Campaign.objects.create(title="Gear", slug="gear", goal_cents=10000)
    donation, _ = start_donation(None, 500, campaign=c)
    assert donation.campaign_id == c.id


@override_settings(DONATIONS_PROVIDER=DEV)
def test_rejects_inactive_campaign_and_creates_no_row():
    c = Campaign.objects.create(title="Old", slug="old", goal_cents=10000, is_active=False)
    with pytest.raises(DonationError):
        start_donation(None, 500, campaign=c)
    assert Donation.objects.count() == 0


def test_campaign_progress_counts_only_completed_for_this_campaign():
    c = Campaign.objects.create(title="C", slug="c", goal_cents=10000)
    _completed(2000, campaign=c, ref="a")
    Donation.objects.create(
        amount_cents=5000,
        campaign=c,
        provider="dev",
        external_ref="b",
        status=Donation.Status.PENDING,
    )  # pending — not counted
    _completed(9999, ref="g")  # general fund — not this campaign
    prog = campaign_progress(c)
    assert prog["raised_cents"] == 2000
    assert prog["percent"] == 20  # 2000/10000 floor


def test_percent_capped_at_100():
    c = Campaign.objects.create(title="Cap", slug="cap", goal_cents=1000)
    _completed(5000, campaign=c, ref="z")
    assert campaign_progress(c)["percent"] == 100  # never over-goal vanity


def test_goal_min_constraint():
    with pytest.raises(IntegrityError), transaction.atomic():
        Campaign.objects.create(title="Tiny", slug="tiny", goal_cents=50)


def test_deleting_campaign_keeps_donations_and_total():
    c = Campaign.objects.create(title="Del", slug="del", goal_cents=10000)
    _completed(3000, campaign=c, ref="d")
    before = completed_total_cents("EUR")
    c.delete()
    donation = Donation.objects.get(external_ref="d")
    assert donation.campaign is None  # SET_NULL → falls back to general fund
    assert completed_total_cents("EUR") == before  # grand total unchanged


def test_active_campaigns_with_progress_is_aggregate_only():
    c = Campaign.objects.create(title="Run", slug="run", goal_cents=10000)
    Campaign.objects.create(title="Hidden", slug="hidden", goal_cents=10000, is_active=False)
    _completed(2500, campaign=c, ref="x")
    rows = active_campaigns_with_progress()
    assert len(rows) == 1  # inactive excluded
    assert rows[0]["raised_cents"] == 2500
    assert rows[0]["percent"] == 25
    # Only safe aggregate keys — never a donations queryset or donor data.
    assert set(rows[0]) == {
        "title",
        "slug",
        "description",
        "raised_cents",
        "goal_cents",
        "currency",
        "percent",
    }
