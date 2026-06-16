import uuid

from django.contrib.auth.models import AbstractBaseUser, BaseUserManager, PermissionsMixin
from django.contrib.postgres.fields import ArrayField
from django.core.validators import MaxValueValidator, MinValueValidator
from django.db import models
from django.utils import timezone


class AgeBand(models.TextChoices):
    UNKNOWN = "unknown", "Unknown"
    UNDER_16 = "under_16", "Under 16"
    AGE_16_17 = "16_17", "16-17"
    ADULT = "adult", "Adult (18+)"


class Cohort(models.TextChoices):
    UNASSIGNED = "unassigned", "Unassigned"
    CHILD = "child", "Child (under 16)"
    TEEN = "teen", "Teen (16-17)"
    ADULT = "adult", "Adult (18+)"


class Role(models.TextChoices):
    """Platform access level. A *guardian* is not a role — it's any USER who has an
    active GuardianRelationship to a minor (a parent/legal guardian)."""

    USER = "user", "User"
    MODERATOR = "moderator", "Moderator"
    ADMIN = "admin", "Admin"


# Romania's digital age of majority is 16, so under-16 is the consent-gated cohort.
COHORT_BY_AGE_BAND = {
    AgeBand.UNDER_16: Cohort.CHILD,
    AgeBand.AGE_16_17: Cohort.TEEN,
    AgeBand.ADULT: Cohort.ADULT,
    AgeBand.UNKNOWN: Cohort.UNASSIGNED,
}


class UserManager(BaseUserManager):
    use_in_migrations = True

    def create_user(self, username, password=None, **extra):
        if not username:
            raise ValueError("Users require a username (the unique identifier).")
        user = self.model(username=username, **extra)
        user.set_password(password)
        user.save(using=self._db)
        return user

    def create_superuser(self, username, password=None, **extra):
        extra.setdefault("is_staff", True)
        extra.setdefault("is_superuser", True)
        extra.setdefault("age_band", AgeBand.ADULT)
        extra.setdefault("role", Role.ADMIN)
        if extra.get("is_staff") is not True:
            raise ValueError("Superuser must have is_staff=True.")
        if extra.get("is_superuser") is not True:
            raise ValueError("Superuser must have is_superuser=True.")
        return self.create_user(username, password, **extra)


class User(AbstractBaseUser, PermissionsMixin):
    """Custom user. `username` is the unique login identifier — for minors this is
    the parent-authorized identifier, not personal data like email.

    Identity data is deliberately minimized: we store an AGE BAND, never a birthdate.
    """

    public_id = models.UUIDField(default=uuid.uuid4, editable=False, unique=True)
    username = models.CharField(max_length=150, unique=True)
    display_name = models.CharField(max_length=120, blank=True)

    age_band = models.CharField(max_length=16, choices=AgeBand.choices, default=AgeBand.UNKNOWN)
    cohort = models.CharField(max_length=16, choices=Cohort.choices, default=Cohort.UNASSIGNED)
    is_identity_verified = models.BooleanField(default=False)
    identity_verified_at = models.DateTimeField(null=True, blank=True)

    role = models.CharField(max_length=16, choices=Role.choices, default=Role.USER)

    is_active = models.BooleanField(default=True)
    is_staff = models.BooleanField(default=False)
    date_joined = models.DateTimeField(default=timezone.now)

    objects = UserManager()

    USERNAME_FIELD = "username"
    REQUIRED_FIELDS: list[str] = []

    class Meta:
        # The nightly F6 reverify_sweep filters minors by (cohort, is_identity_verified); an index
        # keeps that candidate scan from degrading to a seq scan as the users table grows.
        indexes = [models.Index(fields=["cohort", "is_identity_verified"])]

    def __str__(self):
        return self.display_name or self.username

    @property
    def requires_parental_consent(self) -> bool:
        return self.age_band == AgeBand.UNDER_16

    @property
    def is_admin(self) -> bool:
        return self.role == Role.ADMIN or self.is_superuser

    @property
    def is_moderator(self) -> bool:
        # Admins are moderators too.
        return self.role in (Role.MODERATOR, Role.ADMIN) or self.is_staff

    @property
    def is_guardian(self) -> bool:
        """True if this account is a parent/legal guardian of at least one minor."""
        return self.wards.filter(status=GuardianRelationship.Status.ACTIVE).exists()

    def recompute_cohort(self) -> None:
        self.cohort = COHORT_BY_AGE_BAND.get(self.age_band, Cohort.UNASSIGNED)


