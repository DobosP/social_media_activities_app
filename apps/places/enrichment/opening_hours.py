"""Parse OSM-style ``opening_hours`` strings into structured JSON.

D1 stores the raw string; D7 parses it so we can answer "is it open now?" without
an external call. This implements the *common* subset of the OSM opening_hours
spec — enough for the venues we ingest — not the full grammar:

    "Mo-Fr 09:00-18:00; Sa 10:00-14:00"
    "Mo-Su 08:00-22:00"
    "24/7"
    "Mo,We,Fr 10:00-20:00; Su off"

Unparseable input yields ``None`` (caller keeps the raw string). The structured
form is ``{"<day>": [[open_min, close_min], ...]}`` with day keys ``mo,tu,we,th,
fr,sa,su`` and minutes-since-midnight integers; an interval crossing midnight is
split across days.
"""

from __future__ import annotations

import re

DAYS = ["mo", "tu", "we", "th", "fr", "sa", "su"]
_DAY_INDEX = {day: i for i, day in enumerate(DAYS)}

_TIME_RE = re.compile(r"^(\d{1,2}):(\d{2})$")


def _to_minutes(value: str) -> int | None:
    match = _TIME_RE.match(value.strip())
    if not match:
        return None
    hour, minute = int(match.group(1)), int(match.group(2))
    if hour == 24 and minute == 0:
        return 1440
    if not (0 <= hour <= 23 and 0 <= minute <= 59):
        return None
    return hour * 60 + minute


def _expand_days(spec: str) -> list[int] | None:
    """ "mo-fr" -> [0..4]; "sa" -> [5]; "mo,we,fr" -> [0,2,4]; ranges wrap."""
    days: list[int] = []
    for part in spec.split(","):
        part = part.strip()
        if not part:
            return None
        if "-" in part:
            start, end = part.split("-", 1)
            if start not in _DAY_INDEX or end not in _DAY_INDEX:
                return None
            i, j = _DAY_INDEX[start], _DAY_INDEX[end]
            span = range(i, j + 1) if i <= j else [*range(i, 7), *range(0, j + 1)]
            days.extend(span)
        elif part in _DAY_INDEX:
            days.append(_DAY_INDEX[part])
        else:
            return None
    return days


def _add_interval(schedule: dict[str, list[list[int]]], day_idx: int, start: int, end: int) -> None:
    if end > start:
        schedule[DAYS[day_idx]].append([start, end])
    elif end < start:
        # Crosses midnight: split into [start, 1440] today and [0, end] tomorrow.
        schedule[DAYS[day_idx]].append([start, 1440])
        schedule[DAYS[(day_idx + 1) % 7]].append([0, end])


def parse_opening_hours(raw: str) -> dict[str, list[list[int]]] | None:
    if not raw or not raw.strip():
        return None
    text = raw.strip().lower()
    schedule: dict[str, list[list[int]]] = {day: [] for day in DAYS}

    if text in ("24/7", "24/7 open", "mo-su 00:00-24:00"):
        for day in DAYS:
            schedule[day] = [[0, 1440]]
        return schedule

    parsed_any = False
    for rule in text.split(";"):
        rule = rule.strip()
        if not rule:
            continue
        parts = rule.split()
        if len(parts) < 2:
            return None
        day_spec, time_specs = parts[0], parts[1:]
        days = _expand_days(day_spec)
        if not days:
            return None
        if time_specs == ["off"] or time_specs == ["closed"]:
            parsed_any = True
            continue
        for time_spec in time_specs:
            time_spec = time_spec.rstrip(",")
            for window in time_spec.split(","):
                if "-" not in window:
                    return None
                open_str, close_str = window.split("-", 1)
                start, end = _to_minutes(open_str), _to_minutes(close_str)
                if start is None or end is None:
                    return None
                for day_idx in days:
                    _add_interval(schedule, day_idx, start, end)
                parsed_any = True

    return schedule if parsed_any else None


def is_open_at(schedule: dict[str, list[list[int]]] | None, when) -> bool | None:
    """Return whether ``schedule`` is open at datetime ``when`` (or ``None`` if
    we have no parsed schedule to answer with)."""
    if not schedule:
        return None
    day_key = DAYS[when.weekday()]
    minute = when.hour * 60 + when.minute
    for start, end in schedule.get(day_key, []):
        if start <= minute < end:
            return True
    return False
