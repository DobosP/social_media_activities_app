"""Event-source adapters. Venues (libraries, arts centres, sports halls) commonly
publish their programme as an iCalendar (.ics) feed; this is the realistic way to pull
"what's happening" without a bespoke per-venue integration. The parser is dependency-free
and handles the common VEVENT shape (line unfolding, DTSTART/DTEND/SUMMARY/URL/UID)."""

import calendar
from abc import ABC, abstractmethod
from collections.abc import Iterator
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta

from django.utils import timezone as dj_timezone

# W2-F6: a bounded RRULE subset. Recurring venue programmes (a weekly chess club, a Sunday
# parkrun) otherwise surface as a single stale stub. We expand only a safe window, capped + hard
# iteration-guarded, so a malformed or unbounded rule can never blow up memory or row counts.
_WEEKDAYS = {"MO": 0, "TU": 1, "WE": 2, "TH": 3, "FR": 4, "SA": 5, "SU": 6}
_RRULE_HORIZON_DAYS = 90
_RRULE_MAX_OCCURRENCES = 120
_RRULE_ITER_GUARD = 4000


@dataclass
class RawEvent:
    title: str
    starts_at: datetime
    ends_at: datetime | None = None
    description: str = ""
    url: str = ""
    external_id: str = ""
    source: str = "ical"


class EventSource(ABC):
    name: str

    @abstractmethod
    def fetch(self) -> Iterator[RawEvent]:
        raise NotImplementedError


def _unfold(text: str) -> list[str]:
    """RFC 5545 line unfolding: a leading space/tab continues the previous line."""
    lines: list[str] = []
    for raw in text.replace("\r\n", "\n").replace("\r", "\n").split("\n"):
        if raw[:1] in (" ", "\t") and lines:
            lines[-1] += raw[1:]
        else:
            lines.append(raw)
    return lines


def _parse_dt(value: str) -> datetime | None:
    """Parse an iCal date/date-time value into an aware datetime (assume UTC if naive)."""
    value = value.strip()
    for fmt in ("%Y%m%dT%H%M%SZ", "%Y%m%dT%H%M%S", "%Y%m%d"):
        try:
            dt = datetime.strptime(value, fmt)
        except ValueError:
            continue
        if dt.tzinfo is None:
            dt = dt.replace(tzinfo=UTC)
        return dt
    return None


def _pos_int(value, default):
    try:
        n = int(value)
    except (TypeError, ValueError):
        return default
    return n if n > 0 else default


def _add_months(dt: datetime, months: int) -> datetime:
    m = dt.month - 1 + months
    year, month = dt.year + m // 12, m % 12 + 1
    day = min(dt.day, calendar.monthrange(year, month)[1])  # clamp 31 Jan + 1mo -> 28/29 Feb
    return dt.replace(year=year, month=month, day=day)


def _expand_rrule(
    dtstart, rrule, *, now, horizon_days=_RRULE_HORIZON_DAYS, max_occurrences=_RRULE_MAX_OCCURRENCES
):
    """Expand a bounded RRULE subset — FREQ DAILY/WEEKLY/MONTHLY + INTERVAL/COUNT/UNTIL/BYDAY —
    into occurrence start datetimes within ``[now-1d, now+horizon]``, COUNT measured from the
    series start per RFC 5545. Capped at ``max_occurrences`` and hard iteration-guarded, so an
    unbounded ("forever") rule is safe. An unsupported/blank rule degrades to ``[dtstart]``."""
    parts = {}
    for tok in rrule.split(";"):
        if "=" in tok:
            k, v = tok.split("=", 1)
            parts[k.strip().upper()] = v.strip()
    freq = parts.get("FREQ", "").upper()
    if freq not in ("DAILY", "WEEKLY", "MONTHLY"):
        return [dtstart]
    interval = _pos_int(parts.get("INTERVAL"), 1)
    count = _pos_int(parts.get("COUNT"), None)
    until = _parse_dt(parts["UNTIL"]) if parts.get("UNTIL") else None
    floor = now - timedelta(days=1)
    horizon = now + timedelta(days=horizon_days)

    out: list[datetime] = []
    seen = 0  # ALL occurrences from the start, so COUNT (which counts from dtstart) is honoured

    def consider(dt) -> bool:
        """Record an occurrence if it's in window; return False to STOP the whole expansion."""
        nonlocal seen
        seen += 1
        if count is not None and seen > count:
            return False
        if until is not None and dt > until:
            return False
        if dt > horizon:
            return False
        if dt >= floor:
            out.append(dt)
        return len(out) < max_occurrences

    if freq == "WEEKLY" and parts.get("BYDAY"):
        byday = sorted({_WEEKDAYS[d] for d in parts["BYDAY"].split(",") if d in _WEEKDAYS})
        if not byday:
            byday = [dtstart.weekday()]
        week0 = dtstart - timedelta(days=dtstart.weekday())
        for wk in range(_RRULE_ITER_GUARD):
            base = week0 + timedelta(weeks=wk * interval)
            if base > horizon and (until is None or base > until):
                break
            stopped = False
            for wd in byday:
                occ = base + timedelta(days=wd)
                if occ < dtstart:
                    continue  # before the series begins — not an occurrence
                if not consider(occ):
                    stopped = True
                    break
            if stopped:
                break
    else:
        step = timedelta(weeks=interval) if freq == "WEEKLY" else timedelta(days=interval)
        dt = dtstart
        for _ in range(_RRULE_ITER_GUARD):
            if not consider(dt):
                break
            dt = _add_months(dt, interval) if freq == "MONTHLY" else dt + step
    return out or [dtstart]


