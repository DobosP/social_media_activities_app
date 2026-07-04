"""The durable off-request task foundation (apps.ops.tasks + DeferredTask): transactional enqueue,
at-least-once draining with SKIP-LOCKED claims, bounded backoff retries that exhaust to FAILED,
idempotent dedup, payload isolation, and the wiring into the run_due_jobs cron tick."""

from io import StringIO

import pytest
from django.core.management import call_command
from django.db import IntegrityError, transaction
from django.test import override_settings
from django.utils import timezone

from apps.ops import tasks
from apps.ops.models import DeferredTask

pytestmark = pytest.mark.django_db


@pytest.fixture(autouse=True)
def _isolate_registry():
    """Snapshot/restore the handler registry so a test's handlers never leak into other tests
    (and the empty production registry stays empty elsewhere)."""
    saved = dict(tasks._REGISTRY)
    try:
        yield
    finally:
        tasks._REGISTRY.clear()
        tasks._REGISTRY.update(saved)


# --- enqueue ----------------------------------------------------------------------------------


def test_enqueue_creates_a_pending_row():
    tasks.register("test.ok")(lambda payload: None)
    task = tasks.enqueue("test.ok", {"user_id": 7})

    task.refresh_from_db()
    assert task.status == DeferredTask.Status.PENDING
    assert task.kind == "test.ok"
    assert task.payload == {"user_id": 7}
    assert task.attempts == 0
    assert DeferredTask.objects.count() == 1


def test_enqueue_unregistered_kind_fails_fast():
    with pytest.raises(ValueError):
        tasks.enqueue("test.nope", {})
    assert DeferredTask.objects.count() == 0


def test_enqueue_is_transactional():
    """A rolled-back transaction enqueues nothing — the task commits with the business write."""
    tasks.register("test.ok")(lambda payload: None)
    with pytest.raises(RuntimeError):
        with transaction.atomic():
            tasks.enqueue("test.ok", {})
            raise RuntimeError("business write failed")
    assert DeferredTask.objects.count() == 0


def test_enqueue_delay_sets_future_available_at():
    tasks.register("test.ok")(lambda payload: None)
    before = timezone.now()
    task = tasks.enqueue("test.ok", {}, delay=timezone.timedelta(hours=1))
    assert task.available_at > before + timezone.timedelta(minutes=59)


# --- dedup ------------------------------------------------------------------------------------


def test_dedup_key_collapses_duplicate_pending_tasks():
    tasks.register("test.ok")(lambda payload: None)
    a = tasks.enqueue("test.ok", {"u": 1}, dedup_key="user:1")
    b = tasks.enqueue("test.ok", {"u": 1}, dedup_key="user:1")
    assert a.pk == b.pk
    assert DeferredTask.objects.filter(status=DeferredTask.Status.PENDING).count() == 1


def test_dedup_allows_a_new_task_once_the_prior_one_is_terminal():
    tasks.register("test.ok")(lambda payload: None)
    a = tasks.enqueue("test.ok", {}, dedup_key="user:1")
    tasks.run_pending_tasks()
    a.refresh_from_db()
    assert a.status == DeferredTask.Status.DONE
    # The constraint only blocks a *PENDING* duplicate, so a fresh enqueue is allowed.
    b = tasks.enqueue("test.ok", {}, dedup_key="user:1")
    assert b.pk != a.pk


def test_db_constraint_blocks_two_pending_rows_with_same_dedup_key():
    DeferredTask.objects.create(kind="k", dedup_key="d")
    with pytest.raises(IntegrityError):
        with transaction.atomic():
            DeferredTask.objects.create(kind="k", dedup_key="d")


def test_empty_dedup_key_never_collides():
    tasks.register("test.ok")(lambda payload: None)
    tasks.enqueue("test.ok", {})
    tasks.enqueue("test.ok", {})
    assert DeferredTask.objects.count() == 2


# --- run --------------------------------------------------------------------------------------


