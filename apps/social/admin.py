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


admin.site.register(Thread)
admin.site.register(PlaceConfirmation)
