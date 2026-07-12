"""Eager rendition pipeline (ADR-0026 §1-2): AVIF as a selectable output codec, the one eager
thumbnail rendition generated at upload time for Photo/ActivityCover/Attachment, and the
per-viewer signed-URL variant serving (thumb falls back to the full object when a row has none).
"""

from io import BytesIO

import pytest
from django.contrib.gis.geos import Point
from django.test import Client, override_settings
from PIL import Image, features

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media.models import Attachment, Photo
from apps.media.processing import make_thumbnail, validate_and_strip
from apps.media.services import (
    _image_encode_params,
    activity_visual,
    attach_to_post,
    attachments_for_posts,
    delete_photo,
    resolve_activity_cover_token,
    signed_url,
    upload_activity_cover,
    upload_photo,
)
from apps.media.storage import get_storage
from apps.ops.models import DeferredTask
from apps.places.models import Place
from apps.social import services as social
from apps.taxonomy.models import ActivityCategory, ActivityType

pytestmark = pytest.mark.django_db


@pytest.fixture(autouse=True)
def _media_root(tmp_path, settings):
    settings.MEDIA_ROOT = tmp_path / "media"


def _png_bytes(size=(64, 48), color=(10, 120, 200)) -> bytes:
    out = BytesIO()
    Image.new("RGB", size, color).save(out, format="PNG")
    return out.getvalue()


def _user(name, band=AgeBand.ADULT):
    u = User.objects.create_user(username=name, password="pw", display_name=name.title())
    apply_assurance(u, AssuranceResult(age_band=band, provider="dev"))
    return u


def _type(slug="rend-bball"):
    cat, _ = ActivityCategory.objects.get_or_create(slug="rend-sport", defaults={"name": "Sport"})
    t, _ = ActivityType.objects.get_or_create(
        slug=slug, defaults={"name": "Basketball", "category": cat}
    )
    return t


def _activity(owner, slug="rend-bball"):
    from django.utils import timezone

    place = Place.objects.create(
        name="Rendition Court", location=Point(23.6, 46.77, srid=4326), source=Place.Source.OSM
    )
    return social.create_activity(
        owner,
        place=place,
        activity_type=_type(slug),
        title="Game",
        starts_at=timezone.now() + timezone.timedelta(days=1),
    )


def _client(user):
    c = Client()
    c.force_login(user)
    return c


# --- make_thumbnail unit tests (no DB/settings involvement — pure PIL) -----------------------


def test_make_thumbnail_downscales_a_large_source():
    clean, fmt, _size = validate_and_strip(
        _png_bytes(size=(1600, 1200)), max_bytes=50_000_000, output_format="WEBP"
    )
    assert fmt == "WEBP"
    result = make_thumbnail(clean, max_dimension=800, quality=80)
    assert result is not None
    thumb_bytes, (w, h) = result
    assert (w, h) == (800, 600)
    reopened = Image.open(BytesIO(thumb_bytes))
    assert reopened.format == "WEBP"
    assert reopened.size == (800, 600)


def test_make_thumbnail_returns_none_when_source_already_fits():
    clean, fmt, _size = validate_and_strip(
        _png_bytes(size=(400, 300)), max_bytes=50_000_000, output_format="WEBP"
    )
    assert make_thumbnail(clean, max_dimension=800, quality=80) is None


def test_make_thumbnail_returns_none_for_zero_or_none_max_dimension():
    clean, _fmt, _size = validate_and_strip(
        _png_bytes(size=(1600, 1200)), max_bytes=50_000_000, output_format="WEBP"
    )
    assert make_thumbnail(clean, max_dimension=0, quality=80) is None
    assert make_thumbnail(clean, max_dimension=None, quality=80) is None


def test_make_thumbnail_never_raises_on_junk_bytes():
    assert make_thumbnail(b"not an image at all", max_dimension=800, quality=80) is None


# --- AVIF encode path ---------------------------------------------------------------------


@pytest.mark.skipif(not features.check("avif"), reason="Pillow build lacks AVIF support")
def test_upload_photo_encodes_avif_when_configured():
    user = _user("avif_up")
    with override_settings(MEDIA_IMAGE_OUTPUT_FORMAT="AVIF", MEDIA_IMAGE_QUALITY=0):
        photo = upload_photo(user, Photo.Kind.PROFILE, _png_bytes(size=(1600, 1200)))
    assert photo.content_type == "image/avif"
    assert photo.storage_key.endswith(".avif")
    stored = get_storage().open(photo.storage_key)
    assert len(stored) > 0
    reopened = Image.open(BytesIO(stored))
    assert reopened.format == "AVIF"


# --- _image_encode_params: auto vs explicit quality per codec -----------------------------


def test_image_encode_params_auto_quality_avif():
    with override_settings(MEDIA_IMAGE_OUTPUT_FORMAT="AVIF", MEDIA_IMAGE_QUALITY=0):
        fmt, quality = _image_encode_params()
    assert fmt == "AVIF"
    assert quality == 64


def test_image_encode_params_auto_quality_webp():
    with override_settings(MEDIA_IMAGE_OUTPUT_FORMAT="WEBP", MEDIA_IMAGE_QUALITY=0):
        fmt, quality = _image_encode_params()
    assert fmt == "WEBP"
    assert quality == 80


