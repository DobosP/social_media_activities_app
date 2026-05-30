"""Communities = derived, materialized DISCOVERY LABELS over the existing cohort-filtered
activity feed (e.g. "Cluj-Napoca Football"), NOT rooms/rosters/feeds/chat. The "graph" is two
FK chains that already exist — the GEO axis (Place.address_city -> Area; finer PostGIS areas
later) and the TAXONOMY axis (ActivityType -> category). A Community pins one coordinate on each
axis, PER COHORT, so existence and content are cohort-walled. Membership is NEVER stored or
shown. Ingest-safe overlay tables (ingest_places never touches them), like F26/F28."""

from django.db import models

from apps.accounts.models import Cohort


class Area(models.Model):
    """A geographic scope for communities, derived from PLACE geometry only (never from a user
    position). v1 is CITY-tier (one Area == one city, free from Place.address_city); finer
    neighbourhood Areas (GRID/CLUSTER via PostGIS) come later, never finer than ``min_radius_m``."""

    class DeriveMethod(models.TextChoices):
        CITY = "city", "Whole city"
        GRID = "grid", "Snapped grid cell"
        CLUSTER = "cluster", "Venue cluster"
        POLYGON = "polygon", "Admin polygon"

    city = models.CharField(max_length=128, db_index=True)
    slug = models.SlugField(max_length=96, unique=True)
    name = models.CharField(max_length=128)
    derive_method = models.CharField(
        max_length=12, choices=DeriveMethod.choices, default=DeriveMethod.CITY
    )
    # The ENFORCED coarseness floor (child-safety): a derive run may never emit a cell finer than
    # this, so a fine bucket can't pinpoint a minor. CITY ignores it.
    min_radius_m = models.PositiveIntegerField(default=1500)
    is_active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [models.UniqueConstraint(fields=["city", "slug"], name="uq_area_city_slug")]

    def __str__(self):
        return f"area({self.name})"


class Community(models.Model):
    """One row per (cohort, Area, taxonomy coordinate). TYPE-tier = "Cluj-Napoca Football"
    (activity_type set); CATEGORY-tier = "Cluj-Napoca Sport" (activity_type null, the rollup).
    Materialized per cohort by the nightly generate_communities job ONLY above a threshold +
    k-anonymity floor, so a CHILD never sees the EXISTENCE of a community built off adult
    activity. NO member table, NO stored count, NO post/thread/contact relation — membership is
    deliberately unanswerable so it can never become a vanity metric or a grooming surface."""

    class Tier(models.TextChoices):
        TYPE = "type", "Activity type"
        CATEGORY = "category", "Category"

    cohort = models.CharField(max_length=16, choices=Cohort.choices)
    area = models.ForeignKey(Area, on_delete=models.PROTECT, related_name="communities")
    # The rollup category (always set, = the type's category or the category itself).
    category = models.ForeignKey(
        "taxonomy.ActivityCategory", on_delete=models.PROTECT, related_name="communities"
    )
    # Set ONLY for a TYPE-tier community; NULL marks the CATEGORY-tier rollup.
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType",
        on_delete=models.PROTECT,
        null=True,
        blank=True,
        related_name="communities",
    )
    tier = models.CharField(max_length=8, choices=Tier.choices)
    slug = models.SlugField(max_length=140, unique=True)
    # Denormalized human label, composed ONCE by the generator (no user-supplied names).
    name = models.CharField(max_length=160)
    # The threshold+k-floor gate. Deactivate-not-delete so slugs/audit survive a dry spell.
    is_published = models.BooleanField(default=False)
    last_evaluated_at = models.DateTimeField(null=True, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            # One TYPE-tier community per (cohort, area, type).
            models.UniqueConstraint(
                fields=["cohort", "area", "activity_type"],
                condition=models.Q(activity_type__isnull=False),
                name="uq_community_type",
            ),
            # One CATEGORY-tier rollup per (cohort, area, category).
            models.UniqueConstraint(
                fields=["cohort", "area", "category"],
                condition=models.Q(activity_type__isnull=True),
                name="uq_community_category",
            ),
            # Tier must agree with whether activity_type is set.
            models.CheckConstraint(
                condition=(
                    models.Q(tier="type", activity_type__isnull=False)
                    | models.Q(tier="category", activity_type__isnull=True)
                ),
                name="community_tier_matches_type",
            ),
        ]
        indexes = [
            models.Index(fields=["cohort", "is_published", "area"]),
            models.Index(fields=["cohort", "is_published", "category"]),
        ]

    def __str__(self):
        return f"community({self.name}, {self.cohort})"
