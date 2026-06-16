"""W3-F19 — staff-authored 'what a gift makes possible' cost anchors on the donate page.
Illustrative only: a label + amount, never a live ratio against actuals, never a goal/scarcity bar.
"""

import pytest
from django.test import Client

from apps.donations.models import CostAnchor
from apps.donations.services import cost_anchors

pytestmark = pytest.mark.django_db


def _anchor(label, cents, *, active=True, currency="EUR", category=""):
    return CostAnchor.objects.create(
        label=label,
        amount_cents=cents,
        currency=currency,
        spend_category=category,
        is_active=active,
    )


def test_cost_anchors_returns_active_only_largest_first():
    _anchor("Small gift", 1000)
    _anchor("Big gift", 5000)
    _anchor("Retired", 9000, active=False)
    labels = [r["label"] for r in cost_anchors()]
    assert labels == ["Big gift", "Small gift"]  # active only, largest amount first


def test_cost_anchors_filters_currency():
    _anchor("Euro anchor", 2000, currency="EUR")
    _anchor("Pound anchor", 3000, currency="GBP")
    assert [r["label"] for r in cost_anchors("EUR")] == ["Euro anchor"]


def test_cost_anchors_is_capped(settings):
    settings.COST_ANCHORS_MAX = 2
    for i in range(5):
        _anchor(f"Anchor {i}", 1000 + i)
    assert len(cost_anchors()) == 2


def test_cost_anchors_returns_plain_dicts_no_actuals_ratio():
    _anchor("Library room", 4000, category="Library room bookings")
    row = cost_anchors()[0]
    assert set(row.keys()) == {"label", "amount_cents", "currency", "spend_category"}
    assert row["spend_category"] == "Library room bookings"  # decorative label only
    # no computed total / ratio / percent key is ever surfaced
    assert not any(("total" in k) or ("percent" in k) or ("ratio" in k) for k in row)


def test_spend_category_is_not_a_foreign_key():
    field = CostAnchor._meta.get_field("spend_category")
    assert not field.is_relation  # a decorative CharField, never an FK to SpendEntry


def test_positive_amount_constraint():
    from django.db import IntegrityError, transaction

    with pytest.raises(IntegrityError), transaction.atomic():
        CostAnchor.objects.create(label="Zero", amount_cents=0)


def test_donate_page_renders_anchors_without_a_progress_bar():
    _anchor("One youth reading-circle room", 4000)
    html = Client().get("/donate/").content.decode()
    assert "What a gift makes possible" in html
    assert "One youth reading-circle room" in html
    assert "40.00" in html  # 4000 cents via the |cents filter
    assert "of EUR" not in html  # inv.2: never an "X of Y" ratio/goal framing


def test_donate_page_has_no_anchor_section_when_none_active():
    _anchor("Inactive only", 4000, active=False)
    html = Client().get("/donate/").content.decode()
    assert "What a gift makes possible" not in html
