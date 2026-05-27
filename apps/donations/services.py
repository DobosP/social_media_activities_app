from django.db import transaction

from .models import Donation
from .providers import get_payment_provider, new_reference


class DonationError(Exception):
    """Expected donation-domain error."""


@transaction.atomic
def start_donation(donor, amount_cents: int, currency: str = "EUR", *, recurring: bool = False):
    """Create a pending donation and a provider checkout intent. Returns (donation, url)."""
    if amount_cents < 100:
        raise DonationError("Minimum donation is 1.00.")
    provider = get_payment_provider()
    reference = new_reference()
    intent = provider.create_intent(amount_cents, currency, reference=reference)
    donation = Donation.objects.create(
        donor=donor if (donor and donor.is_authenticated) else None,
        amount_cents=amount_cents,
        currency=currency,
        recurring=recurring,
        provider=provider.name,
        external_ref=intent.external_ref,
    )
    return donation, intent.checkout_url


def completed_total_cents(currency: str = "EUR") -> int:
    """Aggregate of completed donations — public transparency figure, no PII."""
    from django.db.models import Sum

    total = Donation.objects.filter(status=Donation.Status.COMPLETED, currency=currency).aggregate(
        s=Sum("amount_cents")
    )["s"]
    return total or 0


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
