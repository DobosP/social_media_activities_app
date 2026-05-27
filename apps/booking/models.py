from django.conf import settings
from django.db import models

from apps.places.models import Place
from apps.social.models import Activity


class PlaceBookingInfo(models.Model):
    """How to book a given place: which provider, a deep link, and instructions.

    Kept here (not on ``Place``) so the booking deliverable doesn't change the shared
    places schema. ``provider`` is a registry slug; ``deeplink`` is the universal
    fallback that works without any integration.
    """

    place = models.OneToOneField(Place, on_delete=models.CASCADE, related_name="booking_info")
    provider = models.CharField(max_length=32, default="deeplink")
    deep_link = models.URLField(blank=True)
    instructions = models.CharField(max_length=500, blank=True)
    # External venue identifier in the provider's system (for REST providers).
    provider_place_ref = models.CharField(max_length=128, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self):
        return f"booking_info({self.place_id}, {self.provider})"


class Booking(models.Model):
    """A booking a user initiated through the app, optionally tied to an Activity."""

    class Status(models.TextChoices):
        PENDING = "pending", "Pending (complete on provider)"
        CONFIRMED = "confirmed", "Confirmed"
        CANCELLED = "cancelled", "Cancelled"
        FAILED = "failed", "Failed"

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="bookings"
    )
    place = models.ForeignKey(Place, on_delete=models.PROTECT, related_name="bookings")
    activity = models.ForeignKey(
        Activity, on_delete=models.SET_NULL, null=True, blank=True, related_name="bookings"
    )
    provider = models.CharField(max_length=32)
    external_ref = models.CharField(max_length=128, blank=True)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    starts_at = models.DateTimeField()
    ends_at = models.DateTimeField(null=True, blank=True)
    party_size = models.PositiveIntegerField(default=1)
    deep_link = models.URLField(blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [
            models.Index(fields=["user", "status"]),
            models.Index(fields=["place"]),
        ]
        ordering = ["-starts_at"]

    def __str__(self):
        return f"booking({self.user_id}, {self.place_id}, {self.status})"
