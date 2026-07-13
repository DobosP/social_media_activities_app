"""MaxBodySizeMiddleware (P1 DoS guard) + its ADR-0026 video-path exemption: the global
Content-Length cap applies to every request; a MULTIPART POST to the thread-post endpoint of a
video-capable-cohort activity gets a larger cap ONLY when MEDIA_VIDEO_ENABLED is on, and only
up to MEDIA_VIDEO_MAX_UPLOAD_BYTES plus multipart overhead. The size logic is unit-tested with
the cohort lookup patched; the cohort gate itself is covered by two DB tests at the end."""

import pytest
from django.http import HttpResponse
from django.test import RequestFactory, override_settings

from apps.ops.middleware import MaxBodySizeMiddleware

_MB = 1024 * 1024
_MULTIPART = "multipart/form-data; boundary=x"


def _middleware():
    """A fresh instance: MAX_REQUEST_BODY_BYTES is read once in __init__, so every case that
    varies it must build (or rebuild) the middleware inside its own override_settings block."""
    return MaxBodySizeMiddleware(lambda request: HttpResponse("ok"))


def _request(method: str, path: str, content_length: int, content_type: str = _MULTIPART):
    # No actual body bytes are built — CONTENT_LENGTH is passed straight into request.META,
    # overriding whatever RequestFactory.generic would derive from an (empty) body.
    return RequestFactory().generic(
        method, path, CONTENT_LENGTH=str(content_length), CONTENT_TYPE=content_type
    )


@pytest.fixture
def video_cohort_ok(monkeypatch):
    """Isolate the size logic from the DB cohort lookup (covered separately below)."""
    monkeypatch.setattr(
        MaxBodySizeMiddleware, "_cohort_accepts_video", staticmethod(lambda pk: True)
    )


# --- default (non-video) path: the plain global cap ------------------------------------------


def test_default_path_over_global_cap_is_413():
    with override_settings(MAX_REQUEST_BODY_BYTES=1000):
        resp = _middleware()(_request("POST", "/api/v1/anything", 1001))
    assert resp.status_code == 413


def test_default_path_under_global_cap_passes_through():
    with override_settings(MAX_REQUEST_BODY_BYTES=1000):
        resp = _middleware()(_request("POST", "/api/v1/anything", 999))
    assert resp.status_code == 200


# --- video (thread-post) path, video DISABLED: still the global cap --------------------------


def test_video_path_with_video_disabled_still_uses_global_cap(video_cohort_ok):
    with override_settings(MAX_REQUEST_BODY_BYTES=1000, MEDIA_VIDEO_ENABLED=False):
        resp = _middleware()(_request("POST", "/activities/123/post", 1001))
    assert resp.status_code == 413


# --- video path, video ENABLED: the larger, video-specific cap -------------------------------


def test_video_enabled_50mb_multipart_upload_passes(video_cohort_ok):
    with override_settings(MEDIA_VIDEO_ENABLED=True, MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB):
        resp = _middleware()(_request("POST", "/activities/123/post", 50 * _MB))
    assert resp.status_code == 200


def test_video_enabled_over_cap_plus_multipart_overhead_is_413(video_cohort_ok):
    with override_settings(MEDIA_VIDEO_ENABLED=True, MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB):
        over_cap_and_overhead = 80 * _MB + 3 * _MB
        resp = _middleware()(_request("POST", "/activities/123/post", over_cap_and_overhead))
    assert resp.status_code == 413


def test_video_enabled_non_multipart_post_stays_at_global_cap(video_cohort_ok):
    """A pre-auth JSON/urlencoded body never rides the video cap — only a genuine multipart
    upload can (review finding: bounds the anonymous disk-spool amplification window)."""
    with override_settings(
        MAX_REQUEST_BODY_BYTES=1000,
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB,
    ):
        resp = _middleware()(
            _request("POST", "/activities/123/post/", 1001, content_type="application/json")
        )
    assert resp.status_code == 413


def test_video_enabled_get_request_stays_at_global_cap(video_cohort_ok):
    with override_settings(
        MAX_REQUEST_BODY_BYTES=1000,
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB,
    ):
        resp = _middleware()(_request("GET", "/activities/123/post", 1001))
    assert resp.status_code == 413


