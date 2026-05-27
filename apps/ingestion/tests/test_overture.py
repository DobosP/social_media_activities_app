import pytest

from apps.ingestion.sources.overture import OvertureAdapter, match_overture


def test_match_overture_primary():
    matches = dict((slug, conf) for slug, _rule, conf in match_overture("library"))
    assert matches == {"reading": 0.95}


def test_match_overture_alternate_scaled_lower():
    result = match_overture("recreation_center", ["basketball_court"])
    by_slug = {slug: conf for slug, _rule, conf in result}
    # basketball appears from both the generic primary (0.2) and the stronger
    # alternate (0.9 * 0.7 = 0.63); the higher wins.
    assert by_slug["basketball"] == pytest.approx(0.63)
    assert by_slug["football"] == pytest.approx(0.2)


def test_match_overture_unknown_is_empty():
    assert match_overture("bank") == []
    assert match_overture(None) == []


def test_row_to_raw_place():
    row = {
        "id": "08f1234",
        "name": "Central Library",
        "category": "library",
        "alternate": ["public_library"],
        "lon": 23.59,
        "lat": 46.77,
        "addresses": [{"freeform": "Str. Mihai 1", "locality": "Cluj-Napoca", "country": "RO"}],
        "websites": ["https://lib.example.ro"],
    }
    raw = OvertureAdapter.row_to_raw_place(row)
    assert raw is not None
    assert raw.source == "overture"
    assert raw.external_id == "08f1234"
    assert raw.lon == 23.59
    assert raw.address["city"] == "Cluj-Napoca"
    assert raw.tags["overture:category"] == "library"
    assert raw.tags["overture:website"] == "https://lib.example.ro"


@pytest.mark.parametrize(
    "row",
    [
        {"id": "1", "name": "", "lon": 1.0, "lat": 2.0},  # no name
        {"id": "2", "name": "X", "lon": None, "lat": 2.0},  # no coords
    ],
)
def test_row_to_raw_place_skips_invalid(row):
    assert OvertureAdapter.row_to_raw_place(row) is None


def test_fetch_requires_bbox():
    adapter = OvertureAdapter(data_path="/tmp/places.parquet")
    with pytest.raises(ValueError):
        list(adapter.fetch())


def test_fetch_yields_and_respects_limit(monkeypatch):
    rows = [
        {"id": str(i), "name": f"Place {i}", "category": "library", "lon": 23.5, "lat": 46.7}
        for i in range(5)
    ]
    adapter = OvertureAdapter(data_path="/tmp/places.parquet")
    monkeypatch.setattr(adapter, "_query_rows", lambda bbox: iter(rows))
    out = list(adapter.fetch(bbox=(23.0, 46.0, 24.0, 47.0), limit=3))
    assert len(out) == 3
    assert all(r.source == "overture" for r in out)
