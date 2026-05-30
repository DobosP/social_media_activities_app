from django.conf import settings
from django.contrib.gis.db import models as gis_models
from django.core.validators import MaxLengthValidator
from django.db import models
from django.db.models import Q, UniqueConstraint

from apps.safety.sanitize import safe_external_url
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

    def __str__(self):
        return self.name or f"{self.source}:{self.osm_id or self.external_id}"

    def save(self, *args, **kwargs):
        # Centralized stored-XSS guard. `website` arrives from untrusted sources (OSM,
        # Overture, Google/Wikidata enrichment) and is served RAW over the JSON API, so
        # only a safe http(s) URL is ever persisted — at the single write chokepoint.
        self.website = safe_external_url(self.website)
        super().save(*args, **kwargs)

    @property
    def is_bookable(self) -> bool:
        """A place with a website is a reservation candidate (deep-link booking)."""
        return bool(self.website)


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


class AccessPreference(models.Model):
    """A user's OWN stated accessibility needs (F15) — a setting they choose, never inferred
    from behaviour and never used for tracking. One row per user. Drives only a SOFT badge
    against the read-time accessibility facts derived from a venue's OSM tags; it never hides a
    place whose accessibility is unknown."""

    user = models.OneToOneField(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="access_preference"
    )
    needs_step_free = models.BooleanField(default=False)
    needs_accessible_toilet = models.BooleanField(default=False)
    # Stored + shown as a forward-looking preference; no OSM tag satisfies it yet, so it drives
    # no badge/sort in v1 (documented to the user).
    prefers_quiet = models.BooleanField(default=False)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    def __str__(self):
        return f"{self.user} access-pref"


class PartnerManager(models.Manager):
    def public(self):
        """The single public-visibility chokepoint: verified AND active only."""
        return self.filter(is_verified=True, is_active=True)


class Partner(models.Model):
    """A vetted civic institution we acknowledge as a partner (F37) — a library/school/NGO/civic
    body that stewards a real venue. TEXT-ONLY by construction: NO logo/image field (never an
    ad/banner surface), NO ranking/boost/featured field. Only verified+active partners are ever
    public, via Partner.objects.public(). The optional website is sanitised like Place.website."""

    class Kind(models.TextChoices):
        LIBRARY = "library", "Library"
        SCHOOL = "school", "School"
        NGO = "ngo", "NGO / nonprofit"
        CIVIC = "civic", "Civic body"
        HEALTHCARE = "healthcare", "Healthcare"
        CULTURAL = "cultural", "Cultural"
        OTHER = "other", "Other"

    name = models.CharField(max_length=255)
    kind = models.CharField(max_length=16, choices=Kind.choices)
    # Short credit only — a hard cap keeps it an acknowledgement, never a promotional paragraph.
    blurb = models.TextField(blank=True, validators=[MaxLengthValidator(280)])
    place = models.ForeignKey(
        "places.Place", on_delete=models.SET_NULL, null=True, blank=True, related_name="partners"
    )
    website = models.URLField(max_length=500, blank=True)
    is_verified = models.BooleanField(default=False)
    is_active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    objects = PartnerManager()

    class Meta:
        ordering = ["name"]  # neutral alphabetical — no rank/amount/boost field exists
        indexes = [models.Index(fields=["is_verified", "is_active"])]

    def __str__(self):
        return f"{self.name} ({self.get_kind_display()})"

    def save(self, *args, **kwargs):
        # Same trust boundary as Place.website: only a safe http(s) URL is ever stored.
        self.website = safe_external_url(self.website)
        super().save(*args, **kwargs)
