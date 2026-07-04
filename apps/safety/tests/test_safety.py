import pytest
from django.contrib.gis.geos import Point

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import AuditLog, Block, ModerationAction, ReasonCode, Report
from apps.safety.services import (
    allow_action,
    block_user,
    file_report,
    is_blocked,
    take_action,
    unblock_user,
    verified_audit_checkpoint,
    verify_audit_chain,
)

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def test_file_report_creates_record_and_audit():
    reporter, target = _user("r1"), _user("t1")
    report = file_report(reporter, target, ReasonCode.HARASSMENT, "was rude")
    assert report.status == Report.Status.OPEN
    assert report.target == target
    assert AuditLog.objects.filter(event="report.filed").count() == 1


def test_block_is_symmetric_and_reversible():
    a, b = _user("a"), _user("b")
    block_user(a, b)
    assert is_blocked(a, b) is True
    assert is_blocked(b, a) is True  # suppressed both directions
    unblock_user(a, b)
    assert is_blocked(a, b) is False


def test_cannot_block_self():
    a = _user("solo")
    with pytest.raises(ValueError):
        block_user(a, a)


def test_block_pair_unique():
    a, b = _user("a2"), _user("b2")
    block_user(a, b)
    block_user(a, b)
    assert Block.objects.filter(blocker=a, blocked=b).count() == 1


def test_ban_deactivates_account_and_resolves_report():
    mod, offender = _user("mod"), _user("bad")
    report = file_report(_user("victim"), offender, ReasonCode.GROOMING)
    action = take_action(
        mod, offender, ModerationAction.Action.BAN, ReasonCode.GROOMING, report=report
    )
    offender.refresh_from_db()
    report.refresh_from_db()
    assert action.action == ModerationAction.Action.BAN
    assert offender.is_active is False
    assert report.status == Report.Status.ACTIONED
    assert report.handled_by == mod


def test_audit_chain_verifies_and_detects_tampering():
    a, b = _user("c1"), _user("c2")
    file_report(a, b, ReasonCode.SPAM)
    block_user(a, b)
    assert verify_audit_chain() is True

    # Tamper with a historical row.
    row = AuditLog.objects.order_by("id").first()
    row.data = {"event": "altered"}
    row.save(update_fields=["data"])
    assert verify_audit_chain() is False


def test_audit_chain_checkpoint_verifies_append_only_extension():
    a, b = _user("cp1"), _user("cp2")
    file_report(a, b, ReasonCode.SPAM)
    checkpoint = verified_audit_checkpoint()
    assert checkpoint is not None
    assert checkpoint.last_id == AuditLog.objects.latest("id").id

    block_user(a, b)
    assert verify_audit_chain(checkpoint=checkpoint) is True

    row = AuditLog.objects.get(pk=checkpoint.last_id)
    row.hash = "0" * 64
    row.save(update_fields=["hash"])
    assert verify_audit_chain(checkpoint=checkpoint) is False


def test_empty_audit_checkpoint_can_seed_incremental_verification():
    checkpoint = verified_audit_checkpoint()
    assert checkpoint is not None
    assert checkpoint.last_id == 0
    assert checkpoint.last_hash == ""
    assert verify_audit_chain(checkpoint=checkpoint) is True


def test_audit_checkpoint_returns_none_on_tamper():
    a, b = _user("cp_bad1"), _user("cp_bad2")
    file_report(a, b, ReasonCode.SPAM)
    row = AuditLog.objects.order_by("id").first()
    row.data = {"event": "altered"}
    row.save(update_fields=["data"])

    assert verified_audit_checkpoint() is None


def test_rate_limiter_blocks_over_limit():
    u = _user("spammer")
    assert allow_action(u, "report", limit=2, window_seconds=60) is True
    assert allow_action(u, "report", limit=2, window_seconds=60) is True
    assert allow_action(u, "report", limit=2, window_seconds=60) is False


def test_report_api_flow():
    from rest_framework.test import APIClient

    reporter = _user("apirep")
    offender = _user("apibad")
    client = APIClient()
    client.force_authenticate(reporter)
    resp = client.post(
        "/api/safety/reports/",
        {"target_type": "user", "target_id": offender.id, "reason": ReasonCode.HARASSMENT},
        format="json",
    )
    assert resp.status_code == 201, resp.content
    assert Report.objects.filter(reporter=reporter).count() == 1


def test_report_on_post_target(db):
    from apps.social.services import create_activity, post_to_thread
    from apps.taxonomy.models import ActivityCategory, ActivityType

    owner = _user("owner")
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug="s4", name="Sport")
    atype = ActivityType.objects.create(slug="bball4", name="Basketball", category=cat)
    activity = create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at="2026-06-01T10:00Z"
    )
    post = post_to_thread(owner, activity, "hello")
    report = file_report(_user("rep2"), post, ReasonCode.OTHER)
    assert report.target == post
