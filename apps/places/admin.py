from django.contrib import admin
from django.contrib.gis import admin as gis_admin

from .edges import moderator_reverse_edge
from .models import ActivityEdgeVote, OpenNowReport, Partner, Place, PlaceActivity
from .services import clear_open_now_reports


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
