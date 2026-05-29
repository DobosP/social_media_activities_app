"""Image validation and metadata stripping. Re-encoding from raw pixels drops all
EXIF/GPS and other metadata — a privacy/safety requirement for every upload path."""

from io import BytesIO

from PIL import Image, UnidentifiedImageError

ALLOWED_FORMATS = {"PNG", "JPEG", "WEBP"}
_EXT = {"PNG": "png", "JPEG": "jpg", "WEBP": "webp"}

# Decompression-bomb ceiling: a small file can declare enormous dimensions that explode
# into gigabytes of raw pixels when decoded. We reject on the header-declared pixel count
# BEFORE allocating/decoding, so a malicious upload can never OOM the (ASGI) worker.
# Default ≈ 30 MP — comfortably above any real phone photo, far below a bomb.
DEFAULT_MAX_PIXELS = 30_000_000


class ImageError(ValueError):
    """Upload is not a valid/allowed image or is too large."""


def validate_and_strip(
    data: bytes,
    *,
    max_bytes: int,
    max_dimension: int | None = None,
    max_pixels: int = DEFAULT_MAX_PIXELS,
):
    """Validate size/format, then return (clean_bytes, format, (w, h)) with metadata
    removed and the image downscaled to fit `max_dimension` (longest side) if given.

    Guards against decompression bombs: the header-declared pixel count is checked
    against `max_pixels` before any pixel data is decoded, and Pillow's own bomb guard
    is armed as a second line of defence."""
    if len(data) > max_bytes:
        raise ImageError(f"Image exceeds the {max_bytes}-byte limit.")
    try:
        with Image.open(BytesIO(data)) as probe:
            fmt = probe.format
            width, height = probe.size  # from the header — no pixels decoded yet
            if width * height > max_pixels:
                raise ImageError("Image pixel dimensions exceed the allowed budget.")
            probe.verify()  # detects truncated/corrupt files
    except Image.DecompressionBombError as exc:
        raise ImageError("Image is too large to process safely.") from exc
    except (UnidentifiedImageError, OSError) as exc:
        raise ImageError("File is not a readable image.") from exc

    if fmt not in ALLOWED_FORMATS:
        raise ImageError(f"Unsupported image format: {fmt}.")

    # Reopen (verify() leaves the image unusable) and rebuild from pixels only. Arm
    # Pillow's own bomb guard so an oversized decode raises instead of OOMing the worker.
    previous_cap = Image.MAX_IMAGE_PIXELS
    Image.MAX_IMAGE_PIXELS = max_pixels
    try:
        with Image.open(BytesIO(data)) as img:
            img.load()
            # Rebuild from raw pixels only: drops EXIF/GPS and any other metadata.
            clean = Image.frombytes(img.mode, img.size, img.tobytes())
            if max_dimension and max(clean.size) > max_dimension:
                clean.thumbnail((max_dimension, max_dimension))
            size = clean.size
            out = BytesIO()
            clean.save(out, format=fmt)
    except Image.DecompressionBombError as exc:
        raise ImageError("Image is too large to process safely.") from exc
    finally:
        Image.MAX_IMAGE_PIXELS = previous_cap
    return out.getvalue(), fmt, size


def extension_for(fmt: str) -> str:
    return _EXT.get(fmt, "bin")
