"""F11 — moderation triage hints: triage_summary / triage_order + the queue view ordering,
audited access, and the privacy guardrails (staff-only, no per-user rollup, CHILD as a bare bool).
"""

import pytest
from django.contrib.gis.geos import Point
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, Role, User
from apps.accounts.services import apply_assurance
from apps.places.models import Place
from apps.safety.models import AuditLog, ReasonCode, Report
from apps.safety.services import file_report, triage_order, triage_summary
from apps.social.models import Post
from apps.social.services import create_activity
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PT = Point(23.6, 46.77, srid=4326)


def _user(name, role=Role.USER):
    u = User.objects.create_user(username=name, password="pw", display_name=name, role=role)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def test_severity_and_child_signals():
    grooming_child = file_report(_user("r1"), _child("c1"), ReasonCode.GROOMING)
    spam_adult = file_report(_user("r2"), _user("a1"), ReasonCode.SPAM)
    g = triage_summary(grooming_child)
    s = triage_summary(spam_adult)
    assert g["severity"] > s["severity"]
    assert g["involves_child"] is True
    assert s["involves_child"] is False
    # CHILD is a derived boolean only — the signal set NEVER carries the age band / DOB.
    assert set(g) == {
        "severity",
        "involves_child",
        "open_duplicates",
        "contact_hint",
        "contact_terms",
    }
    assert "under_16" not in str(g)


def test_open_duplicate_count():
    target = _user("dup_target")
    file_report(_user("rA"), target, ReasonCode.HARASSMENT)
    file_report(_user("rB"), target, ReasonCode.HARASSMENT)
    any_report = Report.objects.filter(target_id=target.id).first()
    assert triage_summary(any_report)["open_duplicates"] == 2


def test_triage_order_most_dangerous_first():
    spam = file_report(_user("r3"), _user("a3"), ReasonCode.SPAM)
    grooming = file_report(_user("r4"), _child("c4"), ReasonCode.GROOMING)
    harassment = file_report(_user("r5"), _user("a5"), ReasonCode.HARASSMENT)
    ordered = triage_order([spam, grooming, harassment])
    assert [r.id for r, _ in ordered] == [grooming.id, harassment.id, spam.id]


def test_contact_hint_only_for_posts():
    owner = _user("po1")
    cat = ActivityCategory.objects.create(slug="cat-t", name="Sport")
    atype = ActivityType.objects.create(slug="at-t", name="Football", category=cat)
    place = Place.objects.create(name="V", location=PT, source=Place.Source.OSM)
    activity = create_activity(
        owner, place=place, activity_type=atype, title="x", starts_at="2030-01-01T10:00Z"
    )
    post = Post.objects.create(
        thread=activity.thread,
        author=owner,
        body="let's move this to whatsapp, my number 0712345678",
    )
    report = file_report(_user("po2"), post, ReasonCode.GROOMING)
    summary = triage_summary(report)
    assert summary["contact_hint"] is True
    assert "whatsapp" in summary["contact_terms"]
    # A user target has no body to scan -> no contact hint.
    assert (
        triage_summary(file_report(_user("po3"), _user("po4"), ReasonCode.SPAM))["contact_hint"]
        is False
    )


def test_queue_view_orders_by_triage_and_audits_access():
    spam = file_report(_user("q1"), _user("q1t"), ReasonCode.SPAM)
    grooming = file_report(_user("q2"), _child("q2c"), ReasonCode.GROOMING)
    moderator = _user("qmod", Role.MODERATOR)
    client = APIClient()
    client.force_authenticate(moderator)
    resp = client.get("/api/safety/moderation/reports/?status=open")
    assert resp.status_code == 200
    rows = resp.json()
    ids = [row["id"] for row in rows]
    assert ids.index(grooming.id) < ids.index(spam.id)  # grooming/child ranked first
    # Triage signals are present in the staff output...
    grow = next(row for row in rows if row["id"] == grooming.id)
    assert grow["triage"]["involves_child"] is True
    # ...and access is audited (DSA accountability), with no report content in the log.
    audit = AuditLog.objects.filter(event="moderation.queue_viewed").latest("id")
    assert audit.actor_ref == moderator.id
    assert "body" not in audit.data and "detail" not in audit.data


def test_triage_is_not_persisted():
    # Computing triage signals must not create any rows (no per-user rollup / no stored ranking).
    target = _child("np1")
    report = file_report(_user("np2"), target, ReasonCode.GROOMING)
    before = Report.objects.count()
    triage_summary(report)
    triage_order([report])
    assert Report.objects.count() == before
