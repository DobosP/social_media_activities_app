"""ADR-0019 §7 — the daily sync_roedu due-job: opt-in guard + sub-command fan-out."""

from io import StringIO
from unittest.mock import patch

import pytest
from django.core.management import call_command

pytestmark = pytest.mark.django_db


def _run(**env):
    out = StringIO()
    call_command("sync_roedu", stdout=out)
    return out.getvalue()


def test_skips_quietly_when_disabled(settings):
    settings.ROEDU_SYNC_ENABLED = False
    with patch("apps.ingestion.management.commands.sync_roedu.call_command") as sub:
        output = _run()
    sub.assert_not_called()
    assert "skipped" in output


def test_skips_quietly_without_api_key(settings, monkeypatch):
    settings.ROEDU_SYNC_ENABLED = True
    monkeypatch.delenv("ROEDU_API_KEY", raising=False)
    with patch("apps.ingestion.management.commands.sync_roedu.call_command") as sub:
        output = _run()
    sub.assert_not_called()
    assert "skipped" in output


def test_runs_all_three_stages_when_enabled(settings, monkeypatch):
    settings.ROEDU_SYNC_ENABLED = True
    settings.ROEDU_SYNC_CITY = "Cluj-Napoca"
    monkeypatch.setenv("ROEDU_API_KEY", "test-key")
    monkeypatch.delenv("ROEDU_APP_PACK", raising=False)
    with patch("apps.ingestion.management.commands.sync_roedu.call_command") as sub:
        output = _run()
    names = [call.args[0] for call in sub.call_args_list]
    assert names == ["ingest_places", "sync_roedu_events", "resolve_place_covers"]
    assert "completed for Cluj-Napoca" in output


def test_app_pack_mode_is_forwarded_to_event_sync_for_one_mode_per_run(settings, monkeypatch):
    settings.ROEDU_SYNC_ENABLED = True
    settings.ROEDU_SYNC_CITY = "Cluj-Napoca"
    monkeypatch.setenv("ROEDU_API_KEY", "test-key")
    monkeypatch.setenv("ROEDU_APP_PACK", "events_places")
    with patch("apps.ingestion.management.commands.sync_roedu.call_command") as sub:
        _run()
    event_call = sub.call_args_list[1]
    assert event_call.args == (
        "sync_roedu_events",
        "--city",
        "Cluj-Napoca",
        "--app-pack",
        "events_places",
    )


def test_registered_in_due_jobs():
    from apps.ops.management.commands.run_due_jobs import DUE_JOBS

    assert "sync_roedu" in {name for name, _ in DUE_JOBS}
