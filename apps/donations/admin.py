from django.contrib import admin

from .models import Donation


@admin.register(Donation)
class DonationAdmin(admin.ModelAdmin):
    list_display = ("id", "amount_cents", "currency", "provider", "status", "donor", "created_at")
    list_filter = ("status", "provider", "currency")
    search_fields = ("external_ref", "donor__username")
    readonly_fields = ("external_ref", "created_at", "completed_at")
