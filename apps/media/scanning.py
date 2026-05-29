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


class ManagedScanner(ImageScanner):
    """Screens an image against a managed CSAM hash-matching service.

    Privacy-preserving: only the SHA-256 of the upload is sent (never the image bytes),
    matching the project's hash-only posture. The endpoint is operator-configured
    (MEDIA_SCANNER_ENDPOINT) and the request is routed through apps.safety.net.safe_get so
    it can't be coerced into reaching an internal/metadata host, and the reply is byte-capped.

    Expected response: JSON {"match": <bool>} (a truthy match blocks the upload).
    Fail-closed: on any network/parse error the image is treated as NOT clean so a children's
    platform never stores content the scanner could not clear. This is the production swap
    point — point MEDIA_IMAGE_SCANNER at apps.media.scanning.ManagedScanner."""

    def _endpoint(self) -> str:
        return getattr(settings, "MEDIA_SCANNER_ENDPOINT", "")

    def is_effective(self) -> bool:
        # Only effective when an endpoint is configured to screen against.
        return bool(self._endpoint())

    def scan(self, data: bytes) -> ScanResult:
        from apps.safety.net import safe_get

        endpoint = self._endpoint()
        if not endpoint:
            return ScanResult(clean=False, matched="scanner_unconfigured")
        api_key = getattr(settings, "MEDIA_SCANNER_API_KEY", "")
        digest = hashlib.sha256(data).hexdigest()
        headers = {"Accept": "application/json"}
        if api_key:
            headers["Authorization"] = f"Bearer {api_key}"
        try:
            resp = safe_get(
                endpoint,
                method="POST",
                json={"sha256": digest},
                headers=headers,
                timeout=getattr(settings, "MEDIA_SCANNER_TIMEOUT", 10),
                max_bytes=64 * 1024,
            )
            resp.raise_for_status()
            matched = bool(resp.json().get("match") or resp.json().get("flagged"))
        except Exception as exc:
            # Fail closed: never clear an image the scanner could not evaluate.
            logger.warning("Managed scanner unavailable; failing closed: %s", exc)
            return ScanResult(clean=False, matched="scanner_error")
        return ScanResult(clean=not matched, matched=digest if matched else "")


def get_scanner() -> ImageScanner:
    return import_string(settings.MEDIA_IMAGE_SCANNER)()
