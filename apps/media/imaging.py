import io

from PIL import Image

# Formats we accept, mapped to a canonical content type. Re-encoding to these
# formats is what strips EXIF/GPS and other embedded metadata.
SUPPORTED_FORMATS = {
    "JPEG": "image/jpeg",
    "PNG": "image/png",
    "WEBP": "image/webp",
}


class InvalidImage(Exception):
    """The uploaded bytes are not a usable image in a supported format."""


class ProcessedImage:
    def __init__(self, data: bytes, content_type: str, width: int, height: int):
        self.data = data
        self.content_type = content_type
        self.width = width
        self.height = height


def process_image(data: bytes) -> ProcessedImage:
    """Validate that `data` is a supported image and return a re-encoded copy with
    all metadata (EXIF, GPS, etc.) stripped.

    Re-encoding from pixel data only — EXIF/GPS is a privacy and de-anonymization
    risk for minors, so it never reaches storage. See docs/SAFETY.md.
    """
    try:
        with Image.open(io.BytesIO(data)) as img:
            img.verify()
        with Image.open(io.BytesIO(data)) as img:
            fmt = img.format
            if fmt not in SUPPORTED_FORMATS:
                raise InvalidImage(f"Unsupported image format: {fmt}.")
            img = img.convert("RGBA" if fmt == "PNG" else "RGB")
            width, height = img.size
            # A fresh image carries no metadata from the original (paste copies
            # pixels only, not the EXIF/info dict).
            clean = Image.new(img.mode, img.size)
            clean.paste(img)
            out = io.BytesIO()
            clean.save(out, format=fmt)
    except InvalidImage:
        raise
    except Exception as exc:  # noqa: BLE001 - Pillow raises a variety of errors
        raise InvalidImage(f"Not a valid image: {exc}") from exc

    return ProcessedImage(out.getvalue(), SUPPORTED_FORMATS[fmt], width, height)
