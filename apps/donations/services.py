from django.conf import settings
from django.db import transaction
from django.db.models import Count, Q, Sum

from .models import Campaign, CostAnchor, Donation, InKindContribution, SpendEntry
from .providers import get_payment_provider, new_reference


class DonationError(Exception):
    """Expected donation-domain error."""


@transaction.atomic
def start_donation(
    donor, amount_cents: int, currency: str = "EUR", *, recurring: bool = False, campaign=None
):
    """Create a pending donation and a provider checkout intent. Returns (donation, url).
    ``campaign`` is an optional earmark; None = the general fund (the unchanged default path)."""
    if amount_cents < 100:
        raise DonationError("Minimum donation is 1.00.")
    if campaign is not None and not campaign.is_active:
        raise DonationError("That campaign is no longer accepting earmarked gifts.")
    provider = get_payment_provider()
    reference = new_reference()
    intent = provider.create_intent(amount_cents, currency, reference=reference)
    donation = Donation.objects.create(
        donor=donor if (donor and donor.is_authenticated) else None,
        amount_cents=amount_cents,
        currency=currency,
        recurring=recurring,
        campaign=campaign,
        provider=provider.name,
        external_ref=intent.external_ref,
    )
    return donation, intent.checkout_url


def active_campaigns_with_progress() -> list[dict]:
    """Active earmark campaigns, each with a calm static progress figure (F34). One grouped
    query (no N+1); AGGREGATE-only — returns plain dicts, never a Campaign object or a donor
    list, so no per-donor data can leak. percent uses integer math, capped at 100.

    F42: if a campaign credits a partner, the partner's name/blurb/website are exposed ONLY when
    that partner is still public() (verified+active). The check is done in Python on the
    select_related'd partner (no extra query / no N+1), mirroring Partner.objects.public(), so a
    partner deactivated after being named simply stops being credited."""
    qs = (
        # W2-F26: a closed campaign (closed_at set) leaves the active "donate now" list and moves to
        # the close-out section, so it never keeps soliciting after it's been wrapped up.
        Campaign.objects.filter(is_active=True, closed_at__isnull=True)
        .select_related("partner")
        .annotate(
            raised=Sum(
                "donations__amount_cents",
                filter=Q(donations__status=Donation.Status.COMPLETED),
            )
        )
        .order_by("title")
    )
    rows = []
    for c in qs:
        raised = c.raised or 0
        percent = min(100, raised * 100 // c.goal_cents) if c.goal_cents else 0
        # Read-time public() chokepoint, in Python on the joined row (is_verified AND is_active).
        p = c.partner if (c.partner and c.partner.is_verified and c.partner.is_active) else None
        rows.append(
            {
                "title": c.title,
                "slug": c.slug,
                "description": c.description,
                "raised_cents": raised,
                "goal_cents": c.goal_cents,
                "currency": c.currency,
                "percent": percent,
                "partner_name": p.name if p else "",
                "partner_blurb": p.blurb if p else "",
                "partner_website": p.website if p else "",
            }
        )
    return rows


def completed_campaigns_with_outcomes() -> list[dict]:
    """W2-F26: closed earmark campaigns that published a plain-text outcome — the honest "what your
    gift funded" close-out loop. A campaign appears ONLY when it has BOTH closed_at AND a non-empty
    outcome, so a closed campaign with no outcome is correctly omitted (never a false "delivered"
    claim). AGGREGATE-only (no donor objects/PII); linked spend rows are shown beside each (an
    UNTAGGED spend row still tallies globally via spend_by_category but won't appear here — the
    correct fail-safe). Two queries total, no N+1. Deliberately exposes NO goal/percent — a neutral
    ledger close-out, never an "X of Y goal" or "we hit it!" surface."""
    campaigns = [
        c
        for c in (
            Campaign.objects.exclude(closed_at__isnull=True)
            .exclude(outcome="")
            .annotate(
                raised=Sum(
                    "donations__amount_cents",
                    filter=Q(donations__status=Donation.Status.COMPLETED),
                )
            )
            .order_by("-closed_at")
        )
        # Belt-and-suspenders beyond the cheap DB pre-filter: a whitespace-only outcome (reachable
        # only off the admin write path, which strips) is never a published "delivered" claim.
        if c.outcome.strip()
    ]
    spend_by_campaign: dict[int, list] = {}
    for s in (
        SpendEntry.objects.filter(campaign_id__in=[c.id for c in campaigns])
        .values("campaign_id", "category", "amount_cents", "currency", "period")
        .order_by("-amount_cents")
    ):
        spend_by_campaign.setdefault(s["campaign_id"], []).append(s)
    return [
        {
            "title": c.title,
            "slug": c.slug,
            "outcome": c.outcome,
            "closed_at": c.closed_at,
            "raised_cents": c.raised or 0,
            "currency": c.currency,
            "spend_entries": spend_by_campaign.get(c.id, []),
        }
        for c in campaigns
    ]


def campaign_progress(campaign) -> dict:
    """Single-campaign progress (aggregate-only). percent = floor(raised*100/goal), capped 100."""
    raised = (
        Donation.objects.filter(
            campaign=campaign, status=Donation.Status.COMPLETED, currency=campaign.currency
        ).aggregate(s=Sum("amount_cents"))["s"]
        or 0
    )
    percent = min(100, raised * 100 // campaign.goal_cents) if campaign.goal_cents else 0
    return {
        "raised_cents": raised,
        "goal_cents": campaign.goal_cents,
        "currency": campaign.currency,
        "percent": percent,
    }


def completed_total_cents(currency: str = "EUR") -> int:
    """Aggregate of completed donations — public transparency figure, no PII."""
    total = Donation.objects.filter(status=Donation.Status.COMPLETED, currency=currency).aggregate(
        s=Sum("amount_cents")
    )["s"]
    return total or 0


def spend_by_category(currency: str = "EUR") -> list[dict]:
    """Staff-entered spend grouped by category (F29) — aggregate-only, no PII, integer cents."""
    return list(
        SpendEntry.objects.filter(currency=currency)
        .values("category")
        .annotate(total_cents=Sum("amount_cents"))
        .order_by("-total_cents")
    )


def spend_total_cents(currency: str = "EUR") -> int:
    """Grand total of staff-entered spend (F29). Kept separate from completed_total_cents so the
    'received' and 'allocated' figures stay independent and are never framed as 'X of Y'."""
    return SpendEntry.objects.filter(currency=currency).aggregate(s=Sum("amount_cents"))["s"] or 0


def in_kind_by_category(currency: str = "EUR") -> list[dict]:
    """W3-F20: staff-entered NON-CASH civic support, grouped by (category, unit), as plain dicts in
    ONE query (no N+1). DELIBERATELY SEPARATE from the euro ledger: quantities are summed only
    WITHIN a shared unit (never across incompatible units, e.g. room-hours + kits), and the optional
    euro value stays its own figure — never added into completed_total_cents / spend_total_cents,
    never an 'X of Y' bar. Donor-FK-free; identities never exposed."""
    return list(
        InKindContribution.objects.filter(currency=currency)
        .values("category", "unit_text")
        .annotate(
            total_quantity=Sum("quantity"),
            total_cents=Sum("value_cents"),
            n=Count("id"),
        )
        .order_by("category", "unit_text")
    )


def civic_outcomes() -> list[dict]:
    """W4-F24: the staff-authored civic-impact statements for the transparency page, newest first,
    as plain dicts. PROSE ONLY — never an auto-derived count (mirrors in_kind_by_category's
    aggregate-only, donor-FK-free shape; CivicOutcome has NO FK/query path to Activity/Membership/
    Donation). A credited partner's name is re-gated to public() at read time, so a
    since-deactivated partner's name drops to None."""
    from .models import CivicOutcome

    return [
        {
            "headline": o.headline,
            "detail": o.detail,
            "period": o.period,
            "partner_name": (
                o.partner.name
                if (o.partner_id and o.partner.is_verified and o.partner.is_active)
                else None
            ),
        }
        for o in CivicOutcome.objects.filter(is_active=True).select_related("partner")
    ]


def cost_anchors(currency: str = "EUR") -> list[dict]:
    """W3-F19: the active staff-authored 'what a gift makes possible' cost anchors for the donate
    page. PURELY ILLUSTRATIVE — plain dicts of (label + amount + decorative category), ordered
    largest-first and capped. There is deliberately NO live ratio against the SpendEntry actuals
    and NO 'X of Y' goal/progress framing: ``spend_category`` is a non-binding label, never joined
    to a computed total (the F29/F34/W2-F26 anti-vanity rule)."""
    limit = getattr(settings, "COST_ANCHORS_MAX", 6)
    return list(
        CostAnchor.objects.filter(is_active=True, currency=currency)
        .order_by("-amount_cents", "id")
        .values("label", "amount_cents", "currency", "spend_category")[:limit]
    )


@transaction.atomic
def complete_donation(external_ref: str) -> Donation | None:
    """Reconcile a completed payment (called from a provider webhook)."""
    donation = (
        Donation.objects.select_for_update()
        .filter(external_ref=external_ref, status=Donation.Status.PENDING)
        .first()
    )
    if donation is None:
        return None
    donation.mark_completed()
    return donation
