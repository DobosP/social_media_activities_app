"""Private-thread VIDEO attachments (ADR-0026): the synchronous admission gates (flag, cohort,
size, fail-closed SHA-256 scan), the asynchronous transcode + frame-scan status machine driven by
real ffmpeg, Range-aware serving, and the ephemeral/erasure lifecycle.

The fixture clip is built once per module with ffmpeg from a strong 2-D gradient PNG (constant
over time) so the poster and every sampled frame share one stable, non-degenerate perceptual
fingerprint — the frame-scan block test needs a deterministic dHash. ffmpeg-dependent tests are
skipped where ffmpeg/ffprobe are unavailable.
"""

import hashlib
import io
import os
import shutil
import subprocess
import tempfile
from datetime import timedelta

import pytest
from django.conf import settings
from django.contrib.gis.geos import Point
from django.core.files.uploadedfile import SimpleUploadedFile
from django.test import Client, override_settings
from django.utils import timezone
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, Cohort, ParentalConsent, User
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media import video
from apps.media.models import Attachment
from apps.media.perceptual import dhash_hex
from apps.media.storage import get_storage
from apps.ops.models import DeferredTask
from apps.safety.models import AuditLog
from apps.social import services as social
from apps.social.models import Membership
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db
PW = "pw-123-secret"

_HAS_FFMPEG = video.ffmpeg_available()
requires_ffmpeg = pytest.mark.skipif(not _HAS_FFMPEG, reason="ffmpeg/ffprobe not available")

# A magic-only "video" (bytes 4:8 == b"ftyp") for the admission gates that reject BEFORE any
# ffprobe/ffmpeg call — so those tests don't need the ffmpeg-built fixture.
_FAKE_MP4 = b"\x00\x00\x00\x18ftypmp42" + b"\x00" * 48

# Settings for the real-processing tests. The frame-scan interval is pinned to 1s: the module
# fixture is a 2s clip (per the spec), and at the DEFAULT 5s interval `fps=1/5` samples zero
# frames from a sub-interval clip, which makes ffmpeg exit non-zero and the whole transcode fail
# (see the product-bug note at the end of this module). 1s samples ~2 frames, exercising the real
# frame-scan path on the short fixture.
_PROC = {"MEDIA_VIDEO_ENABLED": True, "MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS": 1}
_FRAME_SCAN_INTERVAL = 1


