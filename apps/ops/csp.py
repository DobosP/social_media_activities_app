"""CSP report parsing and digest helpers.

Browser CSP reports are intentionally unauthenticated and attacker-controlled. Keep this module
strictly operational: bounded strings, no query strings/fragments, and malformed reports become
counts rather than exceptions.
"""

from __future__ import annotations

import json
import re
from collections import Counter
from collections.abc import Iterable
from dataclasses import dataclass
from threading import Lock
from typing import Any
from urllib.parse import urlsplit

_CONTROL_CHARS = re.compile(r"[\x00-\x1f\x7f]")
MAX_CSP_REPORT_BODY_BYTES = 8 * 1024
_RECENT_REPORT_LIMIT = 200
_recent_report_lock = Lock()
_recent_reports: list[dict[str, str]] = []


@dataclass(frozen=True)
class CSPViolation:
    directive: str
    blocked: str
    document: str


@dataclass(frozen=True)
class CSPIngestResult:
    violations: list[CSPViolation]
    malformed: bool = False


def csp_clean(value: Any, limit: int) -> str:
    """Strip control characters and clamp attacker-controlled CSP fields."""
    return _CONTROL_CHARS.sub(" ", str(value or ""))[:limit]


def _pick(rep: dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if rep.get(key):
            return rep[key]
    return ""


def _normalise_directive(value: Any) -> str:
    cleaned = csp_clean(value, 80).strip()
    if not cleaned:
        return "unknown"
    return cleaned.split()[0]


def _normalise_uri(value: Any, *, limit: int = 200) -> str:
    cleaned = csp_clean(value, limit).strip()
    if not cleaned:
        return "unknown"
    if cleaned in {"self", "inline", "eval", "wasm-eval"}:
        return cleaned
    if cleaned.endswith(":") and "/" not in cleaned:
        return cleaned
    try:
        parts = urlsplit(cleaned)
    except ValueError:
        return cleaned[:limit]
    if parts.scheme in {"http", "https", "ws", "wss"} and parts.netloc:
        path = parts.path or "/"
        return f"{parts.scheme}://{parts.netloc}{path}"[:limit]
    if parts.scheme:
        return f"{parts.scheme}:"[:limit]
    return cleaned[:limit]


def _coerce_json(payload: bytes | str | dict[str, Any] | list[Any]) -> Any:
    if isinstance(payload, bytes):
        payload = payload.decode("utf-8", "replace")
    if isinstance(payload, str):
        return json.loads(payload or "{}")
    return payload


def iter_csp_reports(payload: bytes | str | dict[str, Any] | list[Any]) -> list[dict[str, Any]]:
    """Return report dictionaries from legacy and Reporting API envelopes.

    Raises ``ValueError`` only for unparseable JSON or unsupported top-level shapes. Callers that
    accept browser reports should catch it and count the payload as malformed.
    """
    try:
        data = _coerce_json(payload)
    except (TypeError, ValueError) as exc:
        raise ValueError("unparseable CSP report") from exc

    reports: list[dict[str, Any]] = []
    if isinstance(data, dict):
        rep = data.get("csp-report") or data.get("body") or data
        if isinstance(rep, dict):
            reports.append(rep)
    elif isinstance(data, list):
        for item in data:
            if not isinstance(item, dict):
                continue
            rep = item.get("csp-report") or item.get("body") or item
            if isinstance(rep, dict):
                reports.append(rep)
    else:
        raise ValueError("unsupported CSP report shape")

    if not reports:
        raise ValueError("empty CSP report")
    return reports


def parse_csp_report(payload: bytes | str | dict[str, Any] | list[Any]) -> list[CSPViolation]:
    violations = []
    for rep in iter_csp_reports(payload):
        directive = _normalise_directive(_pick(rep, "effective-directive", "violated-directive"))
        blocked = _normalise_uri(_pick(rep, "blocked-uri", "blockedURL"))
        document = _normalise_uri(_pick(rep, "document-uri", "documentURL"))
        violations.append(CSPViolation(directive=directive, blocked=blocked, document=document))
    return violations


def serialize_csp_violation(violation: CSPViolation) -> dict[str, str]:
    """Return the only CSP report fields allowed to leave the parser boundary."""
    return {
        "directive": violation.directive,
        "blocked": violation.blocked,
        "document": violation.document,
    }


def remember_csp_violations(violations: Iterable[CSPViolation]) -> None:
    """Keep a process-local ring buffer of sanitized CSP triples for operator tests/debugging.

    This is deliberately not a DB model and not a raw-payload store. Multi-process deployments still
    rely on application logs or exported report files for durable review; this buffer only exposes
    the same sanitized fields that the digest helper groups.
    """
    records = [serialize_csp_violation(violation) for violation in violations]
    if not records:
        return
    with _recent_report_lock:
        _recent_reports.extend(records)
        del _recent_reports[:-_RECENT_REPORT_LIMIT]


def recent_csp_violations() -> list[dict[str, str]]:
    with _recent_report_lock:
        return list(_recent_reports)


def clear_recent_csp_violations() -> None:
    with _recent_report_lock:
        _recent_reports.clear()


def ingest_csp_report(payload: bytes | str | dict[str, Any] | list[Any]) -> CSPIngestResult:
    """Parse and remember a browser CSP report without retaining raw attacker-controlled input."""
    try:
        violations = parse_csp_report(payload)
    except ValueError:
        return CSPIngestResult(violations=[], malformed=True)
    remember_csp_violations(violations)
    return CSPIngestResult(violations=violations)


def digest_csp_reports(
    payloads: Iterable[bytes | str | dict[str, Any] | list[Any]],
) -> dict[str, Any]:
    """Group report-only violations by directive, blocked URI, and document URI."""
    grouped: Counter[tuple[str, str, str]] = Counter()
    malformed = 0
    total = 0
    for payload in payloads:
        try:
            violations = parse_csp_report(payload)
        except ValueError:
            malformed += 1
            continue
        for violation in violations:
            total += 1
            grouped[(violation.directive, violation.blocked, violation.document)] += 1

    groups = [
        {"count": count, "directive": directive, "blocked": blocked, "document": document}
        for (directive, blocked, document), count in grouped.most_common()
    ]
    return {"total": total, "malformed": malformed, "groups": groups}
