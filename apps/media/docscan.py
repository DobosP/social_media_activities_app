"""Document (PDF) scanning seam — W8.

Images are re-encoded (which destroys embedded payloads); PDFs cannot be cheaply
rewritten, so the existing posture is adults-only + size-capped + ALWAYS served as a
forced download with nosniff (never executes inline). This module adds the standard
third layer big platforms use: an antivirus pass over the stored bytes.

Default is ``NoopDocumentScanner`` with ``MEDIA_REQUIRE_DOCUMENT_SCANNER=False`` so the
current behaviour is unchanged; an operator deploys a clamd sidecar and flips
``MEDIA_DOCUMENT_SCANNER`` to ``ClamdScanner`` (+ the require flag) to fail closed.
ClamAV catches known signatures only — it is a tier, not a guarantee (the
forced-download posture stays regardless of scan result)."""

import logging
import socket
import struct
from abc import ABC, abstractmethod

from django.conf import settings
from django.utils.module_loading import import_string

from .scanning import ScanResult

logger = logging.getLogger(__name__)


class DocumentScanner(ABC):
    @abstractmethod
    def scan(self, data: bytes) -> ScanResult: ...

    def is_effective(self) -> bool:
        return True


class NoopDocumentScanner(DocumentScanner):
    """No document scanning configured. ``is_effective`` is honest about it, so the
    fail-closed flag (MEDIA_REQUIRE_DOCUMENT_SCANNER) refuses PDFs when an operator
    demands scanning but hasn't wired a scanner."""

    def is_effective(self) -> bool:
        return False

    def scan(self, data: bytes) -> ScanResult:  # pragma: no cover - guarded by is_effective
        return ScanResult(clean=True)


class ClamdScanner(DocumentScanner):
    """Streams the bytes to a clamd daemon over its INSTREAM protocol (stdlib socket,
    no client dependency). Fail-closed: any connection/protocol error is NOT clean."""

    def _addr(self) -> tuple[str, int]:
        return (
            getattr(settings, "MEDIA_CLAMD_HOST", "127.0.0.1"),
            int(getattr(settings, "MEDIA_CLAMD_PORT", 3310)),
        )

    def is_effective(self) -> bool:
        return bool(self._addr()[0])

    def scan(self, data: bytes) -> ScanResult:
        timeout = getattr(settings, "MEDIA_CLAMD_TIMEOUT", 20)
        try:
            with socket.create_connection(self._addr(), timeout=timeout) as sock:
                sock.sendall(b"zINSTREAM\0")
                view = memoryview(data)
                chunk_size = 64 * 1024
                for start in range(0, len(view), chunk_size):
                    chunk = view[start : start + chunk_size]
                    sock.sendall(struct.pack("!L", len(chunk)) + chunk.tobytes())
                sock.sendall(struct.pack("!L", 0))
                reply = sock.recv(4096).decode("utf-8", "replace").strip("\0").strip()
        except OSError as exc:
            logger.warning("clamd unavailable; failing closed: %s", exc)
            return ScanResult(clean=False, matched="clamd_error")
        if reply.endswith("OK"):
            return ScanResult(clean=True)
        return ScanResult(clean=False, matched=reply[:120])


def get_document_scanner() -> DocumentScanner:
    return import_string(
        getattr(settings, "MEDIA_DOCUMENT_SCANNER", "apps.media.docscan.NoopDocumentScanner")
    )()