def _gradient_png(size=(320, 240)) -> bytes:
    w, h = size
    img = Image.new("RGB", (w, h))
    px = img.load()
    for x in range(w):
        r = (x * 255) // w
        for y in range(h):
            px[x, y] = (r, (y * 255) // h, (x + y) % 256)
    out = io.BytesIO()
    img.save(out, format="PNG")
    return out.getvalue()


@pytest.fixture(scope="module")
def video_bytes(tmp_path_factory):
    if not _HAS_FFMPEG:
        pytest.skip("ffmpeg/ffprobe not available")
    d = tmp_path_factory.mktemp("videofix")
    png = d / "grad.png"
    png.write_bytes(_gradient_png())
    out = d / "fixture.mp4"
    subprocess.run(
        [
            "ffmpeg",
            "-y",
            "-loop",
            "1",
            "-i",
            str(png),
            "-f",
            "lavfi",
            "-i",
            "sine=frequency=440:duration=2",
            "-t",
            "2",
            "-r",
            "10",
            "-c:v",
            "libx264",
            "-pix_fmt",
            "yuv420p",
            "-c:a",
            "aac",
            "-b:a",
            "96k",
            "-shortest",
            "-movflags",
            "+faststart",
            str(out),
        ],
        check=True,
        capture_output=True,
    )
    return out.read_bytes()


def _frame_fp(raw: bytes) -> str | None:
    """The perceptual fingerprint of a sampled frame of the (locally) transcoded fixture — the
    exact bytes the service's frame scan will hash. Constant gradient content ⇒ every frame and
    the poster share this dHash, so a blocklist entry of it deterministically blocks the clip."""
    d = tempfile.mkdtemp(prefix="fpcalc-")
    try:
        src = os.path.join(d, "src.mp4")
        with open(src, "wb") as fh:
            fh.write(raw)
        out = os.path.join(d, "out.mp4")
        video.transcode(
            src,
            out,
            max_side=1280,
            max_duration=90,
            crf=23,
            preset="medium",
            audio_bitrate="96k",
            threads=2,
            timeout=600,
        )
        for frame in video.sample_frames(
            out, d, interval_seconds=_FRAME_SCAN_INTERVAL, max_frames=25
        ):
            fp = dhash_hex(frame)
            if fp:
                return fp
        return dhash_hex(video.extract_poster(out, duration=2.0, timeout=60))
    finally:
        shutil.rmtree(d, ignore_errors=True)


@pytest.fixture(scope="module")
def frame_fp(video_bytes):
    fp = _frame_fp(video_bytes)
    if not fp:
        pytest.skip("fixture pattern yielded no usable perceptual fingerprint")
    return fp


# --- helpers (mirroring apps/media/tests/test_attachments.py) -------------------------------


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


def _type(slug="vid-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="vid-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _activity(owner, slug="vid-bball"):
    from apps.places.models import ApprovedChildVenue, Place

    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    if owner.cohort == Cohort.CHILD:
        ApprovedChildVenue.objects.create(place=place)
    return social.create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Game",
        starts_at=timezone.now() + timedelta(days=1),
    )


def _join(activity, user):
    return Membership.objects.create(
        activity=activity, user=user, role=Membership.Role.MEMBER, state=Membership.State.MEMBER
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


def _fileobj(raw, name="clip.mp4"):
    return SimpleUploadedFile(name, raw, content_type="video/mp4")


def _attach(owner, activity, raw, **kwargs):
    post = social.post_to_thread(owner, activity, "clip")
    att = media.attach_to_post(owner, post, filename="clip.mp4", fileobj=_fileobj(raw), **kwargs)
    return att, post


# ============================================================================================
# A. ADMISSION GATES
# ============================================================================================


@override_settings(MEDIA_VIDEO_ENABLED=False)
def test_flag_off_rejects_video():
    # 1. The capability defaults off — a video fileobj is refused with nothing persisted.
    owner = _adult("vid_off")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "clip")
    with pytest.raises(media.MediaRejected):
        media.attach_to_post(owner, post, filename="clip.mp4", fileobj=_fileobj(_FAKE_MP4))
    assert not Attachment.objects.filter(post=post).exists()


@override_settings(MEDIA_VIDEO_ENABLED=True)
def test_video_blocked_in_non_video_cohort():
    # 2. Flag on, but a minor-cohort activity is not in MEDIA_VIDEO_COHORTS → NotAuthorized
    # (mirrors the PDF "adults-only for a new media type" precedent). Rejected before ffprobe.
    child = _child("vid_c2")
    activity = _activity(child, slug="vid-kids")
    post = social.post_to_thread(child, activity, "hi")
    with pytest.raises(media.NotAuthorized):
        media.attach_to_post(child, post, filename="clip.mp4", fileobj=_fileobj(_FAKE_MP4))
    assert not Attachment.objects.filter(post=post).exists()


@requires_ffmpeg
def test_oversize_video_rejected(video_bytes):
    # 3. Adult cohort, flag on, but over the byte cap → MediaRejected.
    owner = _adult("vid_o3")
    activity = _activity(owner)
    with override_settings(MEDIA_VIDEO_ENABLED=True, MEDIA_VIDEO_MAX_UPLOAD_BYTES=1000):
        post = social.post_to_thread(owner, activity, "clip")
        with pytest.raises(media.MediaRejected):
            media.attach_to_post(owner, post, filename="clip.mp4", fileobj=_fileobj(video_bytes))
    assert not Attachment.objects.filter(post=post).exists()


@requires_ffmpeg
def test_video_fail_closed_without_effective_scanner(video_bytes):
    # 4. Scanner required but ineffective (empty blocklists) → refused + audited, nothing stored.
    owner = _adult("vid_o4")
    activity = _activity(owner)
    with override_settings(
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_REQUIRE_SCANNER=True,
        MEDIA_CSAM_HASH_BLOCKLIST=[],
        MEDIA_PERCEPTUAL_BLOCKLIST=[],
    ):
        post = social.post_to_thread(owner, activity, "clip")
        with pytest.raises(media.MediaRejected):
            media.attach_to_post(owner, post, filename="clip.mp4", fileobj=_fileobj(video_bytes))
    assert not Attachment.objects.filter(post=post).exists()
    audit = AuditLog.objects.filter(event="media.attach_blocked").order_by("-id").first()
    assert audit is not None
    assert audit.data.get("reason") == "no_scanner"


@requires_ffmpeg
def test_video_sha256_blocklist_hit_rejected(video_bytes):
    # 5. The original bytes' SHA-256 is on the blocklist → refused, no row, source never stored.
    owner = _adult("vid_o5")
    activity = _activity(owner)
    digest = hashlib.sha256(video_bytes).hexdigest()
    with override_settings(
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_REQUIRE_SCANNER=True,
        MEDIA_CSAM_HASH_BLOCKLIST=[digest],
    ):
        post = social.post_to_thread(owner, activity, "clip")
        with pytest.raises(media.MediaRejected):
            media.attach_to_post(owner, post, filename="clip.mp4", fileobj=_fileobj(video_bytes))
    assert not Attachment.objects.filter(post=post).exists()


@requires_ffmpeg
def test_happy_admission_creates_withheld_pending_row(video_bytes):
    # 6. Clean admission: a withheld (pending) VIDEO row whose SHA-256 == the streamed original
    # digest, byte_size == len(bytes), no delivery key yet, source quarantined and present.
    owner = _adult("vid_o6")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
    assert att.kind == Attachment.Kind.VIDEO
    assert att.status == Attachment.Status.PENDING
    assert att.storage_key == ""
    assert att.source_storage_key.startswith("video-src/")
    assert att.sha256 == hashlib.sha256(video_bytes).hexdigest()
    assert att.byte_size == len(video_bytes)
    assert att.is_available() is False
    assert get_storage().exists(att.source_storage_key) is True


# ============================================================================================
# B. PROCESSING STATE MACHINE (real ffmpeg)
# ============================================================================================


@requires_ffmpeg
def test_processing_brings_pending_to_ready(video_bytes, django_capture_on_commit_callbacks):
    # 7. Transcode + poster + frame-scan → READY: delivery key stored, poster stored, quarantined
    # source deleted, sane dimensions/duration, EXIF stripped, and a probe of the stored MP4 shows
    # H.264 with no location metadata.
    owner = _adult("vid_o7")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        source_key = att.source_storage_key
        assert get_storage().exists(source_key) is True
        with django_capture_on_commit_callbacks(execute=True):
            assert media.process_pending_videos() == 1
    att.refresh_from_db()
    assert att.status == Attachment.Status.READY
    assert att.storage_key.startswith("videos/")
    assert get_storage().exists(att.storage_key) is True
    assert att.poster_storage_key.startswith("video-posters/")
    assert get_storage().exists(att.poster_storage_key) is True
    assert att.source_storage_key == ""
    assert get_storage().exists(source_key) is False
    assert att.width == 320 and att.height == 240
    assert abs(att.duration_seconds - 2) <= 1
    assert att.exif_stripped is True
    assert att.content_type == "video/mp4"

    scratch = tempfile.mkdtemp(prefix="probe-")
    try:
        check = os.path.join(scratch, "check.mp4")
        with open(check, "wb") as fh:
            fh.write(get_storage().open(att.storage_key))
        info = video.probe(check)
    finally:
        shutil.rmtree(scratch, ignore_errors=True)
    vstreams = [s for s in info["streams"] if s.get("codec_type") == "video"]
    assert vstreams and vstreams[0]["codec_name"] == "h264"
    fmt_tags = {k.lower() for k in (info.get("format", {}).get("tags") or {})}
    assert not any("location" in k for k in fmt_tags)


@requires_ffmpeg
def test_withheld_video_invisible_until_ready(video_bytes):
    # 8. A fellow member sees a "processing" placeholder (no url) while pending; once READY the
    # stream yields a playable url plus a poster url.
    owner = _adult("vid_o8")
    member = _adult("vid_m8")
    activity = _activity(owner)
    _join(activity, member)
    with override_settings(**_PROC):
        att, post = _attach(owner, activity, video_bytes)
        pending = media.attachments_for_posts([post], member)[post.id][0]
        assert pending.processing is True
        assert pending.url == ""
        assert media.process_pending_videos() == 1
        ready = media.attachments_for_posts([post], member)[post.id][0]
    assert ready.processing is False
    assert ready.url != ""
    assert ready.poster_url != ""


@requires_ffmpeg
def test_frame_scan_blocks_known_bad_imagery(video_bytes, frame_fp):
    # 9. A sampled-frame fingerprint on the perceptual blocklist blocks the clip: never served,
    # source retained as evidence, staff-only visibility. The SHA-256 list is non-empty (so the
    # scanner is effective) but crafted not to match, isolating the perceptual/frame layer.
    owner = _adult("vid_o9")
    member = _adult("vid_m9")
    staff = _adult("vid_s9")
    staff.is_staff = True
    staff.save(update_fields=["is_staff"])
    activity = _activity(owner)
    _join(activity, member)
    with override_settings(
        **_PROC,
        MEDIA_PERCEPTUAL_BLOCKLIST=[frame_fp],
        MEDIA_REQUIRE_SCANNER=True,
        MEDIA_CSAM_HASH_BLOCKLIST=["0" * 64],
    ):
        att, _post = _attach(owner, activity, video_bytes)
        source_key = att.source_storage_key
        assert media.process_pending_videos() == 1
    att.refresh_from_db()
    assert att.status == Attachment.Status.BLOCKED
    assert att.storage_key == ""
    assert att.source_storage_key == source_key != ""  # evidence retained
    assert get_storage().exists(source_key) is True
    assert media.can_view_attachment(member, att) is False
    assert media.can_view_attachment(staff, att) is True


@requires_ffmpeg
def test_invalid_content_fails_terminally(django_capture_on_commit_callbacks):
    # 10. Junk behind a valid ftyp header: admitted (magic sniff), then deterministically FAILED —
    # no delivery key, quarantined source reclaimed, nothing left pending to retry.
    junk = b"\x00\x00\x00\x18ftypmp42" + os.urandom(5000)
    owner = _adult("vid_o10")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, junk)
        source_key = att.source_storage_key
        with django_capture_on_commit_callbacks(execute=True):
            media.process_pending_videos()
    att.refresh_from_db()
    assert att.status == Attachment.Status.FAILED
    assert att.storage_key == ""
    assert get_storage().exists(source_key) is False
    assert not Attachment.objects.filter(
        kind=Attachment.Kind.VIDEO,
        status__in=[Attachment.Status.PENDING, Attachment.Status.PROCESSING],
    ).exists()


