"""W4-F24 — civic-impact year-in-review: staff-authored prose statements on /transparency/.
NARRATIVE only (never an auto-derived count / 'X of Y' bar); no FK path to Activity/Membership/
Donation; aggregate-only, donor-FK-free, mirroring InKindContribution."""

import pytest
from django.core.exceptions import ValidationError
from django.test import Client

from apps.donations.models import CivicOutcome
from apps.donations.services import civic_outcomes
from apps.places.models import Partner

pytestmark = pytest.mark.django_db


def _partner(name="Cluj City Library", *, verified=True, active=True):
    return Partner.objects.create(
        name=name, kind=Partner.Kind.LIBRARY, is_verified=verified, is_active=active
    )


def _outcome(headline, *, detail="", period="", partner=None, is_active=True):
    return CivicOutcome.objects.create(
        headline=headline, detail=detail, period=period, partner=partner, is_active=is_active
    )


def _page():
    return Client().get("/transparency/").content.decode()


def test_civic_outcomes_returns_active_prose_with_partner():
    p = _partner()
    _outcome(
        "Reading circles ran at 4 venues",
        detail="Across the spring season.",
        period="2026 spring",
        partner=p,
    )
    rows = civic_outcomes()
    assert len(rows) == 1
    assert rows[0]["headline"] == "Reading circles ran at 4 venues"
    assert rows[0]["detail"] == "Across the spring season."
    assert rows[0]["period"] == "2026 spring"
    assert rows[0]["partner_name"] == p.name
    # Prose only — no count/total ever (inv.2).
    assert "count" not in rows[0] and "total_cents" not in rows[0]


def test_inactive_outcome_excluded():
    _outcome("Hidden outcome", is_active=False)
    assert civic_outcomes() == []


def test_partner_name_regated_to_public_at_read_time():
    p = _partner()
    _outcome("With a partner", partner=p)
    assert civic_outcomes()[0]["partner_name"] == p.name
    p.is_verified = False
    p.save(update_fields=["is_verified"])
    assert civic_outcomes()[0]["partner_name"] is None  # a deactivated partner's name drops


def test_clean_rejects_non_public_partner():
    o = CivicOutcome(headline="X", partner=_partner(verified=False))
    with pytest.raises(ValidationError):
        o.full_clean()


def test_clean_allows_public_partner():
    CivicOutcome(headline="X", partner=_partner()).full_clean()  # must not raise


def test_transparency_renders_civic_outcomes_as_prose():
    _outcome("Library reading circles ran at 4 partner venues", period="2026 season")
    html = _page()
    assert "What this made possible" in html
    assert "Library reading circles ran at 4 partner venues" in html
    assert "of EUR" not in html  # inv.2: never an 'X of Y' ratio/goal bar


def test_no_section_without_outcomes():
    assert "What this made possible" not in _page()


def test_civic_outcome_has_no_rollup_fk():
    # The load-bearing invariant: CivicOutcome can NEVER surface a per-user/activity rollup, because
    # it has no FK/query path to Activity/Membership/Donation/User — its only relation is the
    # optional Partner credit.
    related = {
        f.related_model.__name__
        for f in CivicOutcome._meta.get_fields()
        if f.is_relation and f.related_model is not None
    }
    for forbidden in ("Activity", "Membership", "Donation", "User"):
        assert forbidden not in related
    assert related <= {"Partner"}
