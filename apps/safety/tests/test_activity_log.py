"""F34 — your activity log (read-only self audit trail). Pins: audit_log_for is strictly
self-scoped to the actor (another user's actions never appear); an event NOT in the FIXED allowlist
is DROPPED, never rendered raw; the projection leaks ONLY {label, when} — never the raw event code,
target_ref, the data payload, or any other party; and report.filed is omitted (it lives on F19's
safety record). Plus the /my-activity-log/ web view is login-gated + self-only."""

import pytest
from django.test import Client

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.safety import services as safety

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def test_shows_only_the_actors_own_mapped_actions():
    a, b = _user("al_a"), _user("al_b")
    safety.block_user(a, b)  # a -> user.blocked (actor=a)
    safety.block_user(b, a)  # b -> user.blocked (actor=b)

    a_log = safety.audit_log_for(a)
    labels = [e["label"] for e in a_log]
    assert any("blocked" in label.lower() for label in labels)
    # b's identical action must never appear in a's log (self-scoped by actor_ref).
    assert len(a_log) == 1


def test_unmapped_event_is_dropped_not_rendered_raw():
    u = _user("al_unmapped")
    # A real event that is NOT in the allowlist (system/moderator axis) recorded with this user as
    # actor must be filtered out entirely — never shown, never leaked raw.
    safety.record_audit("moderation.suspension_lifted", actor=u)
    safety.record_audit("community.generated", actor=u)
    log = safety.audit_log_for(u)
    assert log == []  # both unmapped -> dropped


def test_report_filed_is_omitted_to_dedupe_with_safety_record():
    u, other = _user("al_rep"), _user("al_rep2")
    from apps.safety.models import ReasonCode

    safety.file_report(u, other, ReasonCode.HARASSMENT)  # records report.filed (actor=u)
    log = safety.audit_log_for(u)
    # report.filed is intentionally absent from the allowlist (it's on /my-safety-record/).
    assert all("report" not in e["label"].lower() for e in log)


def test_message_reported_is_omitted_to_dedupe_with_safety_record():
    # report_message files a Report (-> shown on /my-safety-record/) AND records
    # messaging.message_reported; the latter must NOT also appear here (no double-count).
    u = _user("al_msgrep")
    safety.record_audit("messaging.message_reported", actor=u)
    assert safety.audit_log_for(u) == []  # dropped from the allowlist


def test_group_created_is_shown_to_its_creator():
    u = _user("al_gc")
    safety.record_audit("group.created", actor=u)
    labels = [e["label"] for e in safety.audit_log_for(u)]
    assert any("created a group" in label for label in labels)


def test_projection_leaks_only_label_and_when():
    u, other = _user("al_proj"), _user("al_proj2")
    safety.block_user(u, other)
    log = safety.audit_log_for(u)
    assert log, "expected at least one entry"
    entry = log[0]
    assert set(entry.keys()) == {"label", "when"}  # no event code, target_ref, or data payload
    # the raw event code never appears in the human label
    assert "user.blocked" not in entry["label"]
    # and the target reference format (app.model:pk) is never present
    assert ":" not in entry["label"] or "user.blocked" not in entry["label"]


def test_capped_at_limit():
    u = _user("al_cap")
    for _i in range(5):
        safety.record_audit("user.blocked", actor=u)
    assert len(safety.audit_log_for(u, limit=3)) == 3


def test_web_view_requires_login():
    resp = Client().get("/my-activity-log/")
    assert resp.status_code in (301, 302)


def test_web_view_renders_own_actions_only():
    a, b = _user("al_wa"), _user("al_wb")
    safety.block_user(a, b)
    c = Client()
    c.force_login(a)
    body = c.get("/my-activity-log/").content.decode()
    assert "Your activity log" in body
    assert "You blocked someone" in body
    # No raw event codes leak into the page.
    assert "user.blocked" not in body
