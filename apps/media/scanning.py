import hashlib
from dataclasses import dataclass

from django.conf import settings
from django.utils.module_loading import import_string


@dataclass
class ScanResult:
    allowed: bool
    reason: str = ""


class ImageScanner:
    """Safety-screening seam, run before an image is ever visible.

    Production swaps in a real CSAM hash-matching / classifier service here (where
    lawful) via settings.MEDIA_IMAGE_SCANNER. Keeping it pluggable means the lawful
    scanning posture can change without touching upload/storage. See docs/SAFETY.md.
    """

    def scan(self, *, data: bytes, content_type: str) -> ScanResult:
        raise NotImplementedError


class HashBlocklistScanner(ImageScanner):
    """Default scanner: reject images whose SHA-256 is on a known-bad blocklist.

    This is the shape a real perceptual/CSAM hash-match takes (compare a hash of the
    upload against a maintained set); the set is provided via
    settings.MEDIA_HASH_BLOCKLIST. Empty blocklist => allow.
    """

    def scan(self, *, data: bytes, content_type: str) -> ScanResult:
        blocklist = {h.lower() for h in getattr(settings, "MEDIA_HASH_BLOCKLIST", [])}
        digest = hashlib.sha256(data).hexdigest()
        if digest in blocklist:
            return ScanResult(allowed=False, reason="Image matched a safety blocklist.")
        return ScanResult(allowed=True)


def get_image_scanner() -> ImageScanner:
    path = getattr(settings, "MEDIA_IMAGE_SCANNER", "apps.media.scanning.HashBlocklistScanner")
    return import_string(path)()