class AgeAssurance(models.Model):
    """Record of an age-assurance event from an identity provider (e.g. EUDI wallet /
    EU age-verification). Stores the proven band, not identity data."""

    class ReverifyNotice(models.TextChoices):
        # F6 sent-marker: which re-verify notice the nightly reverify_sweep has already sent for
        # THIS proof, so a tick never re-notifies. A fresh proof (re-verification creates a new
        # row) starts at NONE, giving each proof its own expiring-soon nudge + expiry notice.
        NONE = "", "None"
        SOON = "soon", "Expiring-soon nudge sent"
        EXPIRED = "expired", "Expired / paused notice sent"

    user = models.ForeignKey(User, on_delete=models.CASCADE, related_name="age_assurances")
    provider = models.CharField(max_length=64)
    method = models.CharField(max_length=64, blank=True)
    age_band = models.CharField(max_length=16, choices=AgeBand.choices)
    verified_at = models.DateTimeField(default=timezone.now)
    expires_at = models.DateTimeField(null=True, blank=True)
    raw = models.JSONField(default=dict, blank=True)
    reverify_notice = models.CharField(
        max_length=8, choices=ReverifyNotice.choices, default=ReverifyNotice.NONE, blank=True
    )

    class Meta:
        indexes = [models.Index(fields=["user", "verified_at"])]

    def __str__(self):
        return f"{self.user} {self.age_band} via {self.provider}"


class ParentalConsent(models.Model):
    """Verifiable parental consent for an under-16 user (Romania Law 190/2018 /
    Online Age of Majority law). `guardian_identifier` is a verified reference, not
    free-form personal data."""

    class Status(models.TextChoices):
        PENDING = "pending", "Pending"
        ACTIVE = "active", "Active"
        REVOKED = "revoked", "Revoked"
        EXPIRED = "expired", "Expired"

    minor = models.ForeignKey(User, on_delete=models.CASCADE, related_name="parental_consents")
    guardian_identifier = models.CharField(max_length=255)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    scope = models.CharField(max_length=255, blank=True)
    granted_at = models.DateTimeField(null=True, blank=True)
    expires_at = models.DateTimeField(null=True, blank=True)
    revoked_at = models.DateTimeField(null=True, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        indexes = [models.Index(fields=["minor", "status"])]

    def __str__(self):
        return f"consent({self.minor}, {self.status})"

    def is_valid(self) -> bool:
        if self.status != self.Status.ACTIVE:
            return False
        if self.expires_at and self.expires_at <= timezone.now():
            return False
        return True


class ConsumedAgeNonce(models.Model):
    """Single-use ledger of OpenID4VP age-verification nonces (W2-9). Claiming a nonce
    here before applying an assurance prevents a captured wallet presentation from being
    replayed: the unique constraint makes a second claim of the same nonce fail."""

    nonce = models.CharField(max_length=255, unique=True)
    created_at = models.DateTimeField(auto_now_add=True)

    def __str__(self):
        return f"consumed-nonce({self.nonce})"


class GuardianRelationship(models.Model):
    """An account-level legal-guardianship link: an adult `guardian` is the parent/
    protector of a minor `ward`. Established alongside parental consent; lets the
    guardian accompany/act for their child within safety rules (see docs/SAFETY.md)."""

    class Status(models.TextChoices):
        ACTIVE = "active", "Active"
        REVOKED = "revoked", "Revoked"

    guardian = models.ForeignKey(User, on_delete=models.CASCADE, related_name="wards")
    ward = models.ForeignKey(User, on_delete=models.CASCADE, related_name="guardians")
    relationship = models.CharField(max_length=32, blank=True)  # e.g. parent, legal_guardian
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.ACTIVE)
    consent = models.ForeignKey(
        ParentalConsent,
        on_delete=models.SET_NULL,
        null=True,
        blank=True,
        related_name="guardian_links",
    )
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(fields=["guardian", "ward"], name="uq_guardian_ward"),
            models.CheckConstraint(
                condition=~models.Q(guardian=models.F("ward")), name="guardian_not_self"
            ),
        ]
        indexes = [models.Index(fields=["ward", "status"])]

    def __str__(self):
        return f"{self.guardian} guards {self.ward} ({self.status})"


