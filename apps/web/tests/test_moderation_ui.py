"""ADR-0029 (B5) — the moderator-gated concern queue UI.

Covers: the login+is_moderator gate (non-moderator 403), the dashboard listing + automated-mode
banner, and each item action (review / dismiss / escalate / teen note relay) — its status
transition and its audited AuditLog row. These are FORMATIVE items, not DSA Art-16 notices; only
escalate crosses into the Report tooling, and only the teen note ever reaches a minor (human-
authored, never automated)."""

import pytest
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone

from apps.accounts.models import Role
from apps.notifications.models import Notification
from apps.notifications.services import set_muted_kinds
from apps.places.models import Place
from apps.safety.models import AuditLog, ConcernReview, ReasonCode, Report
from apps.social import services as social
from apps.social.tests.conftest import make_user
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


@pytest.fixture
def place(db):
    return Place.objects.create(
        name="Community Hall", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )


@pytest.fixture
def activity_type(db):
    category, _ = ActivityCategory.objects.get_or_create(
        slug="mod-test-sport", defaults={"name": "Sport"}
    )
    at, _ = ActivityType.objects.get_or_create(
        slug="mod-test-basketball", defaults={"name": "Basketball", "category": category}
    )
    return at


def _moderator(name="mod1"):
    mod = make_user(name)
    mod.role = Role.MODERATOR
    mod.save(update_fields=["role"])
    return mod


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _post(place, activity_type, body="something off topic"):
    owner = make_user("cr_owner")
    activity = social.create_activity(
        owner, place=place, activity_type=activity_type, title="Game", starts_at=timezone.now()
    )
    return owner, social.post_to_thread(owner, activity, body)


def _review(post, author, *, kind=ConcernReview.Kind.CONCERN_ESCALATED, payload=None):
    return ConcernReview.objects.create(
        kind=kind,
        post=post,
        subject_user=author,
        payload=payload or {"flaggers": 4, "window_days": 14},
    )


# --- gate --------------------------------------------------------------------------------------


def test_non_moderator_gets_403(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author)
    member = make_user("plain_member")
    c = _client(member)
    assert c.get("/moderation/").status_code == 403
    assert c.get(f"/moderation/concern/{review.pk}/").status_code == 403


def test_anonymous_redirected_to_login():
    resp = Client().get("/moderation/")
    assert resp.status_code == 302
    assert "/login" in resp.url or "/accounts/login" in resp.url


# --- dashboard ---------------------------------------------------------------------------------


def test_dashboard_lists_open_items(place, activity_type):
    author, post = _post(place, activity_type, body="clearly off-topic body")
    _review(post, author)
    body = _client(_moderator()).get("/moderation/").content.decode()
    assert "Moderation queue" in body
    assert "clearly off-topic body" in body


def test_automated_mode_banner(place, activity_type, settings):
    settings.MODERATION_MODE = "automated"
    author, post = _post(place, activity_type)
    _review(post, author)
    body = _client(_moderator()).get("/moderation/").content.decode()
    assert "Automated mode" in body


def test_default_mode_has_no_automated_banner(place, activity_type, settings):
    settings.MODERATION_MODE = "automated+human"
    body = _client(_moderator()).get("/moderation/").content.decode()
    assert "alerts muted, queue accumulates" not in body


# --- actions -----------------------------------------------------------------------------------


def test_mark_reviewed_transitions_and_audits(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author)
    mod = _moderator()
    resp = _client(mod).post(f"/moderation/concern/{review.pk}/", {"action": "review"})
    assert resp.status_code == 302
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.REVIEWED
    assert review.handled_by_id == mod.id and review.handled_at is not None
    assert AuditLog.objects.filter(
        event="concern.reviewed", target_ref=f"safety.concernreview:{review.pk}"
    ).exists()


def test_dismiss_transitions_and_audits(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author)
    mod = _moderator()
    _client(mod).post(f"/moderation/concern/{review.pk}/", {"action": "dismiss"})
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.DISMISSED
    assert AuditLog.objects.filter(event="concern.dismissed").exists()


def test_escalate_creates_report_and_audits(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author)
    mod = _moderator()
    _client(mod).post(f"/moderation/concern/{review.pk}/", {"action": "escalate"})
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.ESCALATED
    # A real Report was filed against the post via the canonical service.
    rep = Report.objects.filter(reporter=mod, reason=ReasonCode.OTHER).first()
    assert rep is not None and rep.target_id == post.pk
    assert AuditLog.objects.filter(event="concern.escalated").exists()


