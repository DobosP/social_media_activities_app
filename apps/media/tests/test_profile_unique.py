"""Profile pictures must be unique by content (first pass: byte-identical stored content)."""

from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.core.cache import cache
from django.core.files.uploadedfile import SimpleUploadedFile
from django.test import override_settings
from PIL import Image
from rest_framework.test import APIClient

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media.models import Photo
from apps.media.services import DuplicateProfileImage, profile_image_is_taken, upload_photo
from apps.places.models import Place
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _png(color=(10, 120, 200), size=(8, 8)) -> bytes:
    out = BytesIO()
    Image.new("RGB", size, color).save(out, format="PNG")
    return out.getvalue()


def _activity(owner):
    from apps.social.services import create_activity

    cat, _ = ActivityCategory.objects.get_or_create(slug="pu-sport", defaults={"name": "Sport"})
    atype, _ = ActivityType.objects.get_or_create(
        slug="pu-bball", defaults={"name": "Basketball", "category": cat}
    )
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    from django.utils import timezone

    return create_activity(
        owner, place=place, activity_type=atype, title="Game", starts_at=timezone.now()
    )


def _child(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.UNDER_16, provider="dev"))
    return u  # cohort == CHILD


def test_duplicate_profile_image_is_rejected():
    a, b = _user("dup_a"), _user("dup_b")  # same (adult) cohort
    img = _png()
    upload_photo(a, Photo.Kind.PROFILE, img)
    with pytest.raises(DuplicateProfileImage):
        upload_photo(b, Photo.Kind.PROFILE, img)
    assert not Photo.objects.filter(uploader=b, kind=Photo.Kind.PROFILE).exists()


def test_same_image_allowed_across_cohorts():
    # The uniqueness check is cohort-scoped, so an adult and a child may share content — and
    # an adult can never probe/collide against a child's avatar across the cohort wall.
    adult, child = _user("xc_adult"), _child("xc_child")
    img = _png()
    upload_photo(adult, Photo.Kind.PROFILE, img)
    photo = upload_photo(child, Photo.Kind.PROFILE, img)  # different cohort → allowed
    assert photo.kind == Photo.Kind.PROFILE
    assert Photo.objects.filter(kind=Photo.Kind.PROFILE).count() == 2


def test_distinct_profile_images_are_allowed():
    a, b = _user("uniq_a"), _user("uniq_b")
    upload_photo(a, Photo.Kind.PROFILE, _png())
    upload_photo(b, Photo.Kind.PROFILE, _png(color=(1, 2, 3)))  # different content
    assert Photo.objects.filter(kind=Photo.Kind.PROFILE).count() == 2


def test_reuploading_your_own_identical_image_is_allowed():
    a = _user("self_a")
    img = _png()
    first = upload_photo(a, Photo.Kind.PROFILE, img)
    second = upload_photo(a, Photo.Kind.PROFILE, img)  # replacing your own avatar
    assert first.id != second.id
    assert Photo.objects.filter(uploader=a, kind=Photo.Kind.PROFILE).count() == 1


def test_profile_uniqueness_ignores_thread_photos():
    a, b = _user("thread_a"), _user("thread_b")
    activity = _activity(a)  # a is owner-member of the thread
    img = _png()
    upload_photo(a, Photo.Kind.THREAD, img, thread=activity.thread)
    # The same content as a thread photo does NOT block a profile picture (profile-only rule).
    photo = upload_photo(b, Photo.Kind.PROFILE, img)
    assert photo.kind == Photo.Kind.PROFILE


def test_profile_image_is_taken_seam():
    a, b = _user("seam_a"), _user("seam_b")
    p = upload_photo(a, Photo.Kind.PROFILE, _png())
    assert profile_image_is_taken(b, p.sha256) is True  # taken by another user
    assert profile_image_is_taken(a, p.sha256) is False  # the owner is excluded
    assert profile_image_is_taken(b, "") is False  # no digest → never "taken"


def test_api_duplicate_profile_returns_400():
    cache.clear()  # isolate from the avatar_upload rate-limit counter
    a, b = _user("api_a"), _user("api_b")
    img = _png()
    upload_photo(a, Photo.Kind.PROFILE, img)
    client = APIClient()
    client.force_authenticate(b)
    resp = client.post(
        "/api/media/photos/",
        {"file": SimpleUploadedFile("p.png", img, content_type="image/png"), "kind": "profile"},
        format="multipart",
    )
    assert resp.status_code == 400, resp.content
    assert not Photo.objects.filter(uploader=b, kind=Photo.Kind.PROFILE).exists()


@override_settings(AVATAR_UPLOAD_RATE_LIMIT=1, AVATAR_UPLOAD_RATE_WINDOW_SECONDS=3600)
def test_api_avatar_upload_is_rate_limited():
    cache.clear()
    u = _user("rl_api")
    client = APIClient()
    client.force_authenticate(u)
    first = client.post(
        "/api/media/photos/",
        {"file": SimpleUploadedFile("a.png", _png(color=(5, 5, 5))), "kind": "profile"},
        format="multipart",
    )
    assert first.status_code == 201, first.content
    throttled = client.post(
        "/api/media/photos/",
        {"file": SimpleUploadedFile("b.png", _png(color=(6, 6, 6))), "kind": "profile"},
        format="multipart",
    )
    assert throttled.status_code == 429, throttled.content
