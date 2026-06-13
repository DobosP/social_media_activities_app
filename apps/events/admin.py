from django.contrib import admin

from .models import Event, EventFeed


@admin.register(Event)
class EventAdmin(admin.ModelAdmin):
    list_display = ("title", "starts_at", "ends_at", "place", "activity_type", "source")
    list_filter = ("source", "activity_type")
    search_fields = ("title", "description", "place__name")
    autocomplete_fields = ("place", "activity_type")
    date_hierarchy = "starts_at"


@admin.register(EventFeed)
class EventFeedAdmin(admin.ModelAdmin):
    """W9: the operator surface for registered external calendars — feed health
    (last_status/last_synced_at) is visible here, not only in nightly logs."""

    list_display = ("name", "url", "is_active", "last_synced_at", "last_status")
    list_filter = ("is_active",)
    search_fields = ("name", "url")
    autocomplete_fields = ("place", "activity_type")
    readonly_fields = ("last_synced_at", "last_status", "created_at")
