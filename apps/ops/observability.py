"""Structured logging + request correlation (P1 observability), no external dependency.

A request-id ContextVar is set by ``RequestIDMiddleware`` (apps/ops/middleware.py) and woven into
every log record via ``RequestIdFilter`` + echoed on the response + tagged on the Sentry scope, so a
single request can be traced across the HTTP path and the logs. ``JsonFormatter`` emits one JSON
object per record for machine ingestion (LOG_FORMAT=json in prod); a plain text formatter is used
in dev/test for readability. Privacy: the id is a random token, never PII (invariant 2/4)."""

import json
import logging
from contextvars import ContextVar, Token

_request_id: ContextVar[str] = ContextVar("request_id", default="-")


def set_request_id(value: str) -> Token[str]:
    return _request_id.set(value or "-")


def reset_request_id(token: Token[str]) -> None:
    _request_id.reset(token)


def get_request_id() -> str:
    return _request_id.get()


class RequestIdFilter(logging.Filter):
    """Attach the current request id to every record so formatters can include it."""

    def filter(self, record: logging.LogRecord) -> bool:
        record.request_id = get_request_id()
        return True


class JsonFormatter(logging.Formatter):
    """One JSON object per log line. Keeps only non-PII operational fields."""

    _OPTIONAL_FIELDS = ("method", "path", "route", "status_code", "duration_ms")

    def format(self, record: logging.LogRecord) -> str:
        payload = {
            "ts": self.formatTime(record, "%Y-%m-%dT%H:%M:%S%z"),
            "level": record.levelname,
            "logger": record.name,
            "msg": record.getMessage(),
            "request_id": getattr(record, "request_id", "-"),
        }
        for field in self._OPTIONAL_FIELDS:
            if hasattr(record, field):
                payload[field] = getattr(record, field)
        if record.exc_info:
            payload["exc"] = self.formatException(record.exc_info)
        return json.dumps(payload, default=str)
