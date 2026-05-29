"""Media hardening regressions:

* W1-11 — decompression-bomb guard: an image whose declared pixel count exceeds the
  budget is rejected before any pixels are decoded (so it can't OOM the worker).
* W1-6  — no orphaned blobs: deleting a user (GDPR erasure cascade) removes the backing
  storage object, not just the DB row. See docs/PRODUCTION_HARDENING_PLAN_2026-05.md."""

from io import BytesIO

import pytest
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media.models import Photo
from apps.media.processing import ImageError, validate_and_strip
from apps.media.services import upload_photo
from apps.media.storage import get_storage

pytestmark = pytest.mark.django_db


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


def test_user_deletion_erases_media_blob():
    user = User.objects.create_user(username="blob_owner", password="pw")
    apply_assurance(user, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    photo = upload_photo(user, Photo.Kind.PROFILE, _png())
    key = photo.storage_key
    assert get_storage().exists(key) is True

    user.delete()  # cascades Photo delete; the pre_delete signal must remove the blob

    assert Photo.objects.filter(id=photo.id).exists() is False
    assert get_storage().exists(key) is False
