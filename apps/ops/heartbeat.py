"""Best-effort cron/job heartbeat helpers."""

from django.conf import settings


def ping_heartbeat(payload: dict | None = None) -> bool:
    """Ping OPS_HEARTBEAT_URL.

    A plain ping uses GET for compatibility with dead-man's-switch services. A payload uses POST
    JSON for job-specific summaries. Heartbeat failures are observability failures, never job
    failures, so callers get a boolean and no exception.
    """
    url = getattr(settings, "OPS_HEARTBEAT_URL", "")
    if not url:
        return False
    try:
        import requests

        if payload is None:
            requests.get(url, timeout=10)
        else:
            requests.post(url, json=payload, timeout=10)
        return True
    except Exception:  # noqa: BLE001
        return False
