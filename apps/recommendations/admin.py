from django.contrib import admin

from .models import ActivityEmbedding, UserInterest


@admin.register(UserInterest)
class UserInterestAdmin(admin.ModelAdmin):
    list_display = ("user", "activity_type", "created_at")
    search_fields = ("user__username", "activity_type__slug")


@admin.register(ActivityEmbedding)
class ActivityEmbeddingAdmin(admin.ModelAdmin):
    list_display = ("activity", "updated_at")
    search_fields = ("activity__title",)
