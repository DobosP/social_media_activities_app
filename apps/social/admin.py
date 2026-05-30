from django.contrib import admin

from .models import (
    Activity,
    JoinVote,
    Membership,
    PlaceConfirmation,
    Post,
    Thread,
    UserPlaceProposal,
)


class MembershipInline(admin.TabularInline):
    model = Membership
    extra = 0
    fields = ("user", "role", "state", "decided_at")
    readonly_fields = ("decided_at",)


@admin.register(Activity)
class ActivityAdmin(admin.ModelAdmin):
    list_display = ("title", "cohort", "status", "owner", "place", "starts_at", "join_threshold")
    list_filter = ("cohort", "status", "activity_type")
    search_fields = ("title", "owner__username")
    autocomplete_fields = ("owner", "place", "activity_type")
    inlines = [MembershipInline]


@admin.register(Membership)
class MembershipAdmin(admin.ModelAdmin):
    list_display = ("activity", "user", "role", "state", "decided_at")
    list_filter = ("role", "state")
    search_fields = ("activity__title", "user__username")


@admin.register(JoinVote)
class JoinVoteAdmin(admin.ModelAdmin):
    list_display = ("membership", "voter", "approve", "created_at")
    list_filter = ("approve",)


@admin.register(Post)
class PostAdmin(admin.ModelAdmin):
    list_display = ("thread", "author", "created_at")
    search_fields = ("author__username", "body")


@admin.register(UserPlaceProposal)
class UserPlaceProposalAdmin(admin.ModelAdmin):
    list_display = ("place", "proposer", "status", "required_confirmations", "published_at")
    list_filter = ("status",)
    search_fields = ("place__name", "proposer__username")
    readonly_fields = ("place", "proposer", "created_at", "published_at")
    actions = ("publish_selected", "reject_selected")

    @admin.action(description="Publish selected proposals (F25 staff fast-path)")
    def publish_selected(self, request, queryset):
        from .services import staff_publish_proposal

        n = 0
        for proposal in queryset:
            try:
                staff_publish_proposal(request.user, proposal)
                n += 1
            except Exception as exc:  # noqa: BLE001 — surface to the admin, keep going
                self.message_user(request, f"{proposal}: {exc}", level="error")
        self.message_user(request, f"Published {n} proposal(s).")

    @admin.action(description="Reject selected proposals")
    def reject_selected(self, request, queryset):
        from .services import staff_reject_proposal

        n = 0
        for proposal in queryset:
            try:
                staff_reject_proposal(request.user, proposal)
                n += 1
            except Exception as exc:  # noqa: BLE001
                self.message_user(request, f"{proposal}: {exc}", level="error")
        self.message_user(request, f"Rejected {n} proposal(s).")


admin.site.register(Thread)
admin.site.register(PlaceConfirmation)
