from django.contrib import admin

from .models import Booking, PlaceBookingInfo


@admin.register(PlaceBookingInfo)
class PlaceBookingInfoAdmin(admin.ModelAdmin):
    list_display = ("place", "provider", "deep_link")
    search_fields = ("place__name", "provider")


@admin.register(Booking)
class BookingAdmin(admin.ModelAdmin):
    list_display = ("id", "user", "place", "provider", "status", "starts_at")
    list_filter = ("status", "provider")
    search_fields = ("user__username", "place__name", "external_ref")
