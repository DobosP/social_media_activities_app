"""Event-source adapters. Venues (libraries, arts centres, sports halls) commonly
publish their programme as an iCalendar (.ics) feed; this is the realistic way to pull
"what's happening" without a bespoke per-venue integration. The parser is dependency-free
and handles the common VEVENT shape (line unfolding, DTSTART/DTEND/SUMMARY/URL/UID)."""

from abc import ABC, abstractmethod
from collections.abc import Iterator
from dataclasses import dataclass
from datetime import UTC, datetime

from django.utils import timezone as dj_timezone


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


def parse_ics(text: str) -> list[RawEvent]:
    """Parse VEVENTs from iCalendar text into RawEvent objects (skips undated events)."""
    events: list[RawEvent] = []
    current: dict | None = None
    for line in _unfold(text):
        if line == "BEGIN:VEVENT":
            current = {}
            continue
        if line == "END:VEVENT":
            if current and current.get("starts_at"):
                events.append(
                    RawEvent(
                        title=current.get("title", "(untitled)"),
                        starts_at=current["starts_at"],
                        ends_at=current.get("ends_at"),
                        description=current.get("description", ""),
                        url=current.get("url", ""),
                        external_id=current.get("uid", ""),
                    )
                )
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
        elif key == "DTSTART":
            current["starts_at"] = _parse_dt(value)
        elif key == "DTEND":
            current["ends_at"] = _parse_dt(value)
    return events


class ICalFeedSource(EventSource):
    """Reads VEVENTs from iCalendar text (already fetched) or a URL/path."""

    name = "ical"

    def __init__(self, *, text: str | None = None, url: str | None = None, timeout: int = 20):
        self.text = text
        self.url = url
        self.timeout = timeout

    def _load(self) -> str:
        if self.text is not None:
            return self.text
        if not self.url:
            raise ValueError("Provide either text or url.")
        import requests

        resp = requests.get(self.url, timeout=self.timeout)
        resp.raise_for_status()
        return resp.text

    def fetch(self) -> Iterator[RawEvent]:
        now = dj_timezone.now()
        for event in parse_ics(self._load()):
            # Skip events that have already ended.
            if (event.ends_at or event.starts_at) >= now:
                yield event
