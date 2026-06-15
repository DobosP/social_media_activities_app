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

    # --- F20: read-time crowd corrections (never written back to Place) -----------------

    def _raw_address(self) -> str:
        street = f"{self.address_street} {self.address_housenumber}".strip()
        return ", ".join(p for p in (street, self.address_city) if p)

    def _applied_correction(self, field) -> str:
        """The most-recent PUBLISHED correction value for `field`, or '' if none. Iterates
        ``self.corrections.all()`` so a ``prefetch_related('corrections')`` yields ZERO extra
        queries on list surfaces (a bare access on one place costs a single query)."""
        published = [
            c
            for c in self.corrections.all()
            if c.field == field and c.status == PlaceCorrection.Status.PUBLISHED
        ]
        if not published:
            return ""
        return max(published, key=lambda c: (c.published_at or c.created_at, c.id)).proposed_value

    @property
    def display_name(self) -> str:
        """The crowd-corrected name if a correction was published, else the raw (OSM) name. The
        single seam every template/serializer renders so a correction reaches them all (F20)."""
        return self._applied_correction(PlaceCorrection.Field.NAME) or self.name

    @property
    def display_address(self) -> str:
        return self._applied_correction(PlaceCorrection.Field.ADDRESS) or self._raw_address()


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
    # F26: crowd-dispute hide flag. INGEST-SAFE: ingest_places._upsert_edge's fixed `defaults`
    # dict never sets is_disputed, so update_or_create leaves it untouched on re-ingest. Owned
    # solely by the crowd-vote path (apps/places/edges.py).
    is_disputed = models.BooleanField(default=False)
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
    # F32: backed by the honest OSM hearing_loop fact, so it drives the soft badge + needs-aware
    # sort exactly like the other needs.
    needs_hearing_loop = models.BooleanField(default=False)
    # Stored + shown as a forward-looking preference; no OSM tag satisfies it yet, so it drives
    # no badge/sort (documented to the user). Deliberately NOT wired into the sort (F32).
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


# F26: independent confirmations to promote an INFERRED edge to CONFIRMED (ingest-protected),
# and independent disputes to hide an inferred edge from discovery.
DEFAULT_EDGE_QUORUM = 3


class ActivityEdgeVote(models.Model):
    """A user's confirm/dispute of a place<->activity edge (F26). The COUNT SOURCE — ingest
    never touches this table, so the tally survives re-ingest by construction. One vote per
    (edge, user); a mind-change updates the single row (no brigading)."""

    class Vote(models.TextChoices):
        CONFIRM = "confirm", "Confirms"
        DISPUTE = "dispute", "Disputes"

    edge = models.ForeignKey(PlaceActivity, on_delete=models.CASCADE, related_name="edge_votes")
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="activity_edge_votes"
    )
    vote = models.CharField(max_length=8, choices=Vote.choices)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["edge", "user"], name="uq_edge_vote_user"),
        ]
        indexes = [models.Index(fields=["edge", "vote"])]

    def __str__(self):
        return f"{self.vote}({self.edge_id} by {self.user_id})"


class OpenNowReport(models.Model):
    """A member's 'actually closed when it said open' report (F28). DEDICATED, ingest-safe
    overlay: NEVER stored on Place, because ingest_places._resolve_place overwrites
    opening_hours/opening_hours_raw/raw_tags on every re-ingest. 'Hours unreliable' is derived
    at READ time from a count of recent reports (auto-decay), so a re-ingest can't clobber it."""

    place = models.ForeignKey(Place, on_delete=models.CASCADE, related_name="open_now_reports")
    reporter = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="open_now_reports"
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        # No UniqueConstraint: uniqueness is TEMPORAL (one per reporter per place per decay
        # window), enforced in the service so a post-decay report is allowed again.
        indexes = [
            models.Index(fields=["place", "created_at"]),  # read-time recent-count query
            models.Index(fields=["place", "reporter"]),  # cheap per-window dedup .exists()
        ]

    def __str__(self):
        return f"open_now_report({self.place_id} by {self.reporter_id})"


DEFAULT_FACT_QUORUM = 3


class PlaceFactVote(models.Model):
    """F19: a verified member's yes/no vote on a concrete VENUE fact OSM rarely records (drinking
    water, lit at night, toilets, shade, fenced, indoor shelter, playground). The COUNT SOURCE —
    ingest never touches this table, so the tally survives re-ingest. One vote per
    (place, user, fact_key); a mind-change updates the single row (no brigading). fact_key is a
    FIXED closed allowlist and value is a bare boolean — no free text, so it can't be a covert
    channel. The per-fact state is derived OSM-FIRST at read time (see services.place_fact_status);
    these votes only fill in where OSM is silent. Describes the physical venue, never user presence
    — co-voting on a place is NOT a shared activity and never enables can_connect."""

    class FactKey(models.TextChoices):
        DRINKING_WATER = "drinking_water", "Drinking water"
        TOILETS = "toilets", "Toilets"
        LIT_AT_NIGHT = "lit_at_night", "Lit at night"
        INDOOR_SHELTER = "indoor_shelter", "Indoor shelter"
        FENCED = "fenced", "Fenced / away from traffic"
        SHADE = "shade", "Shade"
        PLAYGROUND = "playground", "Playground nearby"
        # W2-F22 "getting there" facts — so minors/non-drivers can judge a venue beyond an address
        # pin. Parking rides the OSM-first yes/no path on the venue's own parking sub-tag; transit
        # has no venue-level tag and is crowd-only. (See services._FACT_OSM.)
        BIKE_PARKING = "bike_parking", "Bike parking"
        CAR_PARKING = "car_parking", "Car parking"
        BUS_TRAM_NEARBY = "bus_tram_nearby", "Bus/tram nearby"

    place = models.ForeignKey(Place, on_delete=models.CASCADE, related_name="fact_votes")
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="place_fact_votes"
    )
    fact_key = models.CharField(max_length=24, choices=FactKey.choices)
    value = models.BooleanField()  # True = yes (present), False = no (absent)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["place", "user", "fact_key"], name="uq_place_fact_user"),
        ]
        indexes = [models.Index(fields=["place", "fact_key"])]

    def __str__(self):
        return f"fact_vote({self.place_id}.{self.fact_key}={self.value} by {self.user_id})"


