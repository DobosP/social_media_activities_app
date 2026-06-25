"""Media hardening regressions:

* W1-11 — decompression-bomb guard: an image whose declared pixel count exceeds the
  budget is rejected before any pixels are decoded (so it can't OOM the worker).
* W1-6  — no orphaned blobs: deleting a user (GDPR erasure cascade) removes the backing
  storage object, not just the DB row. See docs/PRODUCTION_HARDENING_PLAN_2026-05.md."""

from datetime import timedelta
from io import BytesIO
from uuid import uuid4

import pytest
from django.contrib.gis.geos import Point
from django.db import transaction
from django.utils import timezone
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media.models import Attachment, Photo
from apps.media.processing import ImageError, validate_and_strip
from apps.media.services import upload_photo
from apps.media.storage import get_storage
from apps.places.models import Place
from apps.social import services as social
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


@pytest.fixture(autouse=True)
def _writable_media_root(tmp_path, settings):
    """Keep local media blobs in a writable pytest temp dir.

    The Docker test process may run from `/app` as an unprivileged user, where
    the default dev `MEDIA_ROOT=/app/media` is not writable. These tests only
    need the local storage backend contract, so isolate blobs per test.
    """
    settings.MEDIA_ROOT = tmp_path / "media"


def _png(size=(8, 8), color=(10, 120, 200)) -> bytes:
    out = BytesIO()
    Image.new("RGB", size, color).save(out, format="PNG")
    return out.getvalue()


def test_rejects_image_over_pixel_budget():
    data = _png(size=(2000, 2000))  # 4,000,000 px
    with pytest.raises(ImageError):
        validate_and_strip(data, max_bytes=10 * 1024 * 1024, max_pixels=1_000_000)


def test_accepts_normal_image_under_pixel_budget():
    clean, fmt, size = validate_and_strip(
        _png(size=(100, 100)), max_bytes=10 * 1024 * 1024, max_dimension=2048, max_pixels=30_000_000
    )
    assert fmt == "PNG"
    assert size == (100, 100)


@pytest.mark.django_db(transaction=True)
def test_user_deletion_erases_media_blob():
    user = User.objects.create_user(username="blob_owner", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    photo = upload_photo(user, Photo.Kind.PROFILE, _png())
    key = photo.storage_key
    assert get_storage().exists(key) is True

    user.delete()  # cascades Photo delete; the pre_delete signal must remove the blob

    assert Photo.objects.filter(id=photo.id).exists() is False
    assert get_storage().exists(key) is False


@pytest.mark.django_db(transaction=True)
def test_rolled_back_user_deletion_keeps_media_blob():
    user = User.objects.create_user(username="rollback_blob_owner", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    photo = upload_photo(user, Photo.Kind.PROFILE, _png())
    key = photo.storage_key
    assert get_storage().exists(key) is True

    with pytest.raises(RuntimeError):
        with transaction.atomic():
            user.delete()
            raise RuntimeError("abort deletion")

    assert Photo.objects.filter(id=photo.id).exists() is True
    assert get_storage().exists(key) is True


@pytest.mark.django_db(transaction=True)
def test_user_deletion_erases_attachment_blob():
    user = User.objects.create_user(username="attachment_blob_owner", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    place = Place.objects.create(
        name="Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    category = ActivityCategory.objects.create(slug="hardening-sport", name="Sport")
    activity_type = ActivityType.objects.create(
        slug="hardening-basketball", name="Basketball", category=category
    )
    activity = social.create_activity(
        user,
        place=place,
        activity_type=activity_type,
        title="Game",
        starts_at=timezone.now() + timedelta(days=1),
    )
    post = social.post_to_thread(user, activity, "attached")
    key = f"hardening-attachment-{uuid4().hex}.png"
    get_storage().save(key, b"clean bytes", content_type="image/png")
    attachment = Attachment.objects.create(
        post=post,
        uploader=user,
        kind=Attachment.Kind.IMAGE,
        storage_key=key,
        content_type="image/png",
    )
    assert get_storage().exists(key) is True

    user.delete()  # cascades Attachment delete; the pre_delete signal must remove the blob

    assert Attachment.objects.filter(id=attachment.id).exists() is False
    assert get_storage().exists(key) is False
