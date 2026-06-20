"""Phase 4 renderer: the self-progression `intensity` flourish on the constellation avatar.

intensity=0.0 (the default) must be byte-identical to today so the base avatar — the one OTHERS
see — never changes; intensity>0 adds a deterministic, well-formed flourish.
"""

import xml.etree.ElementTree as ET

from apps.accounts.avatars import constellation_svg

NODES = [
    {"slug": "basketball", "name": "Basketball", "category": "team_sport", "color": "#ff7a45"},
    {"slug": "football", "name": "Football", "category": "team_sport", "color": "#ff7a45"},
    {"slug": "hiking", "name": "Hiking", "category": "outdoor", "color": "#52c41a"},
]
EDGES = [(0, 1)]


def test_intensity_zero_is_byte_identical_to_base():
    assert constellation_svg("maria", NODES, EDGES, intensity=0.0) == constellation_svg(
        "maria", NODES, EDGES
    )


def test_intensity_above_zero_changes_and_stays_wellformed():
    base = constellation_svg("maria", NODES, EDGES)
    lit = constellation_svg("maria", NODES, EDGES, intensity=0.6)
    assert lit != base
    assert len(lit) > len(base)  # the flourish only adds
    root = ET.fromstring(lit)  # still valid SVG
    assert root.tag.endswith("svg")


def test_intensity_is_deterministic():
    assert constellation_svg("maria", NODES, EDGES, intensity=0.8) == constellation_svg(
        "maria", NODES, EDGES, intensity=0.8
    )


def test_higher_intensity_adds_more():
    low = constellation_svg("maria", NODES, EDGES, intensity=0.2)
    high = constellation_svg("maria", NODES, EDGES, intensity=1.0)
    assert high.count("<circle") >= low.count("<circle")
