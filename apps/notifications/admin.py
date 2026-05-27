from django.contrib import admin

from .models import Notification


@admin.register(Notification)
class NotificationAdmin(admin.ModelAdmin):
    list_display = ("id", "kind", "recipient", "title", "read_at", "created_at")
    list_filter = ("kind",)
    search_fields = ("recipient__username", "title")
    readonly_fields = ("created_at",)
