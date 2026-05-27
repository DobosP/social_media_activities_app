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
