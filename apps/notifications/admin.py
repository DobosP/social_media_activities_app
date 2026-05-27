from django.contrib import admin

from .models import Notification, NotificationPreference


@admin.register(Notification)
class NotificationAdmin(admin.ModelAdmin):
    list_display = ("id", "recipient", "ntype", "title", "read_at", "created_at")
    list_filter = ("ntype",)
    search_fields = ("recipient__username", "title")


@admin.register(NotificationPreference)
class NotificationPreferenceAdmin(admin.ModelAdmin):
    list_display = ("user", "activity_updates", "event_reminders", "system")
