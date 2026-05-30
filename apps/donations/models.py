from django.conf import settings
from django.db import models
from django.utils import timezone


class Donation(models.Model):
    """A donation. The product is donation-funded — no ads, no tracking-based
    monetization (docs/SAFETY.md, DSA Art. 28). No card/payment data is ever stored:
    the payment is handled entirely by the external provider; we keep only an opaque
    reference and status."""

    class Status(models.TextChoices):
        PENDING = "pending", "Pending"
        COMPLETED = "completed", "Completed"
        FAILED = "failed", "Failed"
        REFUNDED = "refunded", "Refunded"

    # Anonymous donations are allowed; donor is optional.
    donor = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="donations",
    )
    amount_cents = models.PositiveIntegerField()
    currency = models.CharField(max_length=3, default="EUR")
    recurring = models.BooleanField(default=False)
    # F34: optional earmark to a campaign. SET_NULL so deleting a campaign never destroys the
    # financial record — the gift falls back to the general fund (campaign=NULL).
    campaign = models.ForeignKey(
        "donations.Campaign",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="donations",
    )
    provider = models.CharField(max_length=64)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    external_ref = models.CharField(max_length=128, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    completed_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        indexes = [
            models.Index(fields=["status", "created_at"]),
            models.Index(fields=["external_ref"]),
            models.Index(fields=["campaign", "status"]),
        ]
        constraints = [
            models.CheckConstraint(
                condition=models.Q(amount_cents__gte=100), name="donation_min_amount"
            ),
        ]

    def __str__(self):
        return f"donation({self.amount_cents} {self.currency}, {self.status})"

    def mark_completed(self) -> None:
        self.status = self.Status.COMPLETED
        self.completed_at = timezone.now()
        self.save(update_fields=["status", "completed_at"])


class SpendEntry(models.Model):
    """A staff-entered line in the public 'where the money goes' ledger (F29). Aggregate-only:
    there is intentionally NO link to any donor or donation, so the public page can never leak
    per-donor data. Money is integer cents."""

    category = models.CharField(max_length=120)
    amount_cents = models.PositiveIntegerField()
    currency = models.CharField(max_length=3, default="EUR")
    period = models.CharField(max_length=60, blank=True)  # free-text label, e.g. "2026 Q1"
    note = models.CharField(max_length=300, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        ordering = ["-created_at"]
        constraints = [
            models.CheckConstraint(
                condition=models.Q(amount_cents__gte=0), name="spendentry_nonneg_amount"
            ),
        ]

    def __str__(self):
        return f"spend({self.category}: {self.amount_cents} {self.currency})"


class Campaign(models.Model):
    """A staff-curated, mission-tied funding campaign a donor can earmark a gift toward (F34).
    Text-only by design: NO end_date (would invite a countdown), NO logo/image (an ad surface),
    NO website. Progress is shown as a calm static bar, aggregate-only."""

    title = models.CharField(max_length=120)
    slug = models.SlugField(max_length=64, unique=True)
    description = models.TextField(blank=True)
    goal_cents = models.PositiveIntegerField()
    currency = models.CharField(max_length=3, default="EUR")
    is_active = models.BooleanField(default=True)
    created = models.DateTimeField(auto_now_add=True)

    class Meta:
        ordering = ["title"]
        indexes = [models.Index(fields=["is_active"])]
        constraints = [
            models.CheckConstraint(
                condition=models.Q(goal_cents__gte=100), name="campaign_goal_min"
            ),
        ]

    def __str__(self):
        return self.title