def _expanded_events(base: "RawEvent", rrule: str, now) -> list["RawEvent"]:
    """One RawEvent per RRULE occurrence, preserving the event's duration. Each occurrence gets a
    distinct, length-SAFE external_id: ``<uid[:191]>:<YYYYMMDD>`` always fits the 200-char column
    + its (source, external_id) unique constraint (a blank UID stays blank — upsert then keys on
    place+title+start, which is per-occurrence distinct)."""
    duration = (base.ends_at - base.starts_at) if base.ends_at else None
    occurrences = _expand_rrule(base.starts_at, rrule, now=now)
    events = []
    for occ in occurrences:
        eid = f"{base.external_id[:191]}:{occ:%Y%m%d}" if base.external_id else ""
        events.append(
            RawEvent(
                title=base.title,
                starts_at=occ,
                ends_at=(occ + duration) if duration else None,
                description=base.description,
                url=base.url,
                external_id=eid,
                source=base.source,
            )
        )
    return events


def parse_ics(text: str, *, now=None) -> list[RawEvent]:
    """Parse VEVENTs from iCalendar text into RawEvent objects (skips undated events). A VEVENT
    with an RRULE is expanded into one RawEvent per upcoming occurrence (W2-F6); ``now`` defaults
    to the current time and bounds the expansion window (overridable for tests)."""
    now = now or dj_timezone.now()
    events: list[RawEvent] = []
    current: dict | None = None
    for line in _unfold(text):
        if line == "BEGIN:VEVENT":
            current = {}
            continue
        if line == "END:VEVENT":
            if current and current.get("starts_at"):
                base = RawEvent(
                    title=current.get("title", "(untitled)"),
                    starts_at=current["starts_at"],
                    ends_at=current.get("ends_at"),
                    description=current.get("description", ""),
                    url=current.get("url", ""),
                    external_id=current.get("uid", ""),
                )
                rrule = current.get("rrule")
                if rrule:
                    events.extend(_expanded_events(base, rrule, now))
                else:
                    events.append(base)
            current = None
            continue
        if current is None or ":" not in line:
            continue
        name, value = line.split(":", 1)
        key = name.split(";", 1)[0].upper()
        if key == "SUMMARY":
            current["title"] = value.strip()[:255]
        elif key == "DESCRIPTION":
            current["description"] = value.strip()
        elif key == "URL":
            current["url"] = value.strip()[:500]
        elif key == "UID":
            current["uid"] = value.strip()[:200]
        elif key == "RRULE":
            current["rrule"] = value.strip()
        elif key == "DTSTART":
            current["starts_at"] = _parse_dt(value)
        elif key == "DTEND":
            current["ends_at"] = _parse_dt(value)
    return events


class ICalFeedSource(EventSource):
    """Reads VEVENTs from iCalendar text (already fetched) or a URL/path."""

    name = "ical"

    def __init__(
        self,
        *,
        text: str | None = None,
        url: str | None = None,
        timeout: int = 20,
        max_bytes: int = 5 * 1024 * 1024,
    ):
        self.text = text
        self.url = url
        self.timeout = timeout
        self.max_bytes = max_bytes

    def _load(self) -> str:
        if self.text is not None:
            return self.text
        if not self.url:
            raise ValueError("Provide either text or url.")
        from apps.safety.net import safe_get

        # The feed URL is operator/external input — fetch it SSRF-safely with a byte
        # cap so a hostile or oversized .ics can't reach internal hosts or exhaust memory.
        resp = safe_get(self.url, timeout=self.timeout, max_bytes=self.max_bytes)
        resp.raise_for_status()
        return resp.text

    def fetch(self) -> Iterator[RawEvent]:
        now = dj_timezone.now()
        for event in parse_ics(self._load()):
            # Skip events that have already ended.
            if (event.ends_at or event.starts_at) >= now:
                yield event
