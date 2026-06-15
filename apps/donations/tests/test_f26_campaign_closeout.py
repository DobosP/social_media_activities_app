"""W2-F26 — spend-tied campaign close-out: when staff close an earmarked campaign and publish a
plain-text outcome, donors see what their gift funded. Aggregate-only, no donor PII, no goal or
scarcity framing; a campaign appears ONLY with BOTH closed_at and a non-empty outcome."""

import pytest
from django.test import Client
from django.utils import timezone

from apps.donations.models import Campaign, Donation, SpendEntry
from apps.donations.services import completed_campaigns_with_outcomes

pytestmark = pytest.mark.django_db


def _campaign(slug, *, outcome="", closed=False, goal=100000):
    return Campaign.objects.create(
        title=f"Campaign {slug}",
        slug=slug,
        goal_cents=goal,
        outcome=outcome,
        closed_at=timezone.now() if closed else None,
    )


def _donation(campaign, cents, *, completed=True):
    return Donation.objects.create(
        campaign=campaign,
        amount_cents=cents,
        provider="dev",
        status=Donation.Status.COMPLETED if completed else Donation.Status.PENDING,
    )


def _page():
    return Client().get("/campaigns/").content.decode()


def test_only_closed_campaigns_with_an_outcome_appear():
    _campaign("active-no-outcome")  # active, no outcome -> excluded
    _campaign(
        "closed-no-outcome", closed=True
    )  # closed but no outcome -> excluded (no false claim)
    _campaign("outcome-not-closed", outcome="We did it")  # outcome but not closed -> excluded
    _campaign("whitespace", outcome="   ", closed=True)  # blank-ish outcome -> excluded
    _campaign("done", outcome="Funded 200 reading hours.", closed=True)  # both -> included
    slugs = {r["slug"] for r in completed_campaigns_with_outcomes()}
    assert slugs == {"done"}


def test_raised_is_aggregated_completed_only():
    c = _campaign("aggr", outcome="Bought 30 chess sets.", closed=True)
    _donation(c, 5000)
    _donation(c, 2500)
    _donation(c, 9999, completed=False)  # pending must NOT count
    row = next(r for r in completed_campaigns_with_outcomes() if r["slug"] == "aggr")
    assert row["raised_cents"] == 7500


def test_linked_spend_rows_attach_untagged_excluded():
    c = _campaign("spend", outcome="Repaired the library roof.", closed=True)
    SpendEntry.objects.create(category="Roof repair", amount_cents=4000, campaign=c)
    SpendEntry.objects.create(category="General admin", amount_cents=1000)  # untagged -> not here
    row = next(r for r in completed_campaigns_with_outcomes() if r["slug"] == "spend")
    cats = {s["category"] for s in row["spend_entries"]}
    assert cats == {"Roof repair"}


def test_spend_survives_campaign_deletion_set_null():
    c = _campaign("survive", outcome="Done.", closed=True)
    entry = SpendEntry.objects.create(category="Venue", amount_cents=3000, campaign=c)
    c.delete()
    entry.refresh_from_db()
    assert entry.campaign_id is None  # financial record kept; just untagged now


def test_campaigns_page_renders_closeout_without_goal_bar():
    c = _campaign("page", outcome="Funded a season of meetups.", closed=True)
    _donation(c, 8000)
    SpendEntry.objects.create(category="Equipment", amount_cents=8000, campaign=c)
    html = _page()
    assert "Completed campaigns" in html
    assert "Funded a season of meetups." in html
    assert "Equipment" in html
    # The neutral close-out must carry NO goal-bar / scarcity framing. With no ACTIVE campaign on
    # the page, the bar markup + "goal" wording must be entirely absent (the active bar adds them).
    assert "progressbar" not in html
    assert "aria-valuenow" not in html
    assert "goal" not in html
