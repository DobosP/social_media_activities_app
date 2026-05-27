from django.contrib.gis.db import models as gis_models
from django.db import models
from django.db.models import Q, UniqueConstraint

from apps.taxonomy.models import ActivityType


class Place(gis_models.Model):
    """A physical location where activities can happen.

    Sourced from open data (OSM now; Overture/Google later) or user-submitted.
    A geography PointField gives correct metre distances for proximity queries.
    """

    class Source(models.TextChoices):
        OSM = "osm", "OpenStreetMap"
        OVERTURE = "overture", "Overture Maps"
        GOOGLE = "google", "Google"
        USER = "user", "User-submitted"

    name = models.CharField(max_length=255, blank=True)
    location = gis_models.PointField(geography=True, srid=4326)

    address_street = models.CharField(max_length=255, blank=True)
    address_housenumber = models.CharField(max_length=32, blank=True)
    address_city = models.CharField(max_length=128, blank=True)
    address_postcode = models.CharField(max_length=32, blank=True)
    address_country = models.CharField(max_length=2, blank=True)

    opening_hours_raw = models.CharField(max_length=255, blank=True)
    opening_hours = models.JSONField(null=True, blank=True)

    # Contact details — `website` is the entry point for reservations/bookings (D8).
    website = models.URLField(max_length=500, blank=True)
    phone = models.CharField(max_length=64, blank=True)

    source = models.CharField(max_length=16, choices=Source.choices)
    osm_type = models.CharField(max_length=8, blank=True)  # node | way | relation
    osm_id = models.BigIntegerField(null=True, blank=True)
    external_id = models.CharField(max_length=128, blank=True)
    raw_tags = models.JSONField(default=dict, blank=True)

    # FUTURE: created_by FK to accounts.User (null=True) for user-submitted places.
    first_seen_at = models.DateTimeField(auto_now_add=True)
    last_seen_at = models.DateTimeField(auto_now=True)

    activities = models.ManyToManyField(
        ActivityType, through="PlaceActivity", related_name="places"
    )

    class Meta:
        constraints = [
            UniqueConstraint(
                fields=["osm_type", "osm_id"],
                condition=Q(source="osm"),
                name="uq_place_osm",
            ),
            UniqueConstraint(
                fields=["source", "external_id"],
                condition=~Q(external_id=""),
                name="uq_place_external",
            ),
        ]
        indexes = [
            models.Index(fields=["address_city"]),
            models.Index(fields=["source"]),
        ]

    @property
    def is_bookable(self) -> bool:
        """A place with a website is a reservation candidate (deep-link booking)."""
        return bool(self.website)

    def __str__(self):
        return self.name or f"{self.source}:{self.osm_id or self.external_id}"


class PlaceActivity(models.Model):
    """Edge connecting a Place to an ActivityType it supports."""

    class Origin(models.TextChoices):
        INFERRED = "inferred", "Inferred from tags"
        CONFIRMED = "confirmed", "User-confirmed"
        MANUAL = "manual", "Manually added"

    place = models.ForeignKey(Place, on_delete=models.CASCADE, related_name="place_activities")
    activity = models.ForeignKey(
        ActivityType, on_delete=models.PROTECT, related_name="place_activities"
    )
    origin = models.CharField(max_length=16, choices=Origin.choices, default=Origin.INFERRED)
    confidence = models.FloatField(default=0.5)
    source = models.CharField(max_length=16, default="osm")
    mapping_rule = models.CharField(max_length=128, blank=True)
    # FUTURE: confirmed_by FK to accounts.User (null=True) for user confirmations.
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["place", "activity"], name="uq_place_activity"),
        ]
        indexes = [models.Index(fields=["activity"])]

    def __str__(self):
        return f"{self.place_id}<->{self.activity.slug} ({self.confidence})"
