from django.contrib import admin

from .models import ActivityCategory, ActivityRelation, ActivityType


@admin.register(ActivityCategory)
class ActivityCategoryAdmin(admin.ModelAdmin):
    list_display = ("name", "slug", "parent")
    list_filter = ("parent",)
    search_fields = ("name", "slug")
    prepopulated_fields = {"slug": ("name",)}


@admin.register(ActivityType)
class ActivityTypeAdmin(admin.ModelAdmin):
    list_display = ("name", "slug", "category", "parent", "is_active")
    list_filter = ("category", "is_active")
    search_fields = ("name", "slug")
    prepopulated_fields = {"slug": ("name",)}


@admin.register(ActivityRelation)
class ActivityRelationAdmin(admin.ModelAdmin):
    list_display = ("source", "kind", "target", "symmetric")
    list_filter = ("kind",)
    autocomplete_fields = ("source", "target")