def test_video_enabled_path_with_extra_segment_stays_at_global_cap(video_cohort_ok):
    with override_settings(
        MAX_REQUEST_BODY_BYTES=1000,
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB,
    ):
        resp = _middleware()(_request("POST", "/activities/123/post/extra", 1001))
    assert resp.status_code == 413


def test_video_enabled_non_numeric_activity_id_stays_at_global_cap(video_cohort_ok):
    with override_settings(
        MAX_REQUEST_BODY_BYTES=1000,
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB,
    ):
        resp = _middleware()(_request("POST", "/activities/abc/post", 1001))
    assert resp.status_code == 413


# --- settings are read PER REQUEST for the video branch (not just at __init__) ---------------


def test_video_branch_setting_is_read_per_request_not_only_at_init(video_cohort_ok):
    mw = _middleware()
    with override_settings(MEDIA_VIDEO_ENABLED=False):
        resp = mw(_request("POST", "/activities/123/post", 50 * _MB))
    assert resp.status_code == 413  # video off: over the (default 8 MiB) global cap

    with override_settings(MEDIA_VIDEO_ENABLED=True, MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB):
        resp = mw(_request("POST", "/activities/123/post", 50 * _MB))
    assert resp.status_code == 200  # same instance, video now on: the larger cap applies


def test_real_endpoint_trailing_slash_path_gets_the_video_exemption(video_cohort_ok):
    """Regression for a review-caught bug: the registered route is "/activities/<id>/post/"
    WITH a trailing slash (apps/web/urls.py) and APPEND_SLASH never redirects a POST, so the
    exemption regex must match the trailing-slash form or every real video upload dies at the
    global 8 MiB cap before reaching the view."""
    with override_settings(
        MAX_REQUEST_BODY_BYTES=8 * _MB,
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB,
    ):
        resp = _middleware()(_request("POST", "/activities/123/post/", 50 * _MB))
    assert resp.status_code == 200


# --- the cohort gate itself (DB): the larger cap exists ONLY where video is possible ---------


def _activity(cohort_user):
    from django.contrib.gis.geos import Point
    from django.utils import timezone

    from apps.places.models import Place
    from apps.social.services import create_activity
    from apps.taxonomy.models import ActivityCategory, ActivityType

    place = Place.objects.create(
        name="Cap Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    category, _ = ActivityCategory.objects.get_or_create(slug="sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="basketball", defaults={"name": "Basketball", "category": category}
    )
    return create_activity(
        cohort_user, place=place, activity_type=atype, title="Cap", starts_at=timezone.now()
    )


def _user(username, age_band):
    from django.utils import timezone

    from apps.accounts.models import User

    user = User.objects.create_user(username=username, password="pw", age_band=age_band)
    user.recompute_cohort()
    user.is_identity_verified = True
    user.identity_verified_at = timezone.now()
    user.save()
    return user


@pytest.mark.django_db
def test_adult_cohort_activity_gets_the_video_cap():
    from apps.accounts.models import AgeBand

    activity = _activity(_user("cap-adult", AgeBand.ADULT))
    with override_settings(MEDIA_VIDEO_ENABLED=True, MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB):
        resp = _middleware()(_request("POST", f"/activities/{activity.pk}/post/", 50 * _MB))
    assert resp.status_code == 200


@pytest.mark.django_db
def test_minor_cohort_activity_keeps_the_global_cap():
    """Review finding: the widened pre-auth spool window must not exist for minor-cohort
    threads, where a video upload is impossible anyway."""
    from apps.accounts.models import AgeBand

    activity = _activity(
        _user(
            "cap-teen",
            AgeBand.AGE_16_17,
        )
    )
    with override_settings(
        MAX_REQUEST_BODY_BYTES=1000,
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB,
    ):
        resp = _middleware()(_request("POST", f"/activities/{activity.pk}/post/", 50 * _MB))
    assert resp.status_code == 413


@pytest.mark.django_db
def test_unknown_activity_id_keeps_the_global_cap():
    with override_settings(
        MAX_REQUEST_BODY_BYTES=1000,
        MEDIA_VIDEO_ENABLED=True,
        MEDIA_VIDEO_MAX_UPLOAD_BYTES=80 * _MB,
    ):
        resp = _middleware()(_request("POST", "/activities/999999/post/", 50 * _MB))
    assert resp.status_code == 413