class GuardianGuardrail(models.Model):
    """F7: a guardian's conservative, child-read-only limits on a CHILD ward's meetup
    participation. It only ever NARROWS access — every field maps to an honest fact the
    ``can_join`` gate already knows, never to PII or location:

      - ``supervised_only``    -> the meetup must be ``Activity.guardian_accompanied``.
      - ``latest_start_hour``  -> the meetup's *local* start hour must be <= this (0-23).
      - ``max_open_joins``     -> a cap on how many OPEN meetups the ward may be in at once.

    Attached one-to-one to the ``GuardianRelationship`` so a guardrail can never outlive its
    link (CASCADE) and "active" is exactly ``relationship.status == ACTIVE`` — a revoked link's
    guardrail is structurally ignored by enforcement, and a child can have several guardians,
    each with their own guardrail (the gate consults the STRICTEST across all active ones).
    Mutated only by the guardian from ``/wards/`` and audited in-transaction; the child side is
    legibility-only. See docs/SAFETY.md and accounts.services.effective_guardrail / can_join.
    """

    relationship = models.OneToOneField(
        GuardianRelationship, on_delete=models.CASCADE, related_name="guardrail"
    )
    supervised_only = models.BooleanField(default=False)
    latest_start_hour = models.PositiveSmallIntegerField(
        null=True,
        blank=True,
        validators=[MinValueValidator(0), MaxValueValidator(23)],
    )
    max_open_joins = models.PositiveSmallIntegerField(
        null=True,
        blank=True,
        validators=[MinValueValidator(1), MaxValueValidator(50)],
    )
    # W3-F1 "family calendar": narrow a CHILD ward's joinable WINDOW to real family rules.
    #   - allowed_weekdays  -> canonical sorted ISO-day digits (Mon=1..Sun=7); the meetup's *local*
    #     start weekday must be in this set. "" = NO weekday restriction (the common default).
    #   - earliest_start_hour -> the meetup's *local* start hour must be >= this (0-23); the lower
    #     bookend to latest_start_hour. Both NARROW only, join-time only, no PII (activity facts).
    allowed_weekdays = models.CharField(max_length=7, blank=True, default="")
    earliest_start_hour = models.PositiveSmallIntegerField(
        null=True,
        blank=True,
        validators=[MinValueValidator(0), MaxValueValidator(23)],
    )
    # W3-F2 "category envelope": an allowlist of activity-CATEGORY slugs. A CHILD ward may only
    # join/organize/propose an activity whose type's category-ancestry intersects this set; []
    # = NO category restriction (the calm default). Stored as slugs (not an M2M) to mirror the
    # light shape of the weekday/hour guardrails; validated fail-closed (unknown slug raises) in
    # set_guardian_guardrail, and the gate walks the ancestry via taxonomy.category_ancestry_slugs.
    allowed_categories = ArrayField(models.SlugField(max_length=80), default=list, blank=True)
    created_at = models.DateTimeField(auto_now_add=True)
    updated_at = models.DateTimeField(auto_now=True)

    class Meta:
        constraints = [
            models.CheckConstraint(
                condition=models.Q(latest_start_hour__isnull=True)
                | models.Q(latest_start_hour__gte=0, latest_start_hour__lte=23),
                name="guardrail_latest_start_hour_range",
            ),
            models.CheckConstraint(
                condition=models.Q(max_open_joins__isnull=True)
                | models.Q(max_open_joins__gte=1, max_open_joins__lte=50),
                name="guardrail_max_open_joins_range",
            ),
        ]

    def __str__(self):
        return f"guardrail({self.relationship_id})"


class GuardianLinkInvite(models.Model):
    """A pending, mutually-confirmed request to establish a guardianship link.

    A verified adult initiates the invite for a (minor) ward; the ward must explicitly
    accept before the `GuardianRelationship` is created — so neither an adult can
    unilaterally claim a child, nor a child unilaterally attach to a stranger. This is the
    onboarding path that was previously missing (the only `link_guardian` callers were
    tests), which left minors unable to be onboarded at all. See docs/SAFETY.md. The
    strength of the identity binding upgrades to the EUDI wallet when it ships (~Dec 2026).
    """

    class Status(models.TextChoices):
        PENDING = "pending", "Pending"
        ACCEPTED = "accepted", "Accepted"
        DECLINED = "declined", "Declined"
        EXPIRED = "expired", "Expired"

    guardian = models.ForeignKey(
        User, on_delete=models.CASCADE, related_name="guardian_link_invites_sent"
    )
    ward = models.ForeignKey(
        User, on_delete=models.CASCADE, related_name="guardian_link_invites_received"
    )
    relationship = models.CharField(max_length=32, blank=True)  # e.g. parent, legal_guardian
    token = models.CharField(max_length=64, unique=True)
    status = models.CharField(max_length=16, choices=Status.choices, default=Status.PENDING)
    created_at = models.DateTimeField(auto_now_add=True)
    expires_at = models.DateTimeField()
    responded_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        constraints = [
            # At most one OPEN invite per (guardian, ward) pair.
            models.UniqueConstraint(
                fields=["guardian", "ward"],
                condition=models.Q(status="pending"),
                name="uq_pending_guardian_invite",
            ),
            models.CheckConstraint(
                condition=~models.Q(guardian=models.F("ward")), name="guardian_invite_not_self"
            ),
        ]
        indexes = [models.Index(fields=["ward", "status"])]

    def __str__(self):
        return f"invite({self.guardian} -> {self.ward}, {self.status})"
