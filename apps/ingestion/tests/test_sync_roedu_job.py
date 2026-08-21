"""ADR-0019 §7 — the daily sync_roedu due-job: opt-in guard + sub-command fan-out."""

from io import StringIO
from pathlib import Path
from unittest.mock import patch

import pytest
from django.core.management import call_command
from django.core.management.base import CommandError

from apps.ingestion.sources.roedu_client import (
    SOCIAL_APP_PACK_ID,
    RoeduProductUnavailable,
)

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
    monkeypatch.setenv("ROEDU_APP_PACK", SOCIAL_APP_PACK_ID)
    with patch("apps.ingestion.management.commands.sync_roedu.call_command") as sub:
        _run()
    event_call = sub.call_args_list[1]
    # The credential is forwarded explicitly. This assertion previously pinned
    # the argv WITHOUT --api-key, which is what let the events lane fall back to
    # its own hard-coded "social-app-dev" default while the venues lane of the
    # same nightly job sent the real ROEDU_API_KEY: one job, two clients, and a
    # green test over the top of it.
    assert event_call.args == (
        "sync_roedu_events",
        "--city",
        "Cluj-Napoca",
        "--api-key",
        "test-key",
        "--app-pack",
        SOCIAL_APP_PACK_ID,
    )


def test_invalid_pack_is_rejected_before_any_stage_writes(settings, monkeypatch):
    settings.ROEDU_SYNC_ENABLED = True
    monkeypatch.setenv("ROEDU_API_KEY", "test-key")
    monkeypatch.setenv("ROEDU_APP_PACK", "events_places")
    with (
        patch("apps.ingestion.management.commands.sync_roedu.call_command") as sub,
        pytest.raises(CommandError, match="canonical"),
    ):
        _run()
    sub.assert_not_called()


def test_registered_in_due_jobs():
    from apps.ops.management.commands.run_due_jobs import DUE_JOBS

    assert "sync_roedu" in {name for name, _ in DUE_JOBS}


@pytest.mark.django_db
def test_a_refused_source_is_loud_but_does_not_red_line_the_shared_tick(monkeypatch, settings):
    """RO-EDU is opt-in and external; the tick it shares carries the GDPR/DSA duties.

    `run_due_jobs` pings its heartbeat only when EVERY job succeeded, so failing here
    would withhold the compliance heartbeat nightly for a server-side condition outside
    this app's control — and `resolve_place_covers` is city-scoped, so it must keep
    running for OSM-sourced places.
    """
    settings.ROEDU_SYNC_ENABLED = True
    monkeypatch.setenv("ROEDU_API_KEY", "social-app-dev")
    monkeypatch.delenv("ROEDU_APP_PACK", raising=False)
    calls = []

    def fake_call_command(name, *args, **kwargs):
        calls.append(name)
        if name == "ingest_places":
            refusal = RoeduProductUnavailable("venues", note="schema not ready")
            raise CommandError(str(refusal)) from refusal

    monkeypatch.setattr(
        "apps.ingestion.management.commands.sync_roedu.call_command", fake_call_command
    )
    err, out = StringIO(), StringIO()
    call_command("sync_roedu", stderr=err, stdout=out)

    assert "resolve_place_covers" in calls, "covers must still run after an RO-EDU refusal"
    assert "RO-EDU source failed" in err.getvalue()
    assert "schema not ready" in err.getvalue()
    assert "NOT refreshed" in out.getvalue()


@pytest.mark.django_db
def test_a_failure_that_is_not_a_refusal_still_fails_the_job(monkeypatch, settings):
    """The isolation is for the SOURCE refusing — not for this app's own breakage.

    Without this, `except CommandError` would swallow a missing seed, a stale-snapshot
    guard or a contract breach behind a green tick: the silent zero, one level up.
    """
    settings.ROEDU_SYNC_ENABLED = True
    monkeypatch.setenv("ROEDU_API_KEY", "social-app-dev")
    monkeypatch.delenv("ROEDU_APP_PACK", raising=False)
    calls = []

    def fake_call_command(name, *args, **kwargs):
        calls.append(name)
        if name == "ingest_places":
            raise CommandError("No ActivityType rows found. Run migrations (seed) first.")

    monkeypatch.setattr(
        "apps.ingestion.management.commands.sync_roedu.call_command", fake_call_command
    )
    with pytest.raises(CommandError, match="No ActivityType rows"):
        call_command("sync_roedu")
    assert "resolve_place_covers" not in calls


def test_both_lanes_use_the_same_credential(settings, monkeypatch):
    """One nightly job must speak to the producer as ONE client.

    The venues lane reads ROEDU_API_KEY from the environment; the events lane
    is a separate management command with its own --api-key. Before the fix it
    had a "social-app-dev" default and sync_roedu never passed the flag, so the
    two halves authenticated differently and nothing reported it.
    """

    settings.ROEDU_SYNC_ENABLED = True
    settings.ROEDU_SYNC_CITY = "Cluj-Napoca"
    monkeypatch.setenv("ROEDU_API_KEY", "PROD-SECRET")
    monkeypatch.delenv("ROEDU_APP_PACK", raising=False)
    with patch("apps.ingestion.management.commands.sync_roedu.call_command") as sub:
        _run()
    event_call = sub.call_args_list[1]
    assert "--api-key" in event_call.args
    forwarded = event_call.args[event_call.args.index("--api-key") + 1]
    assert forwarded == "PROD-SECRET"
    assert "social-app-dev" not in event_call.args


def test_the_events_lane_has_no_dev_credential_fallback():
    """A missing credential must fail, not silently authenticate as dev."""

    from apps.events.management.commands import sync_roedu_events

    source = Path(sync_roedu_events.__file__).read_text(encoding="utf-8")
    # The FALLBACK must be gone, not every mention: the comments explaining why
    # deliberately name the old default.
    assert 'default="social-app-dev"' not in source
    assert "default=None" in source


def test_the_ingestion_adapter_has_no_dev_credential_fallback():
    from apps.ingestion.sources import ro_scraper

    source = Path(ro_scraper.__file__).read_text(encoding="utf-8")
    assert '"ROEDU_API_KEY", "social-app-dev"' not in source
    assert 'os.environ.get("ROEDU_API_KEY")' in source
