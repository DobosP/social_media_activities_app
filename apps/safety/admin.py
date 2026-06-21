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

    list_display = ("id", "action", "status", "appellant", "created_at", "decided_at")
    list_filter = ("status",)
    readonly_fields = (
        "action",
        "appellant",
        "statement",
        "created_at",
        "decided_by",
        "decided_at",
    )
    actions = ("uphold", "overturn")

    def has_add_permission(self, request):
        # Appeals are filed by users through file_appeal, never hand-created in admin.
        return False

    def has_delete_permission(self, request, obj=None):
        return False

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
                resolve_appeal(request.user, appeal, grant=True)
                done += 1
            except AppealError as exc:
                self.message_user(request, f"Appeal #{appeal.pk}: {exc}", level="warning")
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