@requires_ffmpeg
def test_attempts_exhausted_fails_on_claim(video_bytes):
    # 11. A row that has already burned MEDIA_VIDEO_MAX_ATTEMPTS is finalised FAILED on the claim
    # side (the queue never retries forever).
    owner = _adult("vid_o11")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        Attachment.objects.filter(pk=att.pk).update(
            processing_attempts=settings.MEDIA_VIDEO_MAX_ATTEMPTS
        )
        assert media.process_pending_videos() == 1
    att.refresh_from_db()
    assert att.status == Attachment.Status.FAILED


@requires_ffmpeg
def test_stale_processing_row_is_reclaimed(video_bytes):
    # 12. A row stuck in PROCESSING past the stale window (crashed worker) with attempts left is
    # reclaimed by the next run and processed through to READY.
    owner = _adult("vid_o12")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        stale = timezone.now() - timedelta(
            seconds=settings.MEDIA_VIDEO_STALE_PROCESSING_SECONDS + 60
        )
        Attachment.objects.filter(pk=att.pk).update(
            status=Attachment.Status.PROCESSING, processing_started_at=stale, processing_attempts=1
        )
        assert media.process_pending_videos() == 1
    att.refresh_from_db()
    assert att.status == Attachment.Status.READY
    assert att.storage_key.startswith("videos/")
    assert att.source_storage_key == ""


