"""W3-F20 — in-kind (non-cash) contribution ledger beside the money ledger on /transparency/.
Aggregate-only, donor-FK-free, NEVER summed into the euro figures or framed as an 'X of Y' bar.
"""

import pytest
from django.core.exceptions import ValidationError
from django.test import Client

from apps.donations.models import InKindContribution, SpendEntry
from apps.donations.services import (
    completed_total_cents,
    in_kind_by_category,
    spend_total_cents,
)
from apps.places.models import Partner

pytestmark = pytest.mark.django_db


def _partner(name="Cluj City Library", *, verified=True, active=True):
    return Partner.objects.create(
        name=name, kind=Partner.Kind.LIBRARY, is_verified=verified, is_active=active
    )


def _in_kind(
    category, *, quantity=None, unit_text="", value_cents=None, currency="EUR", partner=None
):
    return InKindContribution.objects.create(
        category=category,
        quantity=quantity,
        unit_text=unit_text,
        value_cents=value_cents,
        currency=currency,
        partner=partner,
    )


def _page():
    return Client().get("/transparency/").content.decode()


def test_grouped_by_category_sums_within_unit():
    _in_kind("Library room hours", quantity=10, unit_text="room-hours")
    _in_kind("Library room hours", quantity=5, unit_text="room-hours")
    row = next(r for r in in_kind_by_category() if r["category"] == "Library room hours")
    assert row["total_quantity"] == 15  # summed within the same unit
    assert row["unit_text"] == "room-hours"


def test_different_units_in_same_category_stay_separate():
    _in_kind("Equipment", quantity=20, unit_text="balls")
    _in_kind("Equipment", quantity=3, unit_text="kits")
    units = {
        r["unit_text"]: r["total_quantity"]
        for r in in_kind_by_category()
        if r["category"] == "Equipment"
    }
    assert units == {"balls": 20, "kits": 3}  # never summed across incompatible units


def test_value_is_optional_quantity_only_entry():
    _in_kind("Mentoring", quantity=12, unit_text="sessions")  # no value_cents
    row = next(r for r in in_kind_by_category() if r["category"] == "Mentoring")
    assert row["total_quantity"] == 12
    assert row["total_cents"] is None  # no euro value -> stays None, never coerced to 0


def test_not_summed_into_cash_figures():
    _in_kind("Room hours", quantity=10, unit_text="room-hours", value_cents=50000)
    SpendEntry.objects.create(category="Hosting", amount_cents=20000)
    assert completed_total_cents() == 0  # no donations; in-kind value never counts here
    assert spend_total_cents() == 20000  # only SpendEntry; in-kind excluded


def test_clean_rejects_non_public_partner():
    c = InKindContribution(category="Room hours", partner=_partner(verified=False))
    with pytest.raises(ValidationError):
        c.full_clean()


def test_clean_allows_public_partner():
    InKindContribution(category="Room hours", partner=_partner()).full_clean()  # must not raise


def test_deleting_partner_leaves_contribution():
    p = _partner()
    e = _in_kind("Room hours", quantity=5, unit_text="room-hours", partner=p)
    p.delete()
    e.refresh_from_db()
    assert e.partner_id is None  # SET_NULL — record survives, just uncredited


def test_admin_formfield_limits_partner_to_public():
    from django.contrib.admin.sites import AdminSite

    from apps.donations.admin import InKindContributionAdmin

    public = _partner("Verified Lib")
    hidden = _partner("Pending NGO", verified=False)
    admin = InKindContributionAdmin(InKindContribution, AdminSite())
    field = InKindContribution._meta.get_field("partner")
    formfield = admin.formfield_for_foreignkey(field, request=None)
    ids = set(formfield.queryset.values_list("id", flat=True))
    assert public.id in ids and hidden.id not in ids


def test_transparency_page_renders_in_kind_separately_without_a_ratio():
    _in_kind("Library room hours", quantity=20, unit_text="room-hours", value_cents=80000)
    html = _page()
    assert "In-kind contributions" in html
    assert "Library room hours" in html
    assert "20 room-hours" in html
    assert "800.00" in html  # 80000 cents estimated value via |cents
    assert "of EUR" not in html  # inv.2: never an 'X of Y' goal/ratio bar


def test_no_section_when_no_in_kind_entries():
    html = _page()
    assert "In-kind contributions" not in html