DEFAULT_CORRECTION_QUORUM = 3


class PlaceCorrection(models.Model):
    """F20: a member-proposed correction to a venue's name or address, behind the SAME N-confirmer
    quorum as a user-proposed place (F25). Applied at READ time via Place.display_name /
    display_address — NEVER written back to Place, so it can't poison canonical OSM and survives
    re-ingest (the gap a staff-admin edit, clobbered on re-ingest, can't fill). Counts-only pending
    UI: no proposer/confirmer identities. Place metadata is cohort-agnostic — no contact path."""

    class Field(models.TextChoices):
        NAME = "name", "Name"
        ADDRESS = "address", "Address"

    class Status(models.TextChoices):
        PENDING = "pending", "Pending"
        PUBLISHED = "published", "Published"
        REJECTED = "rejected", "Rejected"

    place = models.ForeignKey(Place, on_delete=models.CASCADE, related_name="corrections")
    proposer = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="place_corrections"
    )
    field = models.CharField(max_length=16, choices=Field.choices)
    proposed_value = models.CharField(max_length=255)
    required_confirmations = models.PositiveIntegerField(default=DEFAULT_CORRECTION_QUORUM)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    created_at = models.DateTimeField(auto_now_add=True)
    published_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            models.CheckConstraint(
                condition=Q(required_confirmations__gte=1), name="correction_quorum_positive"
            ),
            # At most ONE open correction per (place, field) — no duplicate pending proposals.
            UniqueConstraint(
                fields=["place", "field"],
                condition=Q(status="pending"),
                name="uq_one_pending_correction_per_field",
            ),
        ]
        indexes = [models.Index(fields=["place", "status"])]

    def __str__(self):
        return f"correction({self.place_id}.{self.field}, {self.status})"


class PlaceCorrectionConfirmation(models.Model):
    """An independent member's confirmation of a proposed correction (proposer excluded)."""

    correction = models.ForeignKey(
        PlaceCorrection, on_delete=models.CASCADE, related_name="confirmations"
    )
    user = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.CASCADE,
        related_name="place_correction_confirmations",
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            UniqueConstraint(fields=["correction", "user"], name="uq_correction_confirm_user"),
        ]

    def __str__(self):
        return f"confirm_correction({self.correction_id} by {self.user_id})"


class ChildVenueClass(models.Model):
    """F9: a STAFF-curated allowlist of venue CLASSES considered safe-enough public places for a
    CHILD-cohort meetup (library, park, sports centre, school, community centre, ...). This is a
    "known public venue type" signal — NOT a claim of staffed supervision. Matching is per source:
    OSM tags (``osm_match`` — every key/value must equal the place's raw_tags) and Overture
    categories (``overture_categories`` — any overlap). Read at activity-create and join time via
    ``places.public_child_venue_class``; NEVER written onto Place (so re-ingest can't clobber it).
    Staff edit the set in Django admin without a deploy."""

    key = models.SlugField(max_length=64, unique=True)
    label = models.CharField(max_length=120)
    osm_match = models.JSONField(default=dict, blank=True)
    overture_categories = models.JSONField(default=list, blank=True)
    is_active = models.BooleanField(default=True)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        verbose_name = "child venue class"
        verbose_name_plural = "child venue classes"

    def __str__(self):
        return self.key


class ApprovedChildVenue(models.Model):
    """F9 escape hatch: a per-PLACE staff approval that a specific venue is a safe-enough public
    place for children, even when its tags don't match any ChildVenueClass (e.g. a legitimate
    library that OSM mis-tagged, or a non-OSM source). An ingest-safe overlay in its OWN table
    (never a Place field), so re-ingest can't clobber it. Managed by staff in Django admin — the
    'staff-approval path' that keeps fail-closed from silently over-blocking real venues."""

    place = models.OneToOneField(Place, on_delete=models.CASCADE, related_name="child_approval")
    approved_by = models.ForeignKey(
        settings.AUTH_USER_MODEL,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="child_venue_approvals",
    )
    note = models.CharField(max_length=255, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)

    def __str__(self):
        return f"child_approved({self.place_id})"