@requires_ffmpeg
def test_ineffective_scanner_leaves_row_pending_without_burning_attempts(video_bytes):
    # 13. Fail-closed at processing time: with no effective scanner, the run claims nothing, the
    # row stays PENDING, and its attempt counter is NOT incremented.
    owner = _adult("vid_o13")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
    with override_settings(
        MEDIA_REQUIRE_SCANNER=True, MEDIA_CSAM_HASH_BLOCKLIST=[], MEDIA_PERCEPTUAL_BLOCKLIST=[]
    ):
        assert media.process_pending_videos() == 0
    att.refresh_from_db()
    assert att.status == Attachment.Status.PENDING
    assert att.processing_attempts == 0


# ============================================================================================
# C. SERVING
# ============================================================================================


@requires_ffmpeg
def test_serve_ready_video_main(video_bytes):
    # 14. The main variant resolves + serves the MP4 with the streaming headers players need.
    owner = _adult("vid_o14")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        assert media.process_pending_videos() == 1
        att.refresh_from_db()
        url = media.attachment_signed_url(att, owner, variant="main")
        assert media.resolve_attachment_token(url.rstrip("/").split("/")[-1], owner)[1] == "main"
        resp = _client(owner).get(url)
    assert resp.status_code == 200
    assert resp["Content-Type"] == "video/mp4"
    assert resp["Accept-Ranges"] == "bytes"
    assert resp["Cache-Control"].startswith("private")


