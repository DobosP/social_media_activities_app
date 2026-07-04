"""Production deferred-task handlers.

Imported at app startup (see ``OpsConfig.ready``) so every ``@register`` runs before any
``enqueue`` call. Handlers stay thin, idempotent, and bounded; they take IDs/minimal scalars from
payloads and re-load/re-check live state.
"""

from __future__ import annotations

from django.conf import settings
from django.core.management import call_command

from .tasks import register


def _str_list(payload: dict, key: str, *, limit: int) -> list[str]:
    raw = payload.get(key) or []
    if isinstance(raw, str):
        raw = [raw]
    if not isinstance(raw, list):
        raise ValueError(f"{key} must be a list")
    values = [v for v in raw[:limit] if isinstance(v, str) and v.strip()]
    return values


@register("erasure.blob_cleanup")
def erasure_blob_cleanup(payload: dict) -> None:
    """Delete already-unreferenced media blobs.

    Idempotent: deleting a missing key is treated as success by the storage backends. The payload is
    bounded and carries object keys only, never media bytes or user PII.
    """
    from apps.media.storage import get_storage
    from apps.safety.services import record_audit

    keys = _str_list(
        payload,
        "blob_keys",
        limit=getattr(settings, "DEFERRED_BLOB_CLEANUP_MAX_KEYS", 200),
    )
    if not keys:
        return
    storage = get_storage()
    for key in keys:
        storage.delete(key)
    record_audit("erasure.blob_cleanup", actor=None, blob_count=len(keys))


@register("media.scan.dispatch")
def media_scan_dispatch(payload: dict) -> None:
    """Fail-closed placeholder for future withheld-media scanning.

    Current media rows are only created after synchronous fail-closed scanning. There is no
    PENDING/withheld row state yet, so executing this task must never mark media clean or visible.
    It records the attempted dispatch and returns without changing media state.
    """
    from apps.safety.services import record_audit

    attachment_id = payload.get("attachment_id")
    photo_id = payload.get("photo_id")
    record_audit(
        "media.scan_dispatch_blocked",
        actor=None,
        attachment_id=attachment_id,
        photo_id=photo_id,
        reason="withheld_state_not_implemented",
    )


@register("notify.activity_fanout")
def notify_activity_fanout(payload: dict) -> None:
    """Fan out one in-app notice to current activity members, re-checking live block/mute gates."""
    from apps.notifications.models import Notification
    from apps.notifications.services import notify
    from apps.safety.services import blocked_user_ids
    from apps.social.models import Activity, Membership

    try:
        activity_id = int(payload["activity_id"])
    except (KeyError, TypeError, ValueError) as exc:
        raise ValueError("activity_id is required") from exc
    activity = (
        Activity.objects.filter(pk=activity_id, is_hidden=False).select_related("owner").first()
    )
    if activity is None:
        return

    kind = str(payload.get("kind") or Notification.Kind.ANNOUNCEMENT.value)
    valid_kinds = {choice.value for choice in Notification.Kind}
    if kind not in valid_kinds:
        raise ValueError("unknown notification kind")
    title = str(payload.get("title") or "")[:200]
    body = str(payload.get("body") or "")[:600]
    url = str(payload.get("url") or f"/api/social/activities/{activity.id}/")[:300]
    try:
        exclude_user_id = int(payload.get("exclude_user_id") or 0)
    except (TypeError, ValueError):
        exclude_user_id = 0

    blocked = blocked_user_ids(activity.owner) if activity.owner_id else set()
    qs = (
        Membership.objects.filter(activity=activity, state=Membership.State.MEMBER)
        .exclude(user_id=exclude_user_id)
        .exclude(user_id__in=blocked)
        .select_related("user")
        .order_by("id")
    )
    max_recipients = getattr(settings, "DEFERRED_NOTIFY_FANOUT_MAX_RECIPIENTS", 500)
    for membership in qs[:max_recipients]:
        if Notification.objects.filter(
            recipient=membership.user, kind=kind, url=url, title=title
        ).exists():
            continue
        notify(membership.user, kind, title, body=body, url=url)


@register("notifications.retention_purge")
def notifications_retention_purge(payload: dict) -> None:
    """Delete one bounded batch of old read, mutable notifications."""
    from apps.notifications.services import purge_read_notifications

    default_days = getattr(settings, "NOTIFICATION_RETENTION_DAYS", 180)
    max_batch = getattr(settings, "NOTIFICATION_RETENTION_BATCH", 1000)
    try:
        days = int(payload.get("days") or default_days)
    except (TypeError, ValueError):
        days = default_days
    try:
        requested_batch = int(payload.get("batch_size") or max_batch)
    except (TypeError, ValueError):
        requested_batch = max_batch
    purge_read_notifications(days=days, batch_size=max(0, min(requested_batch, max_batch)))


@register("cron.run_command")
def cron_run_command(payload: dict) -> None:
    """Run one allowlisted periodic command as its own retryable task."""
    from apps.ops.management.commands.run_due_jobs import DUE_JOBS

    command = str(payload.get("command") or "")
    allowed = {name for name, _ in DUE_JOBS if name != "process_deferred_tasks"}
    if command not in allowed:
        raise ValueError("command is not an allowlisted due job")
    kwargs = payload.get("kwargs") or {}
    if not isinstance(kwargs, dict):
        raise ValueError("kwargs must be an object")
    call_command(command, **kwargs)
