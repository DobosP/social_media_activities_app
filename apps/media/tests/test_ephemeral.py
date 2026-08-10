"""Ephemeral ("temporary") thread pictures: a per-cohort minimum TTL (24h floor for minors so
disappearing media can't be weaponised), expiry stops serving immediately, the purge job reclaims
the blob but EXEMPTS hidden/reported content (evidence is preserved), and the row is retained.
"""

from datetime import timedelta
from io import BytesIO

import pytest
from django.contrib.contenttypes.models import ContentType
from django.contrib.gis.geos import Point
from django.db import connection
from django.test import Client
from django.test.utils import CaptureQueriesContext
from django.utils import timezone
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.communities.models import Area
from apps.media import services as media
from apps.media.models import Attachment
from apps.places.models import Place
from apps.safety.models import AuditLog, ModerationAction, ReasonCode, Report
from apps.safety.services import file_appeal, resolve_appeal, take_action
from apps.social import services as social
from apps.social.models import Group, Membership, Post, Thread
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


# --- author-deleted posts: storage limitation vs evidence preservation ---------------------


def _mod(name):
    u = _adult(name)
    u.is_staff = True
    u.save(update_fields=["is_staff"])
    return u


def test_purge_reclaims_author_deleted_expired_blob():
    # THE F5 defect: an author self-delete sets is_hidden, and the blanket hidden-post exemption
    # preserved the withdrawn bytes on EVERY run, forever — a GDPR storage-limitation
    # (Art. 5(1)(e)) failure. Author-withdrawn content with no standing REMOVE is nobody's
    # evidence: the purge must reclaim it.
    owner = _adult("eph_ad1")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    social.delete_own_post(owner, post)
    _expire(att)
    assert media.purge_expired_attachments() == 1
    att.refresh_from_db()
    assert att.purged_at is not None and att.storage_key == ""
    assert AuditLog.objects.filter(event="media.attachment_purged").exists()


def test_purge_keeps_blob_under_unlifted_remove():
    # The narrowing is strict: a moderator REMOVE with no author involvement keeps the
    # exemption — nothing currently preserved becomes purgeable.
    mod, owner = _mod("eph_m2"), _adult("eph_ad2")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    assert media.purge_expired_attachments() == 0
    att.refresh_from_db()
    assert att.purged_at is None and att.storage_key != ""


def test_purge_keeps_blob_remove_then_self_delete():
    # REMOVE first, author deletes second: BOTH holds exist, and the platform's standing REMOVE
    # must keep the evidence — the naive `is_hidden and not is_author_deleted` predicate would
    # purge exactly this row.
    mod, owner = _mod("eph_m3"), _adult("eph_ad3")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    social.delete_own_post(owner, post)
    assert media.purge_expired_attachments() == 0
    att.refresh_from_db()
    assert att.purged_at is None and att.storage_key != ""


def test_purge_keeps_admin_hidden_blob():
    # An admin manual hide has NO ModerationAction row — the standing-REMOVE helper layers ON
    # is_hidden, never replaces it, so this row must stay exempt (fail-closed).
    owner = _adult("eph_ad4")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    Post.objects.filter(pk=post.pk).update(is_hidden=True)
    assert not ModerationAction.objects.exists()  # the setup really is row-less
    assert media.purge_expired_attachments() == 0
    att.refresh_from_db()
    assert att.purged_at is None and att.storage_key != ""


def test_purge_reclaims_after_remove_lifted_when_author_deleted():
    # REMOVE, author deletes, appeal granted: the reversal leaves the post hidden (the author's
    # own act is permanent) but the platform's hold is lifted — so the only remaining reason is
    # the author's withdrawal, and the bytes are reclaimed.
    mod, owner = _mod("eph_m5"), _adult("eph_ad5")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    action = take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
    social.delete_own_post(owner, post)
    resolve_appeal(mod, file_appeal(owner, action, "wrong"), grant=True)
    post.refresh_from_db()
    assert post.is_hidden and post.is_author_deleted  # stays deleted — Art.17 fix upstream
    assert media.purge_expired_attachments() == 1
    att.refresh_from_db()
    assert att.purged_at is not None and att.storage_key == ""


def test_purge_keeps_author_deleted_but_reported():
    # Composition: the author withdrew it AND someone reported the uploader — the report arm
    # still preserves the evidence (the release narrows ONLY the hidden-post arm).
    owner, reporter = _adult("eph_ad6"), _adult("eph_r6")
    activity = _activity(owner)
    _join(activity, reporter)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    social.delete_own_post(owner, post)
    _expire(att)
    _report(reporter, model=User, obj_id=owner.id)
    assert media.purge_expired_attachments() == 0
    att.refresh_from_db()
    assert att.purged_at is None and att.storage_key != ""


def test_purge_locked_recheck_honours_late_remove(monkeypatch):
    # A REMOVE COMMITTED between the candidate snapshot and the row lock (as here) preserves
    # the evidence — the locked re-check re-runs the standing-REMOVE test against fresh state.
    # Best-effort only: a take_action whose is_hidden UPDATE is still BLOCKED on our row lock
    # has an uncommitted action row the re-check can't see (READ COMMITTED) — the same
    # visibility limit the _under_moderation re-check has always had.
    mod, owner = _mod("eph_m7"), _adult("eph_ad7")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    social.delete_own_post(owner, post)
    _expire(att)

    real = media.targets_with_unlifted_remove
    calls = []

    def race(model, target_ids):
        result = real(model, list(target_ids))
        if not calls:  # first call = the snapshot pass; the REMOVE lands right after it
            take_action(
                mod, Post.objects.get(pk=post.pk), ModerationAction.Action.REMOVE, ReasonCode.OTHER
            )
        calls.append(1)
        return result

    monkeypatch.setattr(media, "targets_with_unlifted_remove", race)
    assert media.purge_expired_attachments() == 0
    att.refresh_from_db()
    assert att.purged_at is None and att.storage_key != ""


