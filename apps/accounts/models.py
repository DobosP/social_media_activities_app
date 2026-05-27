import uuid

from django.contrib.auth.models import AbstractBaseUser, BaseUserManager, PermissionsMixin
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

    user = models.ForeignKey(User, on_delete=models.CASCADE, related_name="age_assurances")
    provider = models.CharField(max_length=64)
    method = models.CharField(max_length=64, blank=True)
    age_band = models.CharField(max_length=16, choices=AgeBand.choices)
    verified_at = models.DateTimeField(default=timezone.now)
    expires_at = models.DateTimeField(null=True, blank=True)
    raw = models.JSONField(default=dict, blank=True)

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
