"""Image safety scanning. The posture is swappable (CSAR-dependent): the default
matches uploads against a configured hash blocklist (the CSAM hash-matching model), and
prod can point MEDIA_IMAGE_SCANNER at a managed scanning service (ManagedHttpScanner).

Either path makes the fail-closed upload gate (MEDIA_REQUIRE_SCANNER) effective, so
photo uploads can be enabled for production once a lawful matcher/blocklist is wired."""

import hashlib
import logging
from abc import ABC, abstractmethod
from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path

from django.conf import settings
from django.utils.module_loading import import_string

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class ScanResult:
    clean: bool
    matched: str = ""


class ImageScanner(ABC):
    @abstractmethod
    def scan(self, data: bytes) -> ScanResult: ...

    def is_effective(self) -> bool:
        """Whether this scanner can actually screen content. The fail-closed upload gate
        (MEDIA_REQUIRE_SCANNER) refuses uploads when this is False, so a children's
        platform never silently accepts unscanned images."""
        return True


@lru_cache(maxsize=8)
def _load_blocklist_file(path: str, mtime: float) -> frozenset[str]:
    """Read newline-delimited SHA-256 hashes from a file (cached on path+mtime so a
    large CSAM hash set is parsed once, but a refreshed file is picked up)."""
    hashes = set()
    for line in Path(path).read_text().splitlines():
        h = line.strip().lower()
        if h and not h.startswith("#"):
            hashes.add(h)
    return frozenset(hashes)


class HashBlocklistScanner(ImageScanner):
    """Blocks an image whose SHA-256 matches a known-bad hash (e.g. a CSAM hash set).
    Lawful and privacy-preserving (hashes only). Hashes come from the inline
    MEDIA_CSAM_HASH_BLOCKLIST and/or a MEDIA_CSAM_HASH_BLOCKLIST_FILE."""

    def _blocklist(self) -> set[str]:
        hashes = {h.lower() for h in getattr(settings, "MEDIA_CSAM_HASH_BLOCKLIST", [])}
        path = getattr(settings, "MEDIA_CSAM_HASH_BLOCKLIST_FILE", "")
        if path:
            try:
                hashes |= _load_blocklist_file(path, Path(path).stat().st_mtime)
            except OSError:
                logger.exception("Could not read MEDIA_CSAM_HASH_BLOCKLIST_FILE=%s", path)
        return hashes

    def scan(self, data: bytes) -> ScanResult:
        digest = hashlib.sha256(data).hexdigest()
        if digest in self._blocklist():
            return ScanResult(clean=False, matched=digest)
        return ScanResult(clean=True)

    def is_effective(self) -> bool:
        # A hash blocklist only screens anything if it actually contains hashes.
        return bool(self._blocklist())


class ManagedHttpScanner(ImageScanner):
    """Submits the original image bytes to a managed content-safety service (e.g. a
    PhotoDNA/Thorn-style CSAM matcher) over HTTPS and blocks on a positive match.

    Configured via MEDIA_SCANNER_ENDPOINT + MEDIA_SCANNER_API_KEY. The service is
    expected to return JSON with a truthy ``match``/``flagged`` field on a hit. Network
    or service errors are treated as NOT-CLEAN (fail-closed) — a children's platform must
    never store an image it could not screen."""

    def _endpoint(self) -> str:
        return getattr(settings, "MEDIA_SCANNER_ENDPOINT", "")

    def scan(self, data: bytes) -> ScanResult:
        import requests

        endpoint = self._endpoint()
        api_key = getattr(settings, "MEDIA_SCANNER_API_KEY", "")
        if not endpoint or not api_key:
            # Misconfigured at call time: fail closed rather than pass content through.
            return ScanResult(clean=False, matched="scanner_unconfigured")
        try:
            resp = requests.post(
                endpoint,
                headers={"Authorization": f"Bearer {api_key}"},
                files={"image": ("upload", data, "application/octet-stream")},
                timeout=getattr(settings, "MEDIA_SCANNER_TIMEOUT", 10),
            )
            resp.raise_for_status()
            body = resp.json()
        except Exception:
            logger.exception("Managed CSAM scanner call failed; failing closed")
            return ScanResult(clean=False, matched="scanner_error")
        flagged = bool(body.get("match") or body.get("flagged") or body.get("is_csam"))
        matched = str(body.get("match_id", "")) if flagged else ""
        return ScanResult(clean=not flagged, matched=matched)

    def is_effective(self) -> bool:
        return bool(self._endpoint() and getattr(settings, "MEDIA_SCANNER_API_KEY", ""))


def get_scanner() -> ImageScanner:
    return import_string(settings.MEDIA_IMAGE_SCANNER)()
