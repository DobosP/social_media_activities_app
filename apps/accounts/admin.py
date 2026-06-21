from django.contrib import admin
from django.contrib.auth.admin import UserAdmin as BaseUserAdmin
from django.contrib.auth.forms import UserChangeForm, UserCreationForm
from django.utils import timezone

from .models import (
    AgeAssurance,
    BannedIdentity,
    GuardianRelationship,
    IdentityBinding,
    ParentalConsent,
    User,
)


class CustomUserCreationForm(UserCreationForm):
    class Meta(UserCreationForm.Meta):
        model = User
        fields = ("username",)


class CustomUserChangeForm(UserChangeForm):
    class Meta(UserChangeForm.Meta):
        model = User
        fields = "__all__"


@admin.register(User)
class UserAdmin(BaseUserAdmin):
    form = CustomUserChangeForm
    add_form = CustomUserCreationForm
    ordering = ("username",)
    list_display = (
        "username",
        "display_name",
        "role",
        "age_band",
        "cohort",
        "is_identity_verified",
        "is_staff",
    )
    list_filter = ("role", "age_band", "cohort", "is_identity_verified", "is_staff", "is_superuser")
    search_fields = ("username", "display_name", "public_id")
    readonly_fields = ("public_id", "identity_verified_at", "last_login", "date_joined")
    fieldsets = (
        (None, {"fields": ("username", "password")}),
        ("Profile", {"fields": ("display_name", "public_id")}),
        ("Role", {"fields": ("role",)}),
        (
            "Age & identity",
            {"fields": ("age_band", "cohort", "is_identity_verified", "identity_verified_at")},
        ),
        (
            "Permissions",
            {"fields": ("is_active", "is_staff", "is_superuser", "groups", "user_permissions")},
        ),
        ("Dates", {"fields": ("last_login", "date_joined")}),
    )
    add_fieldsets = (
        (None, {"classes": ("wide",), "fields": ("username", "password1", "password2")}),
    )


@admin.register(AgeAssurance)
class AgeAssuranceAdmin(admin.ModelAdmin):
    list_display = ("user", "age_band", "provider", "method", "verified_at", "expires_at")
    list_filter = ("provider", "age_band")
    search_fields = ("user__username",)


@admin.register(ParentalConsent)
class ParentalConsentAdmin(admin.ModelAdmin):
    list_display = ("minor", "status", "guardian_identifier", "granted_at", "expires_at")
    list_filter = ("status",)
    search_fields = ("minor__username", "guardian_identifier")


@admin.register(IdentityBinding)
class IdentityBindingAdmin(admin.ModelAdmin):
    """Inspect one-person identity bindings and release one (the 'fresh start' path) so the wallet
    can re-bind. Stores only the keyed HMAC of the holder subject, never the raw subject."""

    list_display = ("id", "holder_hash", "user", "created_at", "released_at")
    search_fields = ("holder_hash", "user__username")
    readonly_fields = ("holder_hash", "user", "created_at", "released_at")
    actions = ("release_bindings",)

    def has_add_permission(self, request):
        return False  # bindings are written by bind_identity, never hand-created

    @admin.action(description="Release selected bindings (allow re-bind)")
    def release_bindings(self, request, queryset):
        from apps.safety.services import record_audit

        released = 0
        for binding in queryset.filter(released_at__isnull=True):
            binding.released_at = timezone.now()
            binding.save(update_fields=["released_at"])
            record_audit("identity.binding_released", actor=request.user, target=binding)
            released += 1
        self.message_user(request, f"Released {released} binding(s).")


@admin.register(BannedIdentity)
class BannedIdentityAdmin(admin.ModelAdmin):
    """Inspect the lifetime identity-ban ledger and lift a ban (allow the wallet to re-register) —
    the staff path that also covers a ban whose account was already erased (orphaned binding)."""

    list_display = ("id", "holder_hash", "created_at")
    search_fields = ("holder_hash",)
    readonly_fields = ("holder_hash", "created_at")
    actions = ("lift_bans",)

    def has_add_permission(self, request):
        return False  # the ledger is written by ban_identity, never hand-created

    @admin.action(description="Lift selected identity bans (allow re-register)")
    def lift_bans(self, request, queryset):
        from apps.safety.services import record_audit

        lifted = 0
        for banned in queryset:
            record_audit(
                "identity.ban_released", actor=request.user, hash_prefix=banned.holder_hash[:12]
            )
            lifted += 1
        queryset.delete()
        self.message_user(request, f"Lifted {lifted} identity ban(s).")


@admin.register(GuardianRelationship)
class GuardianRelationshipAdmin(admin.ModelAdmin):
    list_display = ("guardian", "ward", "relationship", "status", "created_at")
    list_filter = ("status", "relationship")
    search_fields = ("guardian__username", "ward__username")
    autocomplete_fields = ("guardian", "ward")
