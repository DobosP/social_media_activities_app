from django.contrib import admin

from .models import Connection


@admin.register(Connection)
class ConnectionAdmin(admin.ModelAdmin):
    list_display = ("requester", "addressee", "status", "created_at", "decided_at")
    list_filter = ("status",)
    search_fields = ("requester__username", "addressee__username")
    readonly_fields = ("created_at", "decided_at")
    raw_id_fields = ("requester", "addressee")
