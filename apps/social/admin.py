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
    # is_hidden/is_author_deleted are surfaced so an operator can answer "why is this post
    # hidden?" from the changelist — the question the provenance field exists to answer.
    list_display = ("thread", "author", "created_at", "is_hidden", "is_author_deleted")
    search_fields = ("author__username", "body")
    # is_author_deleted records the AUTHOR's own decision, so nothing may clear it — not a
    # granted appeal (apps/safety/services.py::_reverse_action) and not an admin edit either.
    # Without this the model's "never cleared" invariant is only a convention, and clearing it
    # here would re-arm the republish path with no audit row. is_hidden stays editable: it is a
    # moderation state with a legitimate operator escape hatch (owner-ratified 2026-08-12).
    #
    # KNOW THIS BEFORE HIDING A POST HERE: an admin hide writes no ModerationAction, so it has no
    # provenance. Once the author ALSO deletes the post, the row (is_hidden + is_author_deleted +
    # no action row) is byte-identical to a plain self-delete — indistinguishable in data — and
    # its expired media blob is therefore reclaimed by the purge rather than preserved as
    # evidence (apps/media/services.py::purge_expired_attachments). An administrative hold that
    # must survive the author's own deletion needs a real REMOVE action, not this checkbox.
    readonly_fields = ("is_author_deleted",)


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
