"""Image safety scanning. The posture is swappable (CSAR-dependent): the default
matches uploads against a configured hash blocklist (the CSAM hash-matching model),
and prod can swap in a managed scanning service via MEDIA_IMAGE_SCANNER."""

import hashlib
from abc import ABC, abstractmethod
from dataclasses import dataclass

from django.conf import settings
from django.utils.module_loading import import_string


@dataclass(frozen=True)
class ScanResult:
    clean: bool
    matched: str = ""


class ImageScanner(ABC):
    @abstractmethod
    def scan(self, data: bytes) -> ScanResult: ...


class HashBlocklistScanner(ImageScanner):
    """Blocks an image whose SHA-256 matches a known-bad hash (e.g. a CSAM hash set).
    Lawful, privacy-preserving (hashes only) and the swap point for a real service."""

    def scan(self, data: bytes) -> ScanResult:
        digest = hashlib.sha256(data).hexdigest()
        blocklist = {h.lower() for h in getattr(settings, "MEDIA_CSAM_HASH_BLOCKLIST", [])}
        if digest in blocklist:
            return ScanResult(clean=False, matched=digest)
        return ScanResult(clean=True)


def get_scanner() -> ImageScanner:
    return import_string(settings.MEDIA_IMAGE_SCANNER)()
