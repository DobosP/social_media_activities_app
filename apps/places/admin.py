from django.contrib import admin
from django.contrib.gis import admin as gis_admin

from .models import Partner, Place, PlaceActivity


class PlaceActivityInline(gis_admin.TabularInline):
    model = PlaceActivity
    extra = 0
    autocomplete_fields = ("activity",)
    fields = ("activity", "origin", "confidence", "source", "mapping_rule")


@gis_admin.register(Place)
class PlaceAdmin(gis_admin.GISModelAdmin):
    list_display = ("__str__", "source", "address_city", "osm_type", "osm_id")
    list_filter = ("source", "address_city")
    search_fields = ("name", "osm_id", "external_id")
    inlines = [PlaceActivityInline]


@admin.register(Partner)
class PartnerAdmin(admin.ModelAdmin):
    """Staff curation of verified civic partners (F37). Plain admin — no GIS field on Partner."""

    list_display = ("name", "kind", "place", "is_verified", "is_active")
    list_filter = ("kind", "is_verified", "is_active")
    search_fields = ("name", "blurb")
