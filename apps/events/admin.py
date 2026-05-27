from django.contrib import admin

from .models import Event


@admin.register(Event)
class EventAdmin(admin.ModelAdmin):
    list_display = ("title", "starts_at", "ends_at", "place", "activity_type", "source")
    list_filter = ("source", "activity_type")
    search_fields = ("title", "description", "place__name")
    autocomplete_fields = ("place", "activity_type")
    date_hierarchy = "starts_at"