def test_teen_note_send_notifies_author_and_resolves(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author, kind=ConcernReview.Kind.TEEN_CONCERN)
    mod = _moderator()
    edited = "Hey, just a friendly heads-up about your post. You're doing great."
    _client(mod).post(f"/moderation/concern/{review.pk}/", {"action": "send_note", "note": edited})
    note = Notification.objects.filter(
        recipient=author, kind=Notification.Kind.FORMATIVE_NOTE
    ).first()
    assert note is not None and note.body == edited
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.REVIEWED
    assert AuditLog.objects.filter(event="concern.note_relayed").exists()


def test_send_note_rejected_for_non_teen_kind(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author, kind=ConcernReview.Kind.CONCERN_ESCALATED)
    mod = _moderator()
    _client(mod).post(f"/moderation/concern/{review.pk}/", {"action": "send_note", "note": "x"})
    # No corrective note leaves the adult-escalated path via this action; the item stays OPEN.
    assert not Notification.objects.filter(kind=Notification.Kind.FORMATIVE_NOTE).exists()
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.OPEN


def test_teen_note_uses_verbatim_prefill_in_form(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author, kind=ConcernReview.Kind.TEEN_CONCERN)
    body = _client(_moderator()).get(f"/moderation/concern/{review.pk}/").content.decode()
    assert "a bit off-topic for this group" in body
    assert "You&#x27;re doing great being here." in body or "You're doing great being here." in body


# --- R3: idempotency, sensor-kind escalate guard, muted relay, viewed audit --------------------


def test_double_escalate_files_one_report(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author)
    mod = _moderator()
    c = _client(mod)
    c.post(f"/moderation/concern/{review.pk}/", {"action": "escalate"})
    # Second POST on the now-ESCALATED item is rejected (only OPEN transitions) — no second Report.
    c.post(f"/moderation/concern/{review.pk}/", {"action": "escalate"})
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.ESCALATED
    assert Report.objects.filter(reporter=mod, target_id=post.pk).count() == 1


def test_escalate_rejected_for_sensor_kind(place, activity_type):
    # A SENSOR_PILEON item's subject is the PROTECTED victim — escalating it must never file a
    # Report (against the post OR the subject). The handler rejects it; the item stays OPEN.
    author, post = _post(place, activity_type)
    review = _review(post, author, kind=ConcernReview.Kind.SENSOR_PILEON)
    mod = _moderator()
    _client(mod).post(f"/moderation/concern/{review.pk}/", {"action": "escalate"})
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.OPEN
    assert not Report.objects.filter(reporter=mod).exists()


def test_sensor_kind_detail_hides_escalate_form(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author, kind=ConcernReview.Kind.SENSOR_COORDINATED)
    body = _client(_moderator()).get(f"/moderation/concern/{review.pk}/").content.decode()
    assert 'value="escalate"' not in body
    assert "can&#x27;t be escalated" in body or "can't be escalated" in body


def test_teen_note_muted_shows_honest_message(place, activity_type):
    author, post = _post(place, activity_type)
    set_muted_kinds(author, [Notification.Kind.FORMATIVE_NOTE])  # the teen muted the note kind
    review = _review(post, author, kind=ConcernReview.Kind.TEEN_CONCERN)
    mod = _moderator()
    resp = _client(mod).post(
        f"/moderation/concern/{review.pk}/", {"action": "send_note", "note": "hi"}, follow=True
    )
    assert "muted these notes" in resp.content.decode()
    assert not Notification.objects.filter(
        recipient=author, kind=Notification.Kind.FORMATIVE_NOTE
    ).exists()
    review.refresh_from_db()
    assert review.status == ConcernReview.Status.REVIEWED  # the moderator still handled the item
    assert AuditLog.objects.filter(event="concern.note_muted").exists()


def test_viewed_audit_row_on_get_when_post_rendered(place, activity_type):
    author, post = _post(place, activity_type)
    review = _review(post, author)
    _client(_moderator()).get(f"/moderation/concern/{review.pk}/")
    assert AuditLog.objects.filter(
        event="concern.viewed", target_ref=f"safety.concernreview:{review.pk}"
    ).exists()
