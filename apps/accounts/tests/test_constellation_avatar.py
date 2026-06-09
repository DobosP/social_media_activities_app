"""The generated constellation avatar (pure renderer, no DB): deterministic, every interest is a
lit + countable star, each edge glows as a colour-thread, all ids are namespaced so many avatars
can inline on one page without cross-referencing, and the degenerate cases (0 / 1 / disconnected
nodes, malformed edges) never crash and never leak a readable interest label."""

import base64
import re
import xml.etree.ElementTree as ET

from apps.accounts.avatars import (
    constellation_data_uri,
    constellation_svg,
    identicon_svg,
)

NODES = [
    {"slug": "basketball", "name": "Basketball", "category": "team_sport", "color": "#ff7a45"},
    {"slug": "football", "name": "Football", "category": "team_sport", "color": "#ff7a45"},
    {"slug": "hiking", "name": "Hiking", "category": "outdoor", "color": "#52c41a"},
]
EDGES = [(0, 1)]  # basketball + football share team_sport


def _uid(svg):
    return re.search(r'id="([0-9a-f]{8})_sky"', svg).group(1)


def test_wellformed_square_svg_with_viewbox():
    svg = constellation_svg("maria", NODES, EDGES, px=96)
    root = ET.fromstring(svg)  # raises on malformed XML
    assert root.tag.endswith("svg")
    assert root.attrib["viewBox"] == "0 0 96 96"
    assert root.attrib["width"] == "96" and root.attrib["height"] == "96"


def test_deterministic_same_inputs_same_svg():
    assert constellation_svg("maria", NODES, EDGES) == constellation_svg("maria", NODES, EDGES)
    assert constellation_data_uri("maria", NODES, EDGES).startswith("data:image/svg+xml;base64,")


def test_distinct_seeds_differ():
    assert constellation_svg("maria", NODES, EDGES) != constellation_svg("alex", NODES, EDGES)


def test_one_glow_halo_per_node_so_lit_interests_are_countable():
    svg = constellation_svg("maria", NODES, EDGES)
    uid = _uid(svg)
    # exactly one per-node halo gradient is referenced per interest -> the lights are countable.
    assert svg.count(f'fill="url(#{uid}_h') == len(NODES)


def test_each_edge_is_a_colour_thread_between_its_two_stars():
    svg = constellation_svg("maria", NODES, EDGES)
    # one userSpaceOnUse linear gradient per edge, fading between the endpoint colours.
    assert 'gradientUnits="userSpaceOnUse"' in svg
    assert svg.count("<linearGradient") == len(EDGES)


def test_ids_namespaced_so_two_avatars_dont_collide_on_one_page():
    # distinct per-render uid prefixes -> inlining both on one page can't cross-reference defs.
    assert _uid(constellation_svg("maria", NODES, EDGES)) != _uid(
        constellation_svg("alex", NODES, EDGES)
    )


def test_single_node_is_centered_and_lit():
    svg = constellation_svg("solo", [NODES[0]], [], px=80)
    ET.fromstring(svg)
    assert "translate(40.00 40.00)" in svg  # the centre of an 80px tile


def test_zero_nodes_is_a_bare_sky_not_a_crash():
    root = ET.fromstring(constellation_svg("empty", [], []))
    assert root.tag.endswith("svg")


def test_disconnected_and_malformed_edges_are_skipped_without_raising():
    svg = constellation_svg("x", NODES, [(0, 99), (1, 1), None, (0,), "nope", (0, 2)], px=120)
    ET.fromstring(svg)
    # only the one in-range, non-self edge (0,2) survives; (0,1) was not supplied this time.
    assert svg.count("<linearGradient") == 1


def test_tiny_nav_size_still_renders():
    root = ET.fromstring(constellation_svg("maria", NODES, EDGES, px=28))
    assert root.attrib["viewBox"] == "0 0 28 28"


def test_avatar_never_renders_a_readable_interest_label():
    # Child-safety/privacy: the picture shows abstract colour nodes, never the activity names.
    svg = constellation_svg("maria", NODES, EDGES)
    for node in NODES:
        assert node["name"] not in svg and node["slug"] not in svg


def test_fallback_identicon_is_still_a_distinct_generator():
    # constellation and identicon are different images for the same seed (the avatar layer picks).
    assert constellation_svg("maria", NODES, EDGES) != identicon_svg("maria")


def test_data_uri_decodes_to_the_same_svg():
    uri = constellation_data_uri("maria", NODES, EDGES, px=64)
    decoded = base64.b64decode(uri.split(",", 1)[1]).decode("utf-8")
    assert decoded == constellation_svg("maria", NODES, EDGES, px=64)