@requires_ffmpeg
def test_range_requests(video_bytes):
    # 15. Single Range (206 + Content-Range), suffix Range (last N), and an unsatisfiable Range
    # (416 + "bytes */total").
    owner = _adult("vid_o15")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        assert media.process_pending_videos() == 1
        att.refresh_from_db()
        url = media.attachment_signed_url(att, owner)
        total = att.byte_size
        c = _client(owner)
        r1 = c.get(url, HTTP_RANGE="bytes=0-99")
        assert r1.status_code == 206
        assert r1["Content-Range"] == f"bytes 0-99/{total}"
        assert len(r1.content) == 100
        r2 = c.get(url, HTTP_RANGE="bytes=-50")
        assert r2.status_code == 206
        assert r2["Content-Range"] == f"bytes {total - 50}-{total - 1}/{total}"
        assert len(r2.content) == 50
        r3 = c.get(url, HTTP_RANGE="bytes=999999999-")
        assert r3.status_code == 416
        assert r3["Content-Range"] == f"bytes */{total}"


@requires_ffmpeg
def test_poster_serving_and_pending_has_no_poster(video_bytes):
    # 16. A READY video's poster serves as an image; the poster variant on a still-PENDING video
    # is refused (there is no poster yet).
    owner = _adult("vid_o16")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        with pytest.raises(media.NotAuthorized):
            media.attachment_signed_url(att, owner, variant="poster")
        assert media.process_pending_videos() == 1
        att.refresh_from_db()
        poster_url = media.attachment_signed_url(att, owner, variant="poster")
        resp = _client(owner).get(poster_url)
    assert resp.status_code == 200
    assert resp["Content-Type"].startswith("image/")


