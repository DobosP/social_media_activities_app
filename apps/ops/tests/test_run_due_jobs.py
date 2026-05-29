"""The run_due_jobs orchestrator: one cron entry point that fans out to the existing
maintenance commands. Verifies it invokes every job, isolates per-job failures (one bad
job does not skip the rest), threads the reminder window, and signals overall failure."""

from io import StringIO
from unittest import mock

import pytest
from django.core.management import call_command
from django.core.management.base import CommandError

import apps.ops.management.commands.run_due_jobs as run_due_jobs

pytestmark = pytest.mark.django_db

ALL_JOBS = {name for name, _ in run_due_jobs.DUE_JOBS}


def test_runs_against_empty_db_without_error():
    out = StringIO()
    call_command("run_due_jobs", stdout=out, stderr=StringIO())
    assert "All" in out.getvalue()


def test_invokes_every_due_job():
    with mock.patch.object(run_due_jobs, "call_command") as called:
        call_command("run_due_jobs", stdout=StringIO(), stderr=StringIO())
    invoked = {c.args[0] for c in called.call_args_list}
    assert invoked == ALL_JOBS


def test_threads_reminder_window_to_reminders_job():
    with mock.patch.object(run_due_jobs, "call_command") as called:
        call_command(
            "run_due_jobs",
            reminder_within_hours=6,
            stdout=StringIO(),
            stderr=StringIO(),
        )
    reminder_calls = [c for c in called.call_args_list if c.args[0] == "send_activity_reminders"]
    assert reminder_calls and reminder_calls[0].kwargs.get("within_hours") == 6


def test_one_failing_job_does_not_skip_the_rest_and_signals_failure():
    def fake_call(name, **kwargs):
        if name == "purge_chat":
            raise RuntimeError("boom")

    with mock.patch.object(run_due_jobs, "call_command", side_effect=fake_call) as called:
        with pytest.raises(CommandError):
            call_command("run_due_jobs", stdout=StringIO(), stderr=StringIO())
    # All jobs were still attempted despite purge_chat failing.
    invoked = {c.args[0] for c in called.call_args_list}
    assert invoked == ALL_JOBS
