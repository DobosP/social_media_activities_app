from django.conf import settings
from django.core.exceptions import ValidationError
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
    # W2-F26: optional earmark to the campaign this spend delivered on (a verbatim copy of the
    # Donation.campaign pattern). SET_NULL so deleting a campaign never destroys the spend record;
    # an UNTAGGED row still tallies globally in spend_by_category but won't show under a close-out.
    campaign = models.ForeignKey(
        "donations.Campaign",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="spend_entries",
    )
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
    NO website. Progress is shown as a calm static bar, aggregate-only.

    F42: may optionally name a verified civic Partner it supports, surfaced as a one-line text
    credit beside the calm bar on /campaigns/. The partner is gated to Partner.objects.public()
    at write time (admin formfield + clean) AND at read time (active_campaigns_with_progress),
    so a partner deactivated/unverified after being named simply stops being credited — never an
    ad/boost surface, no donor data involved (a Partner has no user)."""

    title = models.CharField(max_length=120)
    slug = models.SlugField(max_length=64, unique=True)
    description = models.TextField(blank=True)
    goal_cents = models.PositiveIntegerField()
    currency = models.CharField(max_length=3, default="EUR")
    is_active = models.BooleanField(default=True)
    # W2-F26: the honest close-out loop. When staff close a campaign they publish a calm one-line
    # plain-text outcome ("what your gift funded") + a closed_at; both set ONLY in CampaignAdmin.
    # A campaign appears in the public close-out section ONLY with BOTH set (never a false
    # "delivered" claim). Capped + autoescaped/|linebreaks at render — no scarcity/goal framing.
    outcome = models.CharField(max_length=280, blank=True, default="")
    closed_at = models.DateTimeField(null=True, blank=True)
    # SET_NULL (like Donation.campaign): deleting a partner must never destroy a campaign or its
    # donation history — the credit just disappears and the campaign stays general-fund-safe.
    partner = models.ForeignKey(
        "places.Partner",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="campaigns",
        help_text="Optional verified civic partner this campaign credits on /campaigns/.",
    )
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

    def clean(self):
        # Defence-in-depth: a campaign can only NAME a verified+active partner (the same
        # public() chokepoint that gates every other partner read). The admin formfield already
        # limits the choices; this catches any full_clean() path (forms, scripts using it).
        super().clean()
        if self.partner_id is not None:
            from apps.places.models import Partner

            if not Partner.objects.public().filter(pk=self.partner_id).exists():
                raise ValidationError(
                    {"partner": "Only a verified, active partner can be credited on a campaign."}
                )
