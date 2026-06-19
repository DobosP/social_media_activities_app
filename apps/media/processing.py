"""Image validation, metadata stripping, and smart compression. Re-encoding from raw pixels
drops all EXIF/GPS and other metadata (a privacy/safety requirement for every upload path) and,
when an ``output_format`` is given, transcodes to a compact codec (WebP by default) at a tuned
quality so private blobs stay small — cheaper EU object storage + less egress, with no separate
thumbnail/object to manage (one upload = one stored object, the existing design).

This module is deliberately settings-free (PIL only): callers pass the knobs, so it stays a pure,
unit-testable function."""

from io import BytesIO

from PIL import Image, ImageOps, UnidentifiedImageError

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
    quality: int = 82,
    output_format: str | None = None,
):
    """Validate size/format, then return ``(clean_bytes, format, (w, h))`` — metadata removed,
    EXIF orientation baked in, downscaled to fit ``max_dimension`` (longest side) if given, and
    re-encoded.

    ``output_format`` (``"WEBP"`` / ``"JPEG"`` / ``"PNG"``) transcodes to that codec; ``None``
    preserves the source format (back-compatible default). ``quality`` (1–100) drives the lossy
    encoders. WebP at a moderate quality is dramatically smaller than the source PNG/JPEG for a
    phone photo, so it is the recommended ``output_format`` for cheap object storage.

    Guards against decompression bombs: the header-declared pixel count is checked against
    ``max_pixels`` before any pixel data is decoded, and Pillow's own bomb guard is armed as a
    second line of defence."""
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
    target = (output_format or fmt).upper()
    if target not in ALLOWED_FORMATS:
        raise ImageError(f"Unsupported output format: {target}.")

    # Reopen (verify() leaves the image unusable) and rebuild from pixels only. Arm Pillow's own
    # bomb guard so an oversized decode raises instead of OOMing the worker.
    previous_cap = Image.MAX_IMAGE_PIXELS
    Image.MAX_IMAGE_PIXELS = max_pixels
    try:
        with Image.open(BytesIO(data)) as img:
            img.load()
            # Bake EXIF orientation into the pixels BEFORE we drop EXIF, so a portrait phone photo
            # doesn't end up sideways once its orientation tag is stripped.
            img = ImageOps.exif_transpose(img)
            # Normalise the colour mode for the target codec (handles palette/CMYK/alpha) and
            # downscale FIRST so the metadata rebuild runs on the smaller pixel buffer.
            base, mode = _prepare(img, target)
            # Downscale to the SMALLER of the configured cap and the target codec's hard per-side
            # limit (WebP 16383 / JPEG 65500), so a long-thin image (e.g. 20000x10 — under the
            # pixel-bomb budget but past WebP's limit) is shrunk to fit instead of blowing up the
            # encoder, even when no max_dimension is configured.
            effective_max = _effective_max(max_dimension, _CODEC_MAX_SIDE.get(target))
            if effective_max and max(base.size) > effective_max:
                base.thumbnail((effective_max, effective_max))
            # Rebuild from raw pixels only: a fresh image with NO source metadata (.info/EXIF/GPS).
            clean = Image.frombytes(mode, base.size, base.tobytes())
            size = clean.size
            out = BytesIO()
            _encode(clean, out, target, quality)
    except Image.DecompressionBombError as exc:
        raise ImageError("Image is too large to process safely.") from exc
    finally:
        Image.MAX_IMAGE_PIXELS = previous_cap
    return out.getvalue(), target, size


def _prepare(img, target):
    """Return ``(image, mode)`` in a colour mode the target codec can encode: JPEG has no alpha
    (transparency is flattened onto white); WebP/PNG keep alpha when the source had it. Palette/
    CMYK/other modes are converted to RGB(A)."""
    has_alpha = img.mode in ("RGBA", "LA", "PA") or (img.mode == "P" and "transparency" in img.info)
    if target == "JPEG":
        if has_alpha:
            rgba = img.convert("RGBA")
            flat = Image.new("RGB", rgba.size, (255, 255, 255))
            flat.paste(rgba, mask=rgba.split()[-1])
            return flat, "RGB"
        return img.convert("RGB"), "RGB"
    mode = "RGBA" if has_alpha else "RGB"
    return img.convert(mode), mode


# Hard per-side encoder limits. Larger uploads are downscaled to fit (above), so a valid photo
# encodes instead of raising; the try/except in _encode is the belt-and-suspenders net for anything
# else the encoder rejects.
_CODEC_MAX_SIDE = {"WEBP": 16383, "JPEG": 65500}


def _effective_max(max_dimension, codec_cap):
    """The longest-side cap to apply: the smaller of the configured ``max_dimension`` and the
    target codec's hard limit (either may be None)."""
    candidates = [d for d in (max_dimension, codec_cap) if d]
    return min(candidates) if candidates else None


def _encode(img, out, target, quality):
    """Encode with the smart-compression knobs per codec. No ``exif=``/``icc_profile=`` is passed,
    so re-encoding drops metadata on top of the from-pixels rebuild. ANY encoder failure (a raw PIL
    ValueError/OSError, e.g. an image past a codec's per-side limit) is normalised to ``ImageError``
    so a caller's existing ImageError -> MediaRejected mapping rejects it cleanly, never a 500."""
    try:
        if target == "WEBP":
            # method=6 = slowest/best compression (fine for a one-shot upload).
            img.save(out, format="WEBP", quality=quality, method=6)
        elif target == "JPEG":
            img.save(out, format="JPEG", quality=quality, optimize=True, progressive=True)
        else:  # PNG is lossless — quality is irrelevant; optimise the deflate stream.
            img.save(out, format="PNG", optimize=True)
    except (ValueError, OSError) as exc:
        raise ImageError("Could not encode the image.") from exc


def extension_for(fmt: str) -> str:
    return _EXT.get(fmt, "bin")