def test_run_executes_handler_and_marks_done():
    seen = []
    tasks.register("test.capture")(lambda payload: seen.append(payload))
    task = tasks.enqueue("test.capture", {"x": 1})

    summary = tasks.run_pending_tasks()

    assert summary == {"claimed": 1, "done": 1, "retried": 0, "failed": 0}
    assert seen == [{"x": 1}]
    task.refresh_from_db()
    assert task.status == DeferredTask.Status.DONE
    assert task.attempts == 1
    assert task.finished_at is not None


def test_handler_receives_a_payload_copy_not_the_stored_dict():
    """A handler mutating its payload must not corrupt the persisted row."""

    @tasks.register("test.mutate")
    def _h(payload):
        payload["injected"] = True

    task = tasks.enqueue("test.mutate", {"a": 1})
    tasks.run_pending_tasks()
    task.refresh_from_db()
    assert task.payload == {"a": 1}  # unchanged


def test_handler_db_writes_roll_back_on_failure():
    """The savepoint around the handler rolls back its own DB writes when it raises, while the
    failure is still recorded on the task."""

    @tasks.register("test.write_then_fail")
    def _h(payload):
        DeferredTask.objects.create(kind="test.sentinel", payload={})
        raise RuntimeError("boom after write")

    task = tasks.enqueue("test.write_then_fail", {}, max_attempts=1)
    tasks.run_pending_tasks()

    task.refresh_from_db()
    assert task.status == DeferredTask.Status.FAILED
    # The handler's sentinel write was rolled back by the savepoint.
    assert DeferredTask.objects.filter(kind="test.sentinel").count() == 0


@override_settings(DEFERRED_TASKS_BACKOFF_BASE=0)
def test_failure_retries_then_exhausts_to_failed():
    calls = []

    @tasks.register("test.always_fail")
    def _h(payload):
        calls.append(1)
        raise RuntimeError("nope")

    task = tasks.enqueue("test.always_fail", {}, max_attempts=2)
    # Backoff is 0 so the retried task is immediately due again — one drain pass exhausts it.
    summary = tasks.run_pending_tasks()

    assert len(calls) == 2
    assert summary == {"claimed": 2, "done": 0, "retried": 1, "failed": 1}
    task.refresh_from_db()
    assert task.status == DeferredTask.Status.FAILED
    assert task.attempts == 2
    assert "RuntimeError" in task.last_error


def test_backoff_defers_the_retry_into_the_future():
    @tasks.register("test.always_fail")
    def _h(payload):
        raise RuntimeError("nope")

    task = tasks.enqueue("test.always_fail", {}, max_attempts=3)
    before = timezone.now()
    summary = tasks.run_pending_tasks()
    # Default backoff base (30s) pushes the next attempt out, so it is NOT re-run this pass.
    assert summary == {"claimed": 1, "done": 0, "retried": 1, "failed": 0}
    task.refresh_from_db()
    assert task.status == DeferredTask.Status.PENDING
    assert task.attempts == 1
    assert task.available_at > before


def test_missing_handler_is_a_bounded_failure_not_an_infinite_loop():
    tasks.register("test.vanishes")(lambda payload: None)
    task = tasks.enqueue("test.vanishes", {}, max_attempts=1)
    # Simulate a de-registered / not-yet-imported handler at drain time.
    tasks._REGISTRY.pop("test.vanishes")

    summary = tasks.run_pending_tasks()

    assert summary["failed"] == 1
    task.refresh_from_db()
    assert task.status == DeferredTask.Status.FAILED
    assert "no handler" in task.last_error.lower()


def test_not_yet_due_tasks_are_skipped():
    tasks.register("test.ok")(lambda payload: None)
    tasks.enqueue("test.ok", {}, delay=timezone.timedelta(hours=1))
    summary = tasks.run_pending_tasks()
    assert summary == {"claimed": 0, "done": 0, "retried": 0, "failed": 0}