@requires_ffmpeg
def test_non_member_cannot_resolve(video_bytes):
    # 17. An adult outside the activity cannot mint a serving URL for the clip.
    owner = _adult("vid_o17")
    outsider = _adult("vid_x17")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        assert media.process_pending_videos() == 1
        att.refresh_from_db()
        with pytest.raises(media.NotAuthorized):
            media.attachment_signed_url(att, outsider)


# ============================================================================================
# D. LIFECYCLE
# ============================================================================================


@requires_ffmpeg
def test_purge_expired_ready_video_clears_all_blobs(video_bytes):
    # 18. Purging an expired READY video reclaims every backing object (mp4 + poster) and clears
    # all keys, retaining the row.
    owner = _adult("vid_o18")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes, ttl_seconds=3600)
        assert media.process_pending_videos() == 1
        att.refresh_from_db()
    video_key, poster_key = att.storage_key, att.poster_storage_key
    assert get_storage().exists(video_key) and get_storage().exists(poster_key)
    att.expires_at = timezone.now() - timedelta(minutes=5)
    att.save(update_fields=["expires_at"])
    assert media.purge_expired_attachments() == 1
    att.refresh_from_db()
    assert att.purged_at is not None
    assert att.storage_key == ""
    assert att.thumb_storage_key == ""
    assert att.poster_storage_key == ""
    assert att.source_storage_key == ""
    assert get_storage().exists(video_key) is False
    assert get_storage().exists(poster_key) is False


@requires_ffmpeg
def test_purge_skips_expired_blocked_video(video_bytes, frame_fp):
    # 19. A safety-BLOCKED clip is never purged even when expired — the retained source IS the
    # moderation evidence.
    owner = _adult("vid_o19")
    activity = _activity(owner)
    with override_settings(
        **_PROC,
        MEDIA_PERCEPTUAL_BLOCKLIST=[frame_fp],
        MEDIA_REQUIRE_SCANNER=True,
        MEDIA_CSAM_HASH_BLOCKLIST=["0" * 64],
    ):
        att, _post = _attach(owner, activity, video_bytes, ttl_seconds=3600)
        assert media.process_pending_videos() == 1
    att.refresh_from_db()
    assert att.status == Attachment.Status.BLOCKED
    source_key = att.source_storage_key
    assert get_storage().exists(source_key) is True
    att.expires_at = timezone.now() - timedelta(minutes=5)
    att.save(update_fields=["expires_at"])
    assert media.purge_expired_attachments() == 0
    att.refresh_from_db()
    assert att.purged_at is None
    assert get_storage().exists(source_key) is True


@requires_ffmpeg
def test_row_delete_enqueues_blob_cleanup_for_video_and_poster(video_bytes):
    # 20. Deleting a READY video row enqueues an erasure.blob_cleanup task for BOTH the mp4 and
    # the poster key (GDPR Art. 17 blob reclaim on every removal path).
    owner = _adult("vid_o20")
    activity = _activity(owner)
    with override_settings(**_PROC):
        att, _post = _attach(owner, activity, video_bytes)
        assert media.process_pending_videos() == 1
        att.refresh_from_db()
    video_key, poster_key = att.storage_key, att.poster_storage_key
    media.delete_attachment(owner, att)
    queued = set()
    for task in DeferredTask.objects.filter(kind="erasure.blob_cleanup"):
        queued.update(task.payload.get("blob_keys", []))
    assert video_key in queued
    assert poster_key in queued


# HISTORY NOTE: these tests originally caught a real bug here — sample_frames() used the fps
# filter, whose first sample sits at t=interval/2, so every clip shorter than ~interval/2
# produced ZERO frames, a spurious ffmpeg failure, and a FAILED row at the shipped 5s default.
# Fixed in apps/media/video.py (select-based sampler that always emits frame 0; regression:
# test_video_pipeline.py::test_sample_frames_yields_frame_zero_for_clips_shorter_than_interval).
# The 1s interval in _PROC is kept only to sample several frames from the short fixture.
