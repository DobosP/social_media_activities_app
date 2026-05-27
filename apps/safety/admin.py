from django.contrib import admin
from django.utils import timezone

from .models import AuditLog, Block, ModerationAction, Report


@admin.register(Report)
class ReportAdmin(admin.ModelAdmin):
    """The moderation review queue: triage reports and record resolutions."""

    list_display = ("id", "reason", "status", "target_type", "target_id", "reporter", "created_at")
    list_filter = ("status", "reason", "target_type")
    search_fields = ("detail", "resolution")
    readonly_fields = ("target_type", "target_id", "reporter", "created_at")
    actions = ("mark_reviewing", "dismiss")

    @admin.action(description="Mark selected reports as reviewing")
    def mark_reviewing(self, request, queryset):
        queryset.update(status=Report.Status.REVIEWING)

    @admin.action(description="Dismiss selected reports")
    def dismiss(self, request, queryset):
        queryset.update(
            status=Report.Status.DISMISSED, handled_by=request.user, handled_at=timezone.now()
        )


@admin.register(ModerationAction)
class ModerationActionAdmin(admin.ModelAdmin):
    list_display = ("id", "action", "reason", "target_type", "target_id", "moderator", "created_at")
    list_filter = ("action", "reason")


@admin.register(Block)
class BlockAdmin(admin.ModelAdmin):
    list_display = ("blocker", "blocked", "created_at")
    search_fields = ("blocker__username", "blocked__username")


@admin.register(AuditLog)
class AuditLogAdmin(admin.ModelAdmin):
    list_display = ("id", "event", "actor", "target_ref", "created_at")
    list_filter = ("event",)
    search_fields = ("event", "target_ref")
    readonly_fields = ("actor", "event", "target_ref", "data", "created_at", "prev_hash", "hash")

    def has_add_permission(self, request):
        return False

    def has_change_permission(self, request, obj=None):
        return False

    def has_delete_permission(self, request, obj=None):
        return False
