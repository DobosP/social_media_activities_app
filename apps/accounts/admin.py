from django.contrib import admin
from django.contrib.auth.admin import UserAdmin as BaseUserAdmin
from django.contrib.auth.forms import UserChangeForm, UserCreationForm

from .models import AgeAssurance, GuardianRelationship, ParentalConsent, User


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


@admin.register(GuardianRelationship)
class GuardianRelationshipAdmin(admin.ModelAdmin):
    list_display = ("guardian", "ward", "relationship", "status", "created_at")
    list_filter = ("status", "relationship")
    search_fields = ("guardian__username", "ward__username")
    autocomplete_fields = ("guardian", "ward")
