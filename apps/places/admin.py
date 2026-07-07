from django.contrib import admin
from django.contrib.gis import admin as gis_admin

from .edges import moderator_reverse_edge
from .models import (
    ActivityEdgeVote,
    ApprovedChildVenue,
    ChildVenueClass,
    OpenNowReport,
    Partner,
    Place,
    PlaceActivity,
    PlaceClaim,
    PlaceCorrection,
    PlaceCover,
)
from .services import clear_open_now_reports, staff_publish_correction, staff_reject_correction


class PlaceActivityInline(gis_admin.TabularInline):
    model = PlaceActivity
    extra = 0
    autocomplete_fields = ("activity",)
    fields = ("activity", "origin", "confidence", "source", "mapping_rule", "is_disputed")
    readonly_fields = ("is_disputed",)


@gis_admin.register(Place)
class PlaceAdmin(gis_admin.GISModelAdmin):
    list_display = ("__str__", "source", "address_city", "osm_type", "osm_id")
    list_filter = ("source", "address_city")
    search_fields = ("name", "osm_id", "external_id")
    inlines = [PlaceActivityInline]
    actions = ("clear_hours_reports",)

    @admin.action(description="F28: clear open-now reports (hours self-heal)")
    def clear_hours_reports(self, request, queryset):
        cleared = sum(clear_open_now_reports(p, moderator=request.user) for p in queryset)
        self.message_user(request, f"Cleared {cleared} open-now report(s).")


@admin.register(PlaceActivity)
class PlaceActivityAdmin(admin.ModelAdmin):
    """F26: staff oversight of crowd-voted edges, with one-click moderator reversals."""

    list_display = ("place", "activity", "origin", "is_disputed", "confidence")
    list_filter = ("origin", "is_disputed", "source")
    search_fields = ("place__name", "activity__name")
    actions = ("edge_demote", "edge_restore", "edge_reset")

    @admin.action(description="F26: demote to inferred + wipe votes")
    def edge_demote(self, request, queryset):
        for edge in queryset:
            moderator_reverse_edge(request.user, edge, action="demote")
        self.message_user(request, f"Demoted {queryset.count()} edge(s).")

    @admin.action(description="F26: restore disputed edge (clear dispute votes)")
    def edge_restore(self, request, queryset):
        for edge in queryset.filter(is_disputed=True):
            moderator_reverse_edge(request.user, edge, action="restore")
        self.message_user(request, "Restored disputed edge(s).")

    @admin.action(description="F26: reset votes (keep origin)")
    def edge_reset(self, request, queryset):
        for edge in queryset:
            moderator_reverse_edge(request.user, edge, action="reset")
        self.message_user(request, f"Reset {queryset.count()} edge(s).")


@admin.register(ActivityEdgeVote)
class ActivityEdgeVoteAdmin(admin.ModelAdmin):
    list_display = ("edge", "user", "vote", "created_at")
    list_filter = ("vote",)
    search_fields = ("edge__place__name", "user__username")
    readonly_fields = ("edge", "user", "vote", "created_at")


@admin.register(OpenNowReport)
class OpenNowReportAdmin(admin.ModelAdmin):
    list_display = ("place", "reporter", "created_at")
    search_fields = ("place__name", "reporter__username")
    readonly_fields = ("place", "reporter", "created_at")


@admin.register(Partner)
class PartnerAdmin(admin.ModelAdmin):
    """Staff curation of verified civic partners (F37). Plain admin — no GIS field on Partner."""

    list_display = ("name", "kind", "place", "is_verified", "is_active")
    list_filter = ("kind", "is_verified", "is_active")
    search_fields = ("name", "blurb")


