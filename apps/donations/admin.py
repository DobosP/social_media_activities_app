from django.contrib import admin

from .models import Campaign, Donation, SpendEntry


@admin.register(Donation)
class DonationAdmin(admin.ModelAdmin):
    list_display = (
        "id",
        "amount_cents",
        "currency",
        "provider",
        "status",
        "donor",
        "campaign",
        "created_at",
    )
    list_filter = ("status", "provider", "currency", "campaign")
    search_fields = ("external_ref", "donor__username")
    readonly_fields = ("external_ref", "created_at", "completed_at")


@admin.register(Campaign)
class CampaignAdmin(admin.ModelAdmin):
    """Staff entry point for earmark campaigns (F34) + optional partner credit (F42)."""

    list_display = ("title", "slug", "goal_cents", "currency", "is_active", "partner", "created")
    list_filter = ("is_active", "currency")
    search_fields = ("title", "slug")
    prepopulated_fields = {"slug": ("title",)}
    readonly_fields = ("created",)

    def formfield_for_foreignkey(self, db_field, request, **kwargs):
        # F42: a campaign may only credit a verified+active partner — the same public()
        # chokepoint used everywhere else. (Model.clean() re-checks; read-time re-checks again.)
        if db_field.name == "partner":
            from apps.places.models import Partner

            kwargs["queryset"] = Partner.objects.public().order_by("name")
        return super().formfield_for_foreignkey(db_field, request, **kwargs)


@admin.register(SpendEntry)
class SpendEntryAdmin(admin.ModelAdmin):
    """Staff entry point for the public spend ledger (F29)."""

    list_display = ("category", "amount_cents", "currency", "period", "created_at")
    list_filter = ("currency",)
    search_fields = ("category", "note")
