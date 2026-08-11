from django.contrib import admin

from .models import (
    AuditLog,
    AuthorityReferral,
    Block,
    ModerationAction,
    ModerationAppeal,
    Report,
)


@admin.register(Report)
class ReportAdmin(admin.ModelAdmin):
    """The moderation review queue: triage reports and record resolutions."""

    list_display = ("id", "reason", "status", "target_type", "target_id", "reporter", "created_at")
    list_filter = ("status", "reason", "target_type")
    search_fields = ("detail", "resolution")
    readonly_fields = ("target_type", "target_id", "reporter", "created_at")
    actions = ("mark_reviewing", "dismiss", "ban_target")

    @admin.action(description="Mark selected reports as reviewing")
    def mark_reviewing(self, request, queryset):
        queryset.update(status=Report.Status.REVIEWING)

    @admin.action(description="Dismiss selected reports")
    def dismiss(self, request, queryset):
        from .services import dismiss_report

        dismissed = 0
        for report in queryset:
            dismiss_report(request.user, report)
            dismissed += 1
        self.message_user(request, f"Dismissed {dismissed} report(s).")

    @admin.action(description="Ban the reported target (account)")
    def ban_target(self, request, queryset):
        from .services import take_action

        banned = 0
        for report in queryset:
            if report.target is not None:
                take_action(
                    request.user,
                    report.target,
                    ModerationAction.Action.BAN,
                    report.reason,
                    report=report,
                )
                banned += 1
        self.message_user(request, f"Banned {banned} target(s).")


@admin.register(ModerationAction)
class ModerationActionAdmin(admin.ModelAdmin):
    list_display = ("id", "action", "reason", "target_type", "target_id", "moderator", "created_at")
    list_filter = ("action", "reason")


@admin.register(ModerationAppeal)
class ModerationAppealAdmin(admin.ModelAdmin):
    """DSA Art.17 internal complaint-handling queue: review user contests and uphold or overturn.

    Both actions go through the audited ``resolve_appeal`` service (never a raw status edit), so an
    overturn reactivates the account / un-hides content and notifies the user + a CHILD's guardian.
    """

    list_display = (
        "id",
        "action",
        "status",
        "appellant",
        "content_note",
        "created_at",
        "decided_at",
    )
    list_filter = ("status",)
    # content_note reads the appeal's action; without this the DSA queue issues one extra query
    # per row just to render the column.
    list_select_related = ("action", "appellant")
    readonly_fields = (
        "action",
        "appellant",
        "statement",
        "created_at",
        "decided_by",
        "decided_at",
        "content_note",
    )
    actions = ("uphold", "overturn")

    def has_add_permission(self, request):
        # Appeals are filed by users through file_appeal, never hand-created in admin.
        return False

    def has_delete_permission(self, request, obj=None):
        return False

    @admin.display(description="Content note")
    def content_note(self, obj):
        # Pre-decision context: overturning a REMOVE never republishes content its author also
        # deleted themselves (the un-hide is declined — apps/safety/services.py::_reverse_action),
        # so the reviewer should know that BEFORE deciding. The action-type check runs first so
        # only REMOVE rows pay the generic-FK resolve (a bounded, staff-only per-row read). The
        # is_hidden gate mirrors _reverse_action: an operator-un-hidden (live) post has nothing
        # left to republish, so the note must not claim otherwise.
        if obj.action.action != ModerationAction.Action.REMOVE:
            return ""
        target = obj.action.target
        if getattr(target, "is_author_deleted", False) and getattr(target, "is_hidden", False):
            return "Author deleted this content themselves — overturning will not republish it."
        return ""

    @admin.action(description="Uphold (decision stands)")
    def uphold(self, request, queryset):
        from .services import AppealError, resolve_appeal

        done = 0
        for appeal in queryset:
            try:
                resolve_appeal(request.user, appeal, grant=False)
                done += 1
            except AppealError as exc:
                self.message_user(request, f"Appeal #{appeal.pk}: {exc}", level="warning")
        self.message_user(request, f"Upheld {done} appeal(s).")

    @admin.action(description="Overturn (reverse the decision)")
    def overturn(self, request, queryset):
        from .services import AppealError, resolve_appeal

        done = 0
        for appeal in queryset:
            try:
                # Capture the RETURNED instance: resolve_appeal re-reads the appeal under lock,
                # and the transient ``reversal_outcome`` exists only on what it returns — never
                # on this queryset's element.
                resolved = resolve_appeal(request.user, appeal, grant=True)
                done += 1
            except AppealError as exc:
                self.message_user(request, f"Appeal #{appeal.pk}: {exc}", level="warning")
                continue
            outcome = getattr(resolved, "reversal_outcome", None)
            if outcome and outcome.left_hidden_author_deleted:
                self.message_user(
                    request,
                    f"Appeal #{appeal.pk}: overturned; the author had deleted this content "
                    "themselves, so it stays hidden.",
                    level="info",
                )
        self.message_user(request, f"Overturned {done} appeal(s).")


@admin.register(Block)
class BlockAdmin(admin.ModelAdmin):
    list_display = ("blocker", "blocked", "created_at")
    search_fields = ("blocker__username", "blocked__username")


@admin.register(AuthorityReferral)
class AuthorityReferralAdmin(admin.ModelAdmin):
    """Read-only ledger of referrals to external authorities (legal defensibility)."""

    list_display = ("id", "authority", "reason", "subject_ref", "referred_by", "created_at")
    list_filter = ("authority", "reason")
    search_fields = ("subject_ref", "reference")
    readonly_fields = (
        "subject_ref",
        "reason",
        "authority",
        "reference",
        "report",
        "referred_by",
        "audit_anchor_hash",
        "notes",
        "created_at",
    )

    def has_add_permission(self, request):
        # Referrals are created through the audited service (create_authority_referral), never
        # hand-typed in admin, so the audit anchor + chain entry are always captured.
        return False

    def has_change_permission(self, request, obj=None):
        return False

    def has_delete_permission(self, request, obj=None):
        return False


@admin.register(AuditLog)
class AuditLogAdmin(admin.ModelAdmin):
    list_display = ("id", "event", "actor", "target_ref", "created_at")
    list_filter = ("event",)
    search_fields = ("event", "target_ref")
    readonly_fields = ("actor", "event", "target_ref", "data", "created_at", "prev_hash", "hash")

    def has_add_permission(self, request):
        return False

    def has_change_permission(self, request, obj=None):
        return False

    def has_delete_permission(self, request, obj=None):
        return False
