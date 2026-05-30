"""Ephemeral ("temporary") thread pictures: a per-cohort minimum TTL (24h floor for minors so
disappearing media can't be weaponised), expiry stops serving immediately, the purge job reclaims
the blob but EXEMPTS hidden/reported content (evidence is preserved), and the row is retained.
"""

from datetime import timedelta
from io import BytesIO

import pytest
from django.contrib.contenttypes.models import ContentType
from django.contrib.gis.geos import Point
from django.test import Client
from django.utils import timezone
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media.models import Attachment
from apps.places.models import Place
from apps.safety.models import ReasonCode, Report
from apps.social import services as social
from apps.social.models import Membership, Post
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"


def _png(color=(10, 120, 200), size=(8, 8)) -> bytes:
    out = BytesIO()
    Image.new("RGB", size, color).save(out, format="PNG")
    return out.getvalue()


def _adult(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _child(name):
    u = User.objects.create_user(username=name, password=PW, display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    ParentalConsent.objects.create(
        minor=u, guardian_identifier="g", status=ParentalConsent.Status.ACTIVE
    )
    return u


def _type(slug="eph-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="eph-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _activity(owner, slug="eph-bball"):
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return social.create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Game",
        starts_at=timezone.now() + timedelta(days=1),
    )


def _join(activity, user):
    Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )
    return activity


def _expire(att):
    """Force an attachment past its expiry (simulate time passing) without sleeping."""
    att.expires_at = timezone.now() - timedelta(minutes=5)
    att.save(update_fields=["expires_at"])


# --- the cohort TTL floor ------------------------------------------------------------------


def test_adult_ttl_is_honoured():
    owner = _adult("eph_a1")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "look")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    assert att.expires_at is not None
    delta = att.expires_at - timezone.now()
    assert timedelta(minutes=55) < delta < timedelta(minutes=65)  # ~1h, the adult floor
    assert att.is_available() is True


def test_minor_ttl_clamped_up_to_24h_floor():
    # A child asking for a 1-hour disappear is clamped UP to the 24h floor — disappearing media
    # can never outrun a guardian/moderator/report in a children's thread.
    child = _child("eph_c1")
    activity = _activity(child, slug="eph-kids")
    post = social.post_to_thread(child, activity, "hi")
    att = media.attach_to_post(child, post, filename="x.png", data=_png(), ttl_seconds=3600)
    assert att.expires_at - timezone.now() > timedelta(hours=23)


def test_no_ttl_is_permanent():
    owner = _adult("eph_a2")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "keep me")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png())  # no ttl
    assert att.expires_at is None
    assert media.purge_expired_attachments() == 0  # a permanent picture is never purged


def test_zero_or_negative_ttl_is_permanent_not_instant():
    owner = _adult("eph_a3")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=0)
    assert att.expires_at is None  # a crafted 0 can't make it vanish faster than the floor


# --- expiry stops serving; the purge reclaims the blob -------------------------------------


def test_expired_attachment_stops_serving_before_purge():
    owner = _adult("eph_a4")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    assert att.is_available() is False
    with pytest.raises(media.NotAuthorized):
        media.attachment_signed_url(att, owner)
    # the stream renders an "expired" placeholder (no url) instead of a broken image
    by_post = media.attachments_for_posts([post], owner)
    rendered = by_post[post.id][0]
    assert rendered.expired is True
    assert rendered.url == ""


def test_purge_reclaims_blob_and_retains_row():
    owner = _adult("eph_a5")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    assert media.purge_expired_attachments() == 1
    att.refresh_from_db()
    assert att.purged_at is not None
    assert att.storage_key == ""  # bytes gone
    assert Attachment.objects.filter(pk=att.pk).exists()  # row retained (audit/sha256 survive)
    assert att.sha256  # the hash survives for retroactive moderation matching
    # idempotent: a second run finds nothing
    assert media.purge_expired_attachments() == 0


def test_purge_exempts_hidden_post():
    owner = _adult("eph_a6")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    Post.objects.filter(pk=post.pk).update(is_hidden=True)
    assert media.purge_expired_attachments() == 0  # evidence preserved
    att.refresh_from_db()
    assert att.purged_at is None and att.storage_key != ""