@admin.register(PlaceCorrection)
class PlaceCorrectionAdmin(admin.ModelAdmin):
    """F20: staff fast-publish / reject of crowd name/address corrections (the launch escape
    hatch when a 3-confirmer quorum won't form organically)."""

    list_display = ("place", "field", "proposed_value", "status", "created_at")
    list_filter = ("status", "field")
    search_fields = ("place__name", "proposed_value")
    readonly_fields = ("place", "proposer", "field", "proposed_value", "created_at", "published_at")
    actions = ("publish_corrections", "reject_corrections")

    @admin.action(description="Publish selected corrections (apply at read time)")
    def publish_corrections(self, request, queryset):
        done = 0
        for correction in queryset.filter(status=PlaceCorrection.Status.PENDING):
            staff_publish_correction(request.user, correction)
            done += 1
        self.message_user(request, f"Published {done} correction(s).")

    @admin.action(description="Reject selected corrections")
    def reject_corrections(self, request, queryset):
        done = 0
        for correction in queryset.exclude(status=PlaceCorrection.Status.REJECTED):
            staff_reject_correction(request.user, correction)
            done += 1
        self.message_user(request, f"Rejected {done} correction(s).")


@admin.register(ChildVenueClass)
class ChildVenueClassAdmin(admin.ModelAdmin):
    """F9: the staff-curated allowlist of venue CLASSES safe-enough for children's meetups."""

    list_display = ("key", "label", "is_active")
    list_filter = ("is_active",)
    search_fields = ("key", "label")


@admin.register(ApprovedChildVenue)
class ApprovedChildVenueAdmin(admin.ModelAdmin):
    """F9: the staff-approval path — approve a specific place for children's meetups when its
    tags don't match a ChildVenueClass (an ingest-safe per-place override)."""

    list_display = ("place", "approved_by", "created_at")
    search_fields = ("place__name", "note")
    autocomplete_fields = ("place", "approved_by")
    readonly_fields = ("created_at",)


@admin.register(PlaceClaim)
class PlaceClaimAdmin(admin.ModelAdmin):
    """ADR-0019 §6: the staff review queue for venue claims. Approve creates/refreshes the
    verified partner stewarding the place; both decisions notify the claimant and audit."""

    list_display = ("place", "org_name", "kind", "claimant", "status", "created_at")
    list_filter = ("status", "kind")
    search_fields = ("place__name", "org_name", "cui", "claimant__username")
    autocomplete_fields = ("place", "claimant")
    readonly_fields = ("created_at", "decided_by", "decided_at", "partner")
    actions = ("approve_claims", "reject_claims")

    @admin.action(description="Approve selected pending claims")
    def approve_claims(self, request, queryset):
        from .services import ClaimError, approve_place_claim

        done = 0
        for claim in queryset.filter(status=PlaceClaim.Status.PENDING):
            try:
                approve_place_claim(request.user, claim)
                done += 1
            except ClaimError as exc:
                self.message_user(request, f"{claim}: {exc}", level=30)
        self.message_user(request, f"Approved {done} claim(s).")

    @admin.action(description="Reject selected pending claims")
    def reject_claims(self, request, queryset):
        from .services import ClaimError, reject_place_claim

        done = 0
        for claim in queryset.filter(status=PlaceClaim.Status.PENDING):
            try:
                reject_place_claim(request.user, claim)
                done += 1
            except ClaimError as exc:
                self.message_user(request, f"{claim}: {exc}", level=30)
        self.message_user(request, f"Rejected {done} claim(s).")


@admin.register(PlaceCover)
class PlaceCoverAdmin(admin.ModelAdmin):
    """P6b staff recovery surface for venue images (business uploads + cached Commons).
    Deleting a row reclaims the stored blob via the media pre_delete signal."""

    list_display = ("place", "source", "uploaded_by", "byte_size", "updated_at")
    list_filter = ("source",)
    search_fields = ("place__name", "attribution", "uploaded_by__username")
    autocomplete_fields = ("place", "uploaded_by")
    readonly_fields = (
        "storage_key",
        "content_type",
        "byte_size",
        "width",
        "height",
        "sha256",
        "exif_stripped",
        "created_at",
        "updated_at",
    )
