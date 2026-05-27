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
    provider = models.CharField(max_length=64)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    external_ref = models.CharField(max_length=128, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    completed_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        indexes = [
            models.Index(fields=["status", "created_at"]),
            models.Index(fields=["external_ref"]),
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