def test_image_encode_params_explicit_quality_wins_for_every_codec():
    with override_settings(MEDIA_IMAGE_OUTPUT_FORMAT="AVIF", MEDIA_IMAGE_QUALITY=55):
        fmt, quality = _image_encode_params()
    assert fmt == "AVIF"
    assert quality == 55

    with override_settings(MEDIA_IMAGE_OUTPUT_FORMAT="WEBP", MEDIA_IMAGE_QUALITY=55):
        fmt, quality = _image_encode_params()
    assert fmt == "WEBP"
    assert quality == 55


# --- upload_photo: eager thumb rendition ----------------------------------------------------


def test_upload_photo_large_source_gets_a_thumb():
    user = _user("thumb_big")
    photo = upload_photo(user, Photo.Kind.PROFILE, _png_bytes(size=(1600, 1200)))
    assert photo.thumb_storage_key != ""
    assert photo.thumb_storage_key.startswith("thumbs/")
    assert get_storage().exists(photo.thumb_storage_key) is True
    stored_thumb = get_storage().open(photo.thumb_storage_key)
    reopened = Image.open(BytesIO(stored_thumb))
    assert max(reopened.size) == 800  # MEDIA_THUMB_DIMENSION default


def test_upload_photo_small_source_gets_no_thumb():
    user = _user("thumb_small")
    photo = upload_photo(user, Photo.Kind.PROFILE, _png_bytes(size=(320, 240)))
    assert photo.thumb_storage_key == ""


# --- signed_url variant serving --------------------------------------------------------------


def test_thumb_variant_serves_a_smaller_body_than_full():
    user = _user("serve_big")
    photo = upload_photo(user, Photo.Kind.PROFILE, _png_bytes(size=(1600, 1200)))
    client = _client(user)

    thumb_resp = client.get(signed_url(photo, user, variant="thumb"))
    full_resp = client.get(signed_url(photo, user, variant="full"))

    assert thumb_resp.status_code == 200
    assert full_resp.status_code == 200
    assert len(thumb_resp.content) < len(full_resp.content)
    assert "private" in thumb_resp["Cache-Control"]
    assert thumb_resp["X-Content-Type-Options"] == "nosniff"


def test_thumb_variant_falls_back_to_full_object_when_no_thumb_row():
    user = _user("serve_small")
    photo = upload_photo(user, Photo.Kind.PROFILE, _png_bytes(size=(320, 240)))
    assert photo.thumb_storage_key == ""
    client = _client(user)

    thumb_resp = client.get(signed_url(photo, user, variant="thumb"))
    full_resp = client.get(signed_url(photo, user, variant="full"))

    assert thumb_resp.status_code == 200 and full_resp.status_code == 200
    assert len(thumb_resp.content) == len(full_resp.content)


# --- upload_activity_cover: eager thumb + replace cleanup -----------------------------------


def test_activity_cover_large_source_gets_a_thumb():
    owner = _user("cover_big_owner")
    activity = _activity(owner)
    cover = upload_activity_cover(owner, activity, _png_bytes(size=(1600, 1200)))
    assert cover.thumb_storage_key != ""
    assert get_storage().exists(cover.thumb_storage_key) is True


def test_activity_cover_replace_deletes_old_thumb_after_commit(django_capture_on_commit_callbacks):
    owner = _user("cover_replace_owner")
    activity = _activity(owner)

    with django_capture_on_commit_callbacks(execute=True):
        first = upload_activity_cover(
            owner, activity, _png_bytes(size=(1600, 1200), color=(1, 1, 1))
        )
    old_thumb = first.thumb_storage_key
    assert old_thumb != ""
    assert get_storage().exists(old_thumb) is True

    with django_capture_on_commit_callbacks(execute=True):
        second = upload_activity_cover(
            owner, activity, _png_bytes(size=(1600, 1200), color=(2, 2, 2))
        )

    assert second.thumb_storage_key != old_thumb
    assert get_storage().exists(old_thumb) is False


def test_activity_visual_serves_the_thumb_variant():
    owner = _user("visual_owner")
    activity = _activity(owner)
    cover = upload_activity_cover(owner, activity, _png_bytes(size=(1600, 1200)))

    visual = activity_visual(activity, owner)
    assert visual["kind"] == "activity_cover_photo"
    token = visual["url"].rsplit("/", 2)[1]
    resolved_cover, variant = resolve_activity_cover_token(token, owner)
    assert resolved_cover.id == cover.id
    assert variant == "thumb"


# --- attach_to_post: image branch thumb -------------------------------------------------------


def test_attach_image_thumb_generated_and_served_distinctly():
    owner = _user("attach_owner")
    activity = _activity(owner)
    post = social.post_to_thread(owner, activity, "big pic")
    att = attach_to_post(owner, post, filename="x.png", data=_png_bytes(size=(1600, 1200)))
    assert att.kind == Attachment.Kind.IMAGE
    assert att.thumb_storage_key != ""

    by_post = attachments_for_posts([post], owner)
    served = by_post[post.id][0]
    assert served.thumb_url != ""
    assert served.url != ""
    assert served.thumb_url != served.url


# --- delete_photo blob cleanup for both renditions --------------------------------------------


def test_delete_photo_enqueues_cleanup_for_main_and_thumb_keys():
    user = _user("delete_owner")
    photo = upload_photo(user, Photo.Kind.PROFILE, _png_bytes(size=(1600, 1200)))
    main_key, thumb_key = photo.storage_key, photo.thumb_storage_key
    assert main_key and thumb_key

    delete_photo(user, photo)

    payloads = DeferredTask.objects.filter(kind="erasure.blob_cleanup").values_list(
        "payload", flat=True
    )
    flat_keys = {key for payload in payloads for key in payload["blob_keys"]}
    assert main_key in flat_keys
    assert thumb_key in flat_keys
