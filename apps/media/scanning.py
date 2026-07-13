"""Image safety scanning. The posture is swappable (CSAR-dependent): the default matches
uploads against a configured hash blocklist (exact SHA-256), and prod can point
MEDIA_IMAGE_SCANNER at a managed service (apps.media.scanning.ManagedScanner) that screens
the upload's SHA-256 against a managed hash set over an SSRF-safe channel.

NOTE on detection strength: both built-in scanners do EXACT SHA-256 matching, which only
catches known-bad files bit-for-bit (any re-encode/resize/crop evades it). They are NOT a
perceptual CSAM matcher (e.g. PhotoDNA) — for perceptual detection an operator must wire a
service that ingests a perceptual hash or the image bytes (over the SSRF-safe channel) and
state the guarantee. Either built-in path makes the fail-closed upload gate
(MEDIA_REQUIRE_SCANNER) effective so uploads can be enabled once a lawful matcher is wired."""

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

    def scan_digest(self, digest: str) -> ScanResult:
        """Screen an already-computed SHA-256 (ADR-0026: video originals are hashed as a
        stream — tens of MB are never buffered just to hash them). Fail-closed default: a
        custom scanner that hasn't implemented digest screening never silently clears one."""
        return ScanResult(clean=False, matched="digest_scan_unsupported")

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
    """Blocks an image whose SHA-256 matches a known-bad hash (e.g. a CSAM hash set),
    OR whose 64-bit perceptual dHash sits within MEDIA_PERCEPTUAL_MAX_DISTANCE bits of a
    perceptual blocklist entry (W8 — catches the trivial re-encode/resize that defeats
    exact hashing; see apps.media.perceptual for honest limits). Lawful and
    privacy-preserving (hashes only). Exact hashes come from MEDIA_CSAM_HASH_BLOCKLIST
    and/or MEDIA_CSAM_HASH_BLOCKLIST_FILE; perceptual entries (16 hex chars) from
    MEDIA_PERCEPTUAL_BLOCKLIST and/or MEDIA_PERCEPTUAL_BLOCKLIST_FILE."""

    def _blocklist(self) -> set[str]:
        hashes = {h.lower() for h in getattr(settings, "MEDIA_CSAM_HASH_BLOCKLIST", [])}
        path = getattr(settings, "MEDIA_CSAM_HASH_BLOCKLIST_FILE", "")
        if path:
            try:
                hashes |= _load_blocklist_file(path, Path(path).stat().st_mtime)
            except OSError:
                logger.exception("Could not read MEDIA_CSAM_HASH_BLOCKLIST_FILE=%s", path)
        return hashes

    def _perceptual_blocklist(self) -> set[str]:
        entries = {h.lower() for h in getattr(settings, "MEDIA_PERCEPTUAL_BLOCKLIST", [])}
        path = getattr(settings, "MEDIA_PERCEPTUAL_BLOCKLIST_FILE", "")
        if path:
            try:
                entries |= _load_blocklist_file(path, Path(path).stat().st_mtime)
            except OSError:
                logger.exception("Could not read MEDIA_PERCEPTUAL_BLOCKLIST_FILE=%s", path)
        return {e for e in entries if len(e) == 16}

    def scan_digest(self, digest: str) -> ScanResult:
        # Exact-hash screening only: the perceptual layer needs pixels, and for video that
        # runs separately against the sampled frames (services.process_pending_videos).
        if digest.lower() in self._blocklist():
            return ScanResult(clean=False, matched=digest)
        return ScanResult(clean=True)

    def scan(self, data: bytes) -> ScanResult:
        digest = hashlib.sha256(data).hexdigest()
        if digest in self._blocklist():
            return ScanResult(clean=False, matched=digest)
        perceptual = self._perceptual_blocklist()
        if perceptual:
            from .perceptual import DEFAULT_MAX_DISTANCE, dhash_hex, hamming_hex

            fingerprint = dhash_hex(data)
            if fingerprint:
                max_distance = getattr(
                    settings, "MEDIA_PERCEPTUAL_MAX_DISTANCE", DEFAULT_MAX_DISTANCE
                )
                for entry in perceptual:
                    if hamming_hex(fingerprint, entry) <= max_distance:
                        return ScanResult(clean=False, matched=f"phash:{entry}")
        return ScanResult(clean=True)

    def is_effective(self) -> bool:
        # A hash blocklist only screens anything if it actually contains hashes.
        return bool(self._blocklist() or self._perceptual_blocklist())


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
        return self.scan_digest(hashlib.sha256(data).hexdigest())

    def scan_digest(self, digest: str) -> ScanResult:
        from apps.safety.net import safe_get

        endpoint = self._endpoint()
        if not endpoint:
            return ScanResult(clean=False, matched="scanner_unconfigured")
        api_key = getattr(settings, "MEDIA_SCANNER_API_KEY", "")
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