def test_purge_snapshot_batches_standing_remove_lookup(monkeypatch):
    # The snapshot pass makes ONE batched standing-REMOVE query for ALL author-deleted
    # candidates (never per-attachment); the locked re-check then fires once per row actually
    # being released.
    owner = _adult("eph_ad8")
    activity = _activity(owner)
    atts, pids = [], []
    for i in range(3):
        post = social.post_to_thread(owner, activity, f"x{i}")
        att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
        social.delete_own_post(owner, post)
        _expire(att)
        atts.append(att)
        pids.append(post.id)

    real = media.targets_with_unlifted_remove
    seen = []

    def spy(model, target_ids):
        ids = list(target_ids)
        seen.append(ids)
        return real(model, ids)

    monkeypatch.setattr(media, "targets_with_unlifted_remove", spy)
    assert media.purge_expired_attachments() == 3
    assert sorted(seen[0]) == sorted(pids)  # one batched snapshot call over every candidate
    assert [len(ids) for ids in seen[1:]] == [1, 1, 1]  # per-row locked re-checks only


def test_purge_reclaims_admin_hidden_then_author_deleted():
    # Pin the OTHER order: an admin manual hide (no ModerationAction row) followed by the
    # author's own delete is BYTE-IDENTICAL in data to a plain self-delete (is_hidden +
    # is_author_deleted + zero action rows), so the release predicate reclaims it — no code
    # can distinguish the two states. An admin hold that must survive the author's deletion
    # needs a real REMOVE action (the docstring + docs/MEDIA_FILTERING.md say exactly this).
    owner = _adult("eph_ad9")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    Post.objects.filter(pk=post.pk).update(is_hidden=True)  # admin manual hide — row-less
    social.delete_own_post(owner, post)  # stamps provenance on the already-hidden row
    _expire(att)
    assert not ModerationAction.objects.exists()  # indistinguishable from a plain self-delete
    assert media.purge_expired_attachments() == 1
    att.refresh_from_db()
    assert att.purged_at is not None and att.storage_key == ""


def test_purge_limit_bounds_a_run_and_remainder_drains():
    # The DUE_JOBS entry passes a batch limit (mirroring transcode_videos) so a huge expiry
    # backlog can't monopolise the shared cron tick. Only actual reclaims count against it,
    # and the remainder drains on the next run.
    owner = _adult("eph_ad10")
    activity = _activity(owner)
    for i in range(3):
        post = social.post_to_thread(owner, activity, f"x{i}")
        _expire(media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600))
    assert media.purge_expired_attachments(limit=2) == 2
    assert Attachment.objects.filter(purged_at__isnull=True).count() == 1
    assert media.purge_expired_attachments(limit=2) == 1  # the backlog drains


def test_purge_snapshot_cost_constant_in_candidates():
    # Same-count-at-two-Ns: with every candidate exempt (REMOVE-then-self-delete, so the
    # author-deleted arm AND the standing-REMOVE lookup both fire), a run does snapshot-phase
    # work only — and that phase is fully batched, so the query count must NOT grow with the
    # number of candidates (no per-row query can hide in the exempt path).
    mod, owner = _mod("eph_m11"), _adult("eph_ad11")
    activity = _activity(owner)

    def _one(i):
        post = social.post_to_thread(owner, activity, f"x{i}")
        att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
        _expire(att)
        take_action(mod, post, ModerationAction.Action.REMOVE, ReasonCode.OTHER)
        social.delete_own_post(owner, post)

    _one(0)
    media.purge_expired_attachments()  # warm the ContentType cache off the measured runs
    with CaptureQueriesContext(connection) as at_one:
        assert media.purge_expired_attachments() == 0
    _one(1)
    _one(2)
    with CaptureQueriesContext(connection) as at_three:
        assert media.purge_expired_attachments() == 0
    assert len(at_one) == len(at_three)


def test_purge_survives_group_thread_attachment():
    # Defensive: Thread.activity is NULLABLE (a group thread). No public path can create an
    # ephemeral attachment there today (_resolve_expiry reads activity.cohort first), but the
    # activity dereference sits OUTSIDE the per-item try/except — one hypothetical row must
    # not abort the whole run, and the run must still reclaim everything else.
    owner = _adult("eph_ad12")
    area = Area.objects.create(city="Cluj-Napoca", slug="eph-grp", name="Cluj-Napoca")
    cat = ActivityCategory.objects.get_or_create(slug="eph-sport", defaults={"name": "Sport"})[0]
    group = Group.objects.create(
        owner=owner,
        area=area,
        category=cat,
        tier=Group.Tier.CATEGORY,
        cohort=owner.cohort,
        title="G",
    )
    thread = Thread.objects.create(group=group)
    gpost = Post.objects.create(thread=thread, author=owner, body="x")
    g_att = Attachment.objects.create(
        post=gpost,
        uploader=owner,
        kind=Attachment.Kind.IMAGE,
        storage_key="eph-grp/none.png",
        content_type="image/png",
        byte_size=1,
        sha256="0" * 64,
        expires_at=timezone.now() - timedelta(minutes=5),
    )
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "x")
    att = media.attach_to_post(owner, post, filename="x.png", data=_png(), ttl_seconds=3600)
    _expire(att)
    assert media.purge_expired_attachments() == 2  # neither crashed the run nor got skipped
    for a in (g_att, att):
        a.refresh_from_db()
        assert a.purged_at is not None and a.storage_key == ""


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
