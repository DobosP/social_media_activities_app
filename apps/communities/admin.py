from django.contrib import admin

from .models import Area, Community


@admin.register(Area)
class AreaAdmin(admin.ModelAdmin):
    list_display = ("name", "city", "derive_method", "min_radius_m", "is_active")
    list_filter = ("derive_method", "is_active")
    search_fields = ("name", "city", "slug")


@admin.register(Community)
class CommunityAdmin(admin.ModelAdmin):
    list_display = ("name", "cohort", "tier", "area", "is_published", "last_evaluated_at")
    list_filter = ("cohort", "tier", "is_published")
    search_fields = ("name", "slug")
    readonly_fields = ("last_evaluated_at", "created_at")
