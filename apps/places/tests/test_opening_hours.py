from datetime import datetime

import pytest

from apps.places.enrichment.opening_hours import is_open_at, parse_opening_hours


@pytest.mark.parametrize(
    "raw,expected_mo",
    [
        ("Mo-Fr 09:00-18:00", [[540, 1080]]),
        ("24/7", [[0, 1440]]),
        ("Mo,We,Fr 10:00-20:00", [[600, 1200]]),
        ("Mo-Fr 09:00-12:00,13:00-17:00", [[540, 720], [780, 1020]]),
    ],
)
def test_parse_basic(raw, expected_mo):
    schedule = parse_opening_hours(raw)
    assert schedule is not None
    assert schedule["mo"] == expected_mo


def test_parse_off_day_and_multiple_rules():
    schedule = parse_opening_hours("Mo-Fr 09:00-18:00; Sa 10:00-14:00; Su off")
    assert schedule["sa"] == [[600, 840]]
    assert schedule["su"] == []
    assert schedule["fr"] == [[540, 1080]]


def test_parse_crosses_midnight_splits_to_next_day():
    schedule = parse_opening_hours("Fr 20:00-02:00")
    assert schedule["fr"] == [[1200, 1440]]
    assert schedule["sa"] == [[0, 120]]


@pytest.mark.parametrize("raw", ["", "  ", "garbage", "Mo", "Mo 9-5pm", "Xx 09:00-10:00"])
def test_parse_unparseable_returns_none(raw):
    assert parse_opening_hours(raw) is None


def test_is_open_at():
    schedule = parse_opening_hours("Mo-Fr 09:00-18:00")
    # 2024-01-01 is a Monday.
    assert is_open_at(schedule, datetime(2024, 1, 1, 10, 0)) is True
    assert is_open_at(schedule, datetime(2024, 1, 1, 8, 0)) is False
    # Saturday: closed.
    assert is_open_at(schedule, datetime(2024, 1, 6, 10, 0)) is False
    # Boundary: close time is exclusive.
    assert is_open_at(schedule, datetime(2024, 1, 1, 18, 0)) is False


def test_is_open_at_no_schedule_is_unknown():
    assert is_open_at(None, datetime(2024, 1, 1, 10, 0)) is None
