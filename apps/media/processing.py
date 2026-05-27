"""Image validation and metadata stripping. Re-encoding from raw pixels drops all
EXIF/GPS and other metadata — a privacy/safety requirement for every upload path."""

from io import BytesIO

from PIL import Image, UnidentifiedImageError

ALLOWED_FORMATS = {"PNG", "JPEG", "WEBP"}
_EXT = {"PNG": "png", "JPEG": "jpg", "WEBP": "webp"}


class ImageError(ValueError):
    """Upload is not a valid/allowed image or is too large."""


def validate_and_strip(data: bytes, *, max_bytes: int):
    """Validate size/format, then return (clean_bytes, format, (w, h)) with metadata removed."""
    if len(data) > max_bytes:
        raise ImageError(f"Image exceeds the {max_bytes}-byte limit.")
    try:
        with Image.open(BytesIO(data)) as probe:
            fmt = probe.format
            probe.verify()  # detects truncated/corrupt files
    except (UnidentifiedImageError, OSError) as exc:
        raise ImageError("File is not a readable image.") from exc

    if fmt not in ALLOWED_FORMATS:
        raise ImageError(f"Unsupported image format: {fmt}.")

    # Reopen (verify() leaves the image unusable) and rebuild from pixels only.
    with Image.open(BytesIO(data)) as img:
        img.load()
        size = img.size
        # Rebuild from raw pixels only: drops EXIF/GPS and any other metadata.
        clean = Image.frombytes(img.mode, size, img.tobytes())
        out = BytesIO()
        clean.save(out, format=fmt)
    return out.getvalue(), fmt, size


def extension_for(fmt: str) -> str:
    return _EXT.get(fmt, "bin")