def test_purge_exempts_unresolved_reported_post():
    owner = _adult("eph_a7")
    reporter = _adult("eph_r7")
    activity = _activity(owner)
    _join(activity, reporter)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    Report.objects.create(
        reporter=reporter,
        target_type=ContentType.objects.get_for_model(Post),
        target_id=post.id,
        reason=ReasonCode.HARASSMENT,
        status=Report.Status.OPEN,
    )
    assert media.purge_expired_attachments() == 0  # live moderation case → evidence kept
    # once resolved (dismissed), the hold releases and the blob becomes purgeable again
    Report.objects.filter(target_id=post.id).update(status=Report.Status.DISMISSED)
    assert media.purge_expired_attachments() == 1


def _report(reporter, *, model, obj_id, status=Report.Status.OPEN):
    return Report.objects.create(
        reporter=reporter,
        target_type=ContentType.objects.get_for_model(model),
        target_id=obj_id,
        reason=ReasonCode.GROOMING,
        status=status,
    )


def test_purge_exempts_report_against_the_uploader():
    # The dominant child-safety report: a guardian/peer reports the uploading USER (the groomer),
    # not an internal post id. That must still preserve the image.
    owner = _adult("eph_a8")
    reporter = _adult("eph_r8")
    activity = _activity(owner)
    _join(activity, reporter)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    _report(reporter, model=User, obj_id=owner.id)
    assert media.purge_expired_attachments() == 0


def test_purge_exempts_report_against_the_activity():
    owner = _adult("eph_a9")
    reporter = _adult("eph_r9")
    activity = _activity(owner)
    _join(activity, reporter)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    from apps.social.models import Activity

    _report(reporter, model=Activity, obj_id=activity.id)
    assert media.purge_expired_attachments() == 0


def test_purge_holds_through_actioned_report_releases_only_on_dismissed():
    # A WARN/SUSPEND/BAN substantiates the report but does NOT hide the image — the evidence must
    # survive the DSA appeal window. Only a DISMISSED report releases the purge hold.
    owner = _adult("eph_a10")
    reporter = _adult("eph_r10")
    activity = _activity(owner)
    _join(activity, reporter)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    rep = _report(reporter, model=User, obj_id=owner.id, status=Report.Status.ACTIONED)
    assert media.purge_expired_attachments() == 0  # actioned still holds (appeal window)
    Report.objects.filter(pk=rep.pk).update(status=Report.Status.DISMISSED)
    assert media.purge_expired_attachments() == 1


def test_purge_exempts_hidden_activity():
    owner = _adult("eph_a11")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    from apps.social.models import Activity

    Activity.objects.filter(pk=activity.pk).update(is_hidden=True)
    assert media.purge_expired_attachments() == 0


def test_staff_can_retrieve_expired_blob_before_purge_member_cannot():
    owner = _adult("eph_a12")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    staff = _adult("eph_staff")
    staff.is_staff = True
    staff.save(update_fields=["is_staff"])
    # member: gone. staff: still retrievable while the blob physically exists (evidence).
    with pytest.raises(media.NotAuthorized):
        media.attachment_signed_url(att, owner)
    assert media.attachment_signed_url(att, staff)  # no raise
    # after purge, even staff cannot (bytes are gone)
    assert media.purge_expired_attachments() == 1
    att.refresh_from_db()
    with pytest.raises(media.NotAuthorized):
        media.attachment_signed_url(att, staff)


def test_storage_failure_does_not_abort_run_or_falsely_mark_purged(monkeypatch):
    owner = _adult("eph_a13")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)

    class _FailingStorage:
        def delete(self, key):
            raise OSError("storage unavailable")

    monkeypatch.setattr(media, "get_storage", lambda: _FailingStorage())
    assert media.purge_expired_attachments() == 0  # the failure is swallowed, not propagated
    att.refresh_from_db()
    assert att.purged_at is None and att.storage_key != ""  # NOT falsely marked purged
    monkeypatch.undo()
    assert media.purge_expired_attachments() == 1  # retried successfully next tick


# --- the web compose flow ------------------------------------------------------------------


def test_web_compose_sets_disappear():
    owner = _adult("eph_w1")
    activity = _activity(owner)
    c = Client()
    c.force_login(owner)
    img = BytesIO(_png())
    img.name = "x.png"
    r = c.post(
        f"/activities/{activity.id}/post/",
        {"body": "temp pic", "disappear": "3600", "attachment": img},
    )
    assert r.status_code == 302
    att = Attachment.objects.filter(uploader=owner).latest("id")
    assert att.expires_at is not None
