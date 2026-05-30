from django.db import transaction
from django.db.models import Q, Sum

from .models import Campaign, Donation, SpendEntry
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
    list, so no per-donor data can leak. percent uses integer math, capped at 100."""
    qs = (
        Campaign.objects.filter(is_active=True)
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
        rows.append(
            {
                "title": c.title,
                "slug": c.slug,
                "description": c.description,
                "raised_cents": raised,
                "goal_cents": c.goal_cents,
                "currency": c.currency,
                "percent": percent,
            }
        )
    return rows


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
