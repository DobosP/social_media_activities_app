"""Regression tests for DSA Art. 16 reporter notifications, the report-target
visibility gate, admin bulk-dismiss routing through the service, and the moderation
queue's hard cap."""

import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Role, User
from apps.accounts.services import apply_assurance
from apps.notifications.models import Notification
from apps.places.models import Place
from apps.safety.models import ModerationAction, ReasonCode, Report
from apps.safety.services import dismiss_report, file_report, take_action
from apps.social.services import create_activity, post_to_thread
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name, band=AgeBand.ADULT, role=Role.USER):
    u = User.objects.create_user(username=name, password="pw", display_name=name, role=role)
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


_slug_seq = iter(range(1000))


def _activity(owner, title="Game"):
    n = next(_slug_seq)
    place = Place.objects.create(
        name="Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    cat = ActivityCategory.objects.create(slug=f"d16-{n}", name="Sport")
    atype = ActivityType.objects.create(slug=f"d16-{n}-bball", name="Basketball", category=cat)
    return create_activity(
        owner, place=place, activity_type=atype, title=title, starts_at="2026-06-01T10:00Z"
    )


# --- DSA Art. 16: notify the reporter -------------------------------------------------


def test_reporter_notified_on_file_report():
    reporter, target = _user("rep_a"), _user("tgt_a")
    file_report(reporter, target, ReasonCode.HARASSMENT)
    note = Notification.objects.filter(recipient=reporter, kind=Notification.Kind.SYSTEM).first()
    assert note is not None
    assert "report" in note.title.lower()


def test_anonymous_report_does_not_crash_and_creates_no_notification():
    target = _user("tgt_anon")
    file_report(None, target, ReasonCode.SPAM)
    assert Notification.objects.count() == 0


def test_reporter_notified_on_take_action():
    reporter, offender, mod = _user("rep_b"), _user("off_b"), _user("mod_b", role=Role.MODERATOR)
    report = file_report(reporter, offender, ReasonCode.HARASSMENT)
    Notification.objects.all().delete()  # drop the acknowledgement so we isolate the outcome
    take_action(mod, offender, ModerationAction.Action.BAN, ReasonCode.HARASSMENT, report=report)
    outcome = Notification.objects.filter(recipient=reporter)
    assert outcome.count() == 1
    assert "reviewed" in outcome.first().title.lower()


def test_reporter_notified_on_dismiss():
    reporter, target, mod = _user("rep_c"), _user("tgt_c"), _user("mod_c", role=Role.MODERATOR)
    report = file_report(reporter, target, ReasonCode.SPAM)
    Notification.objects.all().delete()
    dismiss_report(mod, report, "not a violation")
    outcome = Notification.objects.filter(recipient=reporter)
    assert outcome.count() == 1
    assert "reviewed" in outcome.first().title.lower()


def test_take_action_notifies_affected_user_even_without_report():
    # DSA Art.17: the AFFECTED user always receives a statement of reasons (even with no
    # report). With no report there is simply no reporter to acknowledge (Art.16).
    offender, mod = _user("off_d"), _user("mod_d", role=Role.MODERATOR)
    take_action(mod, offender, ModerationAction.Action.WARN, ReasonCode.OTHER)
    assert (
        Notification.objects.filter(recipient=offender, kind=Notification.Kind.MODERATION).count()
        == 1
    )
    assert Notification.objects.count() == 1  # only the statement of reasons, no reporter ack


# --- ReportView visibility gate (no existence leak) -----------------------------------


def test_api_report_user_target_allowed():
    reporter, offender = _user("v_rep"), _user("v_off")
    client = APIClient()
    client.force_authenticate(reporter)
    resp = client.post(
        "/api/safety/reports/",
        {"target_type": "user", "target_id": offender.id, "reason": ReasonCode.HARASSMENT},
        format="json",
    )
    assert resp.status_code == 201, resp.content


def test_api_report_unknown_target_returns_404():
    reporter = _user("v_rep2")
    client = APIClient()
    client.force_authenticate(reporter)
    resp = client.post(
        "/api/safety/reports/",
        {"target_type": "activity", "target_id": 999999, "reason": ReasonCode.SPAM},
        format="json",
    )
    assert resp.status_code == 404
    assert Report.objects.count() == 0


def test_api_report_invisible_activity_returns_404_not_leaking_existence():
    # Owner is a TEEN; reporter is an ADULT in a different cohort, so cannot see it.
    owner = _user("v_owner", band=AgeBand.AGE_16_17)
    activity = _activity(owner)
    outsider = _user("v_outsider", band=AgeBand.ADULT)
    client = APIClient()
    client.force_authenticate(outsider)
    resp = client.post(
        "/api/safety/reports/",
        {"target_type": "activity", "target_id": activity.id, "reason": ReasonCode.SPAM},
        format="json",
    )
    assert resp.status_code == 404
    assert Report.objects.count() == 0


def test_api_report_visible_activity_allowed():
    owner = _user("v_owner2", band=AgeBand.ADULT)
    activity = _activity(owner)
    peer = _user("v_peer", band=AgeBand.ADULT)  # same cohort as owner
    client = APIClient()
    client.force_authenticate(peer)
    resp = client.post(
        "/api/safety/reports/",
        {"target_type": "activity", "target_id": activity.id, "reason": ReasonCode.SPAM},
        format="json",
    )
    assert resp.status_code == 201, resp.content


def test_api_report_post_gated_by_activity_visibility():
    owner = _user("v_powner", band=AgeBand.AGE_16_17)
    activity = _activity(owner)
    post = post_to_thread(owner, activity, "hi")
    outsider = _user("v_poutsider", band=AgeBand.ADULT)
    client = APIClient()
    client.force_authenticate(outsider)
    resp = client.post(
        "/api/safety/reports/",
        {"target_type": "post", "target_id": post.id, "reason": ReasonCode.OTHER},
        format="json",
    )
    assert resp.status_code == 404
    assert Report.objects.count() == 0


# --- Admin bulk dismiss routes through the service ------------------------------------


def test_admin_dismiss_action_uses_service_and_notifies():
    from apps.safety.admin import ReportAdmin
    from apps.safety.models import AuditLog

    site_admin = ReportAdmin(Report, admin_site=None)
    mod = _user("admin_mod", role=Role.MODERATOR)
    reporter = _user("admin_rep")
    report = file_report(reporter, _user("admin_tgt"), ReasonCode.SPAM)
    Notification.objects.all().delete()

    class _Req:
        user = mod

    request = _Req()
    # message_user writes to the messages framework; bypass it for the unit test.
    site_admin.message_user = lambda *a, **k: None
    site_admin.dismiss(request, Report.objects.filter(pk=report.pk))

    report.refresh_from_db()
    assert report.status == Report.Status.DISMISSED
    assert report.handled_by == mod
    # The service emits an audit row and the reporter notification (vs. a bare .update()).
    assert AuditLog.objects.filter(event="report.dismissed").count() == 1
    assert Notification.objects.filter(recipient=reporter).count() == 1


# --- Moderation queue hard cap --------------------------------------------------------


def test_moderation_queue_is_capped_at_100():
    from django.contrib.contenttypes.models import ContentType

    mod = _user("cap_mod", role=Role.MODERATOR)
    target = _user("cap_tgt")
    reporter = _user("cap_rep")
    ct = ContentType.objects.get_for_model(target)
    Report.objects.bulk_create(
        [
            Report(
                reporter=reporter,
                target_type=ct,
                target_id=target.id,
                reason=ReasonCode.SPAM,
            )
            for _ in range(105)
        ]
    )
    client = APIClient()
    client.force_authenticate(mod)
    resp = client.get("/api/safety/moderation/reports/")
    assert resp.status_code == 200
    assert len(resp.json()) == 100