def test_tasks_run_oldest_available_first():
    order = []
    tasks.register("test.order")(lambda payload: order.append(payload["n"]))
    now = timezone.now()
    tasks.enqueue("test.order", {"n": "b"}, available_at=now - timezone.timedelta(seconds=5))
    tasks.enqueue("test.order", {"n": "a"}, available_at=now - timezone.timedelta(seconds=10))

    tasks.run_pending_tasks()
    assert order == ["a", "b"]


def test_limit_bounds_the_batch():
    tasks.register("test.ok")(lambda payload: None)
    for _ in range(3):
        tasks.enqueue("test.ok", {})
    summary = tasks.run_pending_tasks(limit=2)
    assert summary["claimed"] == 2
    assert DeferredTask.objects.filter(status=DeferredTask.Status.PENDING).count() == 1


# --- registry ---------------------------------------------------------------------------------


def test_duplicate_registration_raises_but_same_callable_is_noop():
    fn = lambda payload: None  # noqa: E731
    tasks.register("test.dup")(fn)
    tasks.register("test.dup")(fn)  # same callable -> tolerated
    with pytest.raises(ValueError):
        tasks.register("test.dup")(lambda payload: None)  # different callable -> error


def test_production_registry_has_reviewed_handlers():
    """Production handlers are explicit and reviewed; adding a kind changes this allowlist."""
    from apps.ops import handlers  # noqa: F401  (import triggers any @register)

    assert tasks.registered_kinds() == frozenset(
        {
            "cron.run_command",
            "erasure.blob_cleanup",
            "media.scan.dispatch",
            "notify.activity_fanout",
            "notifications.retention_purge",
        }
    )


def test_blob_cleanup_handler_deletes_storage_key(settings, tmp_path):
    from apps.media.storage import get_storage
    from apps.ops import handlers  # noqa: F401

    settings.MEDIA_ROOT = tmp_path / "media"
    key = "deferred-cleanup.txt"
    get_storage().save(key, b"old bytes", content_type="text/plain")
    assert get_storage().exists(key) is True

    tasks.enqueue("erasure.blob_cleanup", {"blob_keys": [key]}, dedup_key=f"blob:{key}")
    assert tasks.run_pending_tasks()["done"] == 1

    assert get_storage().exists(key) is False


def test_media_scan_dispatch_fails_closed_and_audits():
    from apps.ops import handlers  # noqa: F401
    from apps.safety.models import AuditLog

    tasks.enqueue("media.scan.dispatch", {"attachment_id": 123}, max_attempts=1)
    summary = tasks.run_pending_tasks()

    assert summary["done"] == 1
    assert AuditLog.objects.filter(event="media.scan_dispatch_blocked").exists()


