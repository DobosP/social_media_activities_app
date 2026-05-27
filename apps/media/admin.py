from django.contrib import admin

from .models import MediaImage


@admin.register(MediaImage)
class MediaImageAdmin(admin.ModelAdmin):
    list_display = ("kind", "owner", "thread", "status", "content_type", "created_at")
    list_filter = ("kind", "status")
    search_fields = ("owner__username", "storage_key")
    raw_id_fields = ("owner", "thread")
    readonly_fields = ("created_at", "public_id")
