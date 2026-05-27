from django.db import models
from django.db.models import Q, UniqueConstraint


class Event(models.Model):
    """A happening at a place — pulled from venue calendars (iCal feeds), Google, or
    user/manual entry. Associating events with collected places answers "is something
    happening there" (ROADMAP D7), complementing the static place data."""

    class Source(models.TextChoices):
        ICAL = "ical", "iCalendar feed"
        GOOGLE = "google", "Google"
        USER = "user", "User-submitted"
        MANUAL = "manual", "Manual"

    place = models.ForeignKey(
        "places.Place",
        on_delete=models.CASCADE,
        null=True,
        blank=True,
        related_name="events",
    )
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType",
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="events",
    )
    title = models.CharField(max_length=255)
    description = models.TextField(blank=True)
    starts_at = models.DateTimeField()
    ends_at = models.DateTimeField(null=True, blank=True)
    url = models.URLField(max_length=500, blank=True)

    source = models.CharField(max_length=16, choices=Source.choices, default=Source.MANUAL)
    external_id = models.CharField(max_length=200, blank=True)

    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            UniqueConstraint(
                fields=["source", "external_id"],
                condition=~Q(external_id=""),
                name="uq_event_source_external",
            ),
        ]
        indexes = [
            models.Index(fields=["place", "starts_at"]),
            models.Index(fields=["starts_at"]),
        ]
        ordering = ["starts_at"]

    def __str__(self):
        return f"{self.title} @ {self.starts_at:%Y-%m-%d %H:%M}"
