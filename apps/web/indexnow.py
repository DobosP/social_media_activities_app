"""IndexNow — push public-URL changes to Bing/Yandex so new content is indexed in minutes.

Fully opt-in and best-effort: a no-op unless ``INDEXNOW_ENABLED`` is true, a key is set, and a
``SITE_BASE_URL`` is configured. Only ever submits already-public open-data URLs (venues,
events, landing pages). Outbound POST goes through the SSRF-safe ``safety.net.safe_get``.
"""

import logging

from django.conf import settings

logger = logging.getLogger(__name__)

INDEXNOW_ENDPOINT = "https://api.indexnow.org/indexnow"
KEY_FILE_PATH = "/indexnow.txt"


def is_enabled() -> bool:
    return bool(
        getattr(settings, "INDEXNOW_ENABLED", False)
        and getattr(settings, "INDEXNOW_KEY", "")
        and getattr(settings, "SITE_BASE_URL", "")
    )


def submit_urls(urls) -> bool:
    """Submit a batch of absolute URLs to IndexNow. Returns True if a request was sent.

    Never raises: discovery is a best-effort nicety, never a reason to fail a request or job.
    """
    urls = [u for u in dict.fromkeys(urls) if u]  # de-dupe, drop empties, keep order
    if not urls or not is_enabled():
        return False

    base = settings.SITE_BASE_URL.rstrip("/")
    host = base.split("://", 1)[-1]
    payload = {
        "host": host,
        "key": settings.INDEXNOW_KEY,
        "keyLocation": f"{base}{KEY_FILE_PATH}",
        "urlList": urls[:10000],  # IndexNow caps a batch at 10k URLs
    }
    try:
        from apps.safety.net import safe_get

        safe_get(INDEXNOW_ENDPOINT, method="POST", json=payload, timeout=10, max_bytes=1024 * 64)
        return True
    except Exception:  # noqa: BLE001 — best-effort; log and move on
        logger.warning("IndexNow submit failed", exc_info=True)
        return False
