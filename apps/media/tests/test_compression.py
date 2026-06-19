"""Smart image compression (W8 storage): every uploaded image is transcoded to a compact codec
(WebP by default) at a tuned quality, with EXIF orientation baked in and all metadata stripped — so
private blobs stay small (cheap EU object storage + less egress) with no separate thumbnail object.
"""

import io
import os

import pytest
from PIL import Image

from apps.media.processing import validate_and_strip


def _img_bytes(fmt, *, size=(64, 48), mode="RGB", color=(180, 90, 30), exif=None):
    img = Image.new(mode, size, color)
    buf = io.BytesIO()
    kw = {"exif": exif} if exif is not None else {}
    img.save(buf, format=fmt, **kw)
    return buf.getvalue()


def _noisy_png(size=(512, 512)):
    # Pure noise: incompressible for lossless PNG, the clearest case for lossy WebP winning.
    img = Image.frombytes("RGB", size, os.urandom(size[0] * size[1] * 3))
    buf = io.BytesIO()
    img.save(buf, format="PNG")
    return buf.getvalue()


def test_preserve_source_format_by_default():
    clean, fmt, _ = validate_and_strip(_img_bytes("PNG"), max_bytes=10_000_000)
    assert fmt == "PNG"
    assert Image.open(io.BytesIO(clean)).format == "PNG"


def test_transcode_to_webp():
    clean, fmt, _ = validate_and_strip(
        _img_bytes("PNG"), max_bytes=10_000_000, output_format="WEBP"
    )
    assert fmt == "WEBP"
    assert Image.open(io.BytesIO(clean)).format == "WEBP"


def test_webp_is_smaller_than_source_png_for_a_photographic_image():
    src = _noisy_png()
    webp, _, _ = validate_and_strip(src, max_bytes=10_000_000, output_format="WEBP", quality=80)
    assert len(webp) < len(src)


def test_downscales_to_max_dimension():
    src = _img_bytes("PNG", size=(4000, 3000))
    _, _, (w, h) = validate_and_strip(
        src, max_bytes=50_000_000, max_dimension=2048, output_format="WEBP"
    )
    assert max(w, h) == 2048


def test_long_thin_image_is_clamped_not_a_raw_encoder_error():
    # 20000x10 is only 200k px (under the 30 MP bomb budget) but past WebP's 16383px/side limit.
    # With NO max_dimension configured it must still encode (clamped to the codec limit), never
    # raise a raw PIL ValueError that would 500 the attachment upload.
    src = _img_bytes("PNG", size=(20000, 10))
    clean, fmt, (w, h) = validate_and_strip(
        src, max_bytes=50_000_000, max_dimension=None, output_format="WEBP"
    )
    assert fmt == "WEBP" and max(w, h) <= 16383
    assert Image.open(io.BytesIO(clean)).format == "WEBP"


def test_exif_orientation_is_baked_in_then_stripped():
    # A landscape image tagged "rotate 90 CW" (orientation 6) must come out PORTRAIT, with the EXIF
    # removed — so it never displays sideways once the tag is gone, and no GPS/EXIF survives.
    exif = Image.Exif()
    exif[0x0112] = 6  # Orientation
    src = _img_bytes("JPEG", size=(64, 48), exif=exif.tobytes())
    clean, _, (w, h) = validate_and_strip(src, max_bytes=10_000_000, output_format="WEBP")
    assert (w, h) == (48, 64)  # dimensions swapped -> orientation applied
    assert "exif" not in Image.open(io.BytesIO(clean)).info  # metadata gone


def test_rgba_png_keeps_alpha_when_transcoded_to_webp():
    src = _img_bytes("PNG", mode="RGBA", color=(10, 20, 30, 128))
    clean, _, _ = validate_and_strip(src, max_bytes=10_000_000, output_format="WEBP")
    out = Image.open(io.BytesIO(clean))
    assert out.format == "WEBP" and out.mode in ("RGBA", "LA")  # alpha preserved


def test_rgba_to_jpeg_flattens_without_error():
    src = _img_bytes("PNG", mode="RGBA", color=(10, 20, 30, 0))
    clean, fmt, _ = validate_and_strip(src, max_bytes=10_000_000, output_format="JPEG")
    out = Image.open(io.BytesIO(clean))
    assert fmt == "JPEG" and out.format == "JPEG" and out.mode == "RGB"


def test_palette_png_transcodes_without_losing_colour():
    # A "P" (palette) PNG must convert correctly (the old from-pixels rebuild would have dropped the
    # palette); transcoding to WebP RGB keeps the colour.
    pal = Image.new("RGB", (32, 32), (200, 40, 40)).convert("P")
    buf = io.BytesIO()
    pal.save(buf, format="PNG")
    clean, _, _ = validate_and_strip(buf.getvalue(), max_bytes=10_000_000, output_format="WEBP")
    out = Image.open(io.BytesIO(clean)).convert("RGB")
    r, g, b = out.getpixel((16, 16))
    assert r > 150 and g < 100 and b < 100  # still red, not garbled


@pytest.mark.django_db
def test_upload_photo_stores_webp_by_default():
    # The default MEDIA_IMAGE_OUTPUT_FORMAT=WEBP means a PNG profile upload is stored as WebP, so
    # the whole upload pipeline (photos AND attachments share it) benefits from smart compression.
    from apps.accounts.identity.base import AssuranceResult
    from apps.accounts.models import AgeBand, User
    from apps.accounts.services import apply_assurance
    from apps.media import services as media
    from apps.media.models import Photo

    u = User.objects.create_user(username="webp_up", password="pw", display_name="Webp")
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))

    photo = media.upload_photo(u, Photo.Kind.PROFILE, _img_bytes("PNG"))
    assert photo.content_type == "image/webp"
    assert photo.storage_key.endswith(".webp")
