from django.conf import settings
from django.db import models
from django.db.models import Q

from apps.accounts.models import Cohort
from apps.social.models import Activity, ActivityInterest


class SavedSearch(models.Model):
    """F3: a user-saved discovery filter. The nightly matcher tells the saver ONCE when a new
    activity they could already see matches it. ``cohort`` is PINNED from the user at create (the
    isolation boundary, re-asserted in the matcher). Geo scope is AREA-ONLY — there is **no stored
    coordinate** (privacy: never store user location). Exactly one of activity_type / category.
    A discovery filter only — never a 'shared activity', so it opens no private-contact path."""

    user = models.ForeignKey(
        settings.AUTH_USER_MODEL, on_delete=models.CASCADE, related_name="saved_searches"
    )
    # Pinned from the user's cohort at create; the isolation boundary (re-asserted at match time).
    cohort = models.CharField(max_length=16, choices=Cohort.choices)
    activity_type = models.ForeignKey(
        "taxonomy.ActivityType",
        on_delete=models.PROTECT,
        null=True,
        blank=True,
        related_name="saved_searches",
    )
    category = models.ForeignKey(
        "taxonomy.ActivityCategory",
        on_delete=models.PROTECT,
        null=True,
        blank=True,
        related_name="saved_searches",
    )
    # The ONLY geo scope — a city Area, never a coordinate.
    area = models.ForeignKey(
        "communities.Area",
        on_delete=models.PROTECT,
        null=True,
        blank=True,
        related_name="saved_searches",
    )
    beginners = models.BooleanField(default=False)  # applied as a filter only when True
    cost_band = models.CharField(
        max_length=16, choices=Activity.CostBand.choices, blank=True, default=""
    )  # applied only when non-empty; exact match (mirrors the F17 ?beginners filter)
    # F12: optional schedule-fit window (weekday/weekend × daytime/evening). Reuses the shipped
    # ActivityInterest.CoarseWindow choices; applied only when non-empty, judged in LOCAL time at
    # read time (nothing time-derived is stored on the Activity). No coordinate, no per-user log.
    coarse_window = models.CharField(
        max_length=16, choices=ActivityInterest.CoarseWindow.choices, blank=True, default=""
    )
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        verbose_name_plural = "saved searches"
        constraints = [
            # Exactly one of activity_type / category (enforced at form, serializer, and DB layers).
            models.CheckConstraint(
                condition=(
                    Q(activity_type__isnull=False, category__isnull=True)
                    | Q(activity_type__isnull=True, category__isnull=False)
                ),
                name="savedsearch_type_xor_category",
            ),
        ]
        indexes = [
            models.Index(fields=["cohort", "activity_type"]),
            models.Index(fields=["cohort", "category"]),
            models.Index(fields=["user"]),
        ]

    def __str__(self):
        return f"SavedSearch(user={self.user_id})"


class SavedSearchMatch(models.Model):
    """One-notice-per-(user, activity) ledger. Written the first time a candidate matches AND passes
    the per-saver read gate — even when the notice is muted — so a saver is alerted at most ONCE per
    given activity, ever: never re-fired after un-mute, never replayed by recreating a search.
    Keyed on (user, activity), NOT (search, activity)."""

    user = models.ForeignKey(settings.AUTH_USER_MODEL, on_delete=models.CASCADE)
    activity = models.ForeignKey("social.Activity", on_delete=models.CASCADE)
    created_at = models.DateTimeField(auto_now_add=True)

    class Meta:
        constraints = [
            models.UniqueConstraint(
                fields=["user", "activity"], name="uq_savedsearchmatch_user_activity"
            ),
        ]

    def __str__(self):
        return f"SavedSearchMatch(user={self.user_id}, activity={self.activity_id})"