def test_notify_activity_fanout_handler_is_bounded_and_idempotent():
    from django.contrib.gis.geos import Point

    from apps.accounts.identity.base import AssuranceResult
    from apps.accounts.models import AgeBand, User
    from apps.accounts.services import apply_assurance
    from apps.notifications.models import Notification
    from apps.ops import handlers  # noqa: F401
    from apps.places.models import Place
    from apps.social.models import Membership
    from apps.social.services import create_activity
    from apps.taxonomy.models import ActivityCategory, ActivityType

    owner = User.objects.create_user(username="fanout_owner", password="pw", display_name="Owner")
    member = User.objects.create_user(
        username="fanout_member", password="pw", display_name="Member"
    )
    apply_assurance(owner, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    apply_assurance(member, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    place = Place.objects.create(
        name="Fanout Place", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    category = ActivityCategory.objects.create(slug="fanout-cat", name="Fanout")
    activity_type = ActivityType.objects.create(
        slug="fanout-type", name="Fanout Type", category=category
    )

    activity = create_activity(
        owner,
        place=place,
        activity_type=activity_type,
        title="Fanout",
        starts_at=timezone.now() + timezone.timedelta(days=1),
    )
    Membership.objects.create(
        activity=activity,
        user=member,
        role=Membership.Role.MEMBER,
        state=Membership.State.MEMBER,
        decided_at=timezone.now(),
    )
    payload = {
        "activity_id": activity.id,
        "exclude_user_id": owner.id,
        "kind": Notification.Kind.ANNOUNCEMENT,
        "title": "Announcement: Fanout",
        "body": "Bring water",
        "url": f"/api/social/activities/{activity.id}/",
    }
    tasks.enqueue("notify.activity_fanout", payload, dedup_key=f"notify:{activity.id}")
    tasks.run_pending_tasks()
    tasks.enqueue("notify.activity_fanout", payload, dedup_key=f"notify:{activity.id}:again")
    tasks.run_pending_tasks()

    assert (
        Notification.objects.filter(
            recipient=member, kind=Notification.Kind.ANNOUNCEMENT, title="Announcement: Fanout"
        ).count()
        == 1
    )


@override_settings(NOTIFICATION_RETENTION_DAYS=180, NOTIFICATION_RETENTION_BATCH=1)
def test_notification_retention_handler_keeps_unread_and_dsa_notices():
    from datetime import timedelta

    from apps.accounts.models import User
    from apps.notifications.models import Notification
    from apps.ops import handlers  # noqa: F401

    user = User.objects.create_user(username="deferred_retention", password="pw")

    def _notification(kind, *, read):
        row = Notification.objects.create(
            recipient=user,
            kind=kind,
            title="old",
            read_at=timezone.now() if read else None,
        )
        Notification.objects.filter(pk=row.pk).update(
            created_at=timezone.now() - timedelta(days=400)
        )
        return row

    purgeable = _notification(Notification.Kind.JOIN_APPROVED, read=True)
    unread = _notification(Notification.Kind.JOIN_APPROVED, read=False)
    system = _notification(Notification.Kind.SYSTEM, read=True)

    tasks.enqueue("notifications.retention_purge", {"days": 180, "batch_size": 99})
    assert tasks.run_pending_tasks()["done"] == 1

    assert not Notification.objects.filter(pk=purgeable.pk).exists()
    assert Notification.objects.filter(pk=unread.pk).exists()
    assert Notification.objects.filter(pk=system.pk).exists()


def test_cron_run_command_handler_allowlists_due_jobs(monkeypatch):
    from apps.ops import handlers  # noqa: F401

    called = []
    monkeypatch.setattr(
        "apps.ops.handlers.call_command", lambda name, **kwargs: called.append(name)
    )

    tasks.enqueue("cron.run_command", {"command": "expire_api_tokens"})
    tasks.run_pending_tasks()

    assert called == ["expire_api_tokens"]


def test_cron_run_command_handler_rejects_unknown_command():
    from apps.ops import handlers  # noqa: F401

    tasks.enqueue("cron.run_command", {"command": "shell"}, max_attempts=1)
    summary = tasks.run_pending_tasks()

    assert summary["failed"] == 1


# --- command + cron wiring --------------------------------------------------------------------


def test_management_command_drains_the_queue():
    seen = []
    tasks.register("test.capture")(lambda payload: seen.append(payload))
    tasks.enqueue("test.capture", {"x": 1})

    out = StringIO()
    call_command("process_deferred_tasks", stdout=out, stderr=StringIO())

    assert seen == [{"x": 1}]
    assert "done=1" in out.getvalue()


def test_command_reports_failures_on_stderr_without_raising():
    @tasks.register("test.always_fail")
    def _h(payload):
        raise RuntimeError("nope")

    tasks.enqueue("test.always_fail", {}, max_attempts=1)
    err = StringIO()
    call_command("process_deferred_tasks", stdout=StringIO(), stderr=err)
    assert "failed=1" in err.getvalue()


def test_process_deferred_tasks_is_wired_into_run_due_jobs():
    import apps.ops.management.commands.run_due_jobs as rdj

    assert "process_deferred_tasks" in {name for name, _ in rdj.DUE_JOBS}
