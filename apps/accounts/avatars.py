"""Deterministic, generated avatars.

A user's avatar is a pure function of a stable seed (their username) plus, for the richer
"constellation" avatar, their *declared interests* — no upload, no external service, no per-user
cloud spend (inv.6), and it reveals no PII (the seed is one-way hashed; a minor's username is a
system handle anyway; interests render as abstract colour-coded nodes, never readable labels). The
SAME inputs always yield the SAME image, so a person looks consistent across chat, connections, and
their profile without storing anything.

Two generators live here, both pure (no DB):

* ``identicon_svg`` — the mirrored 5x5 grid. The universal *default* when there is nothing to draw a
  constellation from (a user with zero declared interests, or a bare seed string).
* ``constellation_svg`` — a night-sky map of the user's interest graph: each interest is a glowing
  colour-coded star (the "light on each node"), related interests joined by luminous colour-threads
  whose colour fades between the two stars. Caller supplies the already-resolved nodes + edges (see
  ``apps.recommendations.services.interest_graph``), so this module stays a leaf with no app deps.

An uploaded profile picture (one max, see ``media``) overrides the generated avatar on the profile
page only.
"""

import base64
import hashlib
import math

_GRID = 5  # 5x5, horizontally mirrored (symmetric, GitHub-style)


def _fill_colour(digest: bytes) -> str:
    # Hue from the hash; fixed saturation/lightness so every identicon has even contrast on the
    # light background and none come out muddy or neon.
    hue = ((digest[0] << 8) | digest[1]) % 360
    return f"hsl({hue}, 52%, 47%)"


def identicon_svg(seed: str, *, px: int = 80) -> str:
    """A deterministic mirrored identicon as an SVG string for ``seed``."""
    digest = hashlib.sha256((seed or "?").encode("utf-8")).digest()
    fg = _fill_colour(digest)
    cell = px / _GRID
    rects = []
    for col in range(3):  # left three columns; mirror to the right two
        for row in range(_GRID):
            if digest[col * _GRID + row] & 1:  # low bit decides filled/empty
                for c in (col, _GRID - 1 - col):
                    x = round(c * cell, 2)
                    y = round(row * cell, 2)
                    rects.append(f'<rect x="{x}" y="{y}" width="{cell}" height="{cell}"/>')
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{px}" height="{px}" '
        f'viewBox="0 0 {px} {px}" role="img" aria-hidden="true">'
        f'<rect width="{px}" height="{px}" fill="#eef0ee"/>'
        f'<g fill="{fg}">{"".join(rects)}</g></svg>'
    )


def identicon_data_uri(seed: str, *, px: int = 80) -> str:
    """The identicon as a ``data:image/svg+xml;base64,...`` URI — embeddable in a template ``src``
    or a JSON payload, so the same server-side generator is the single source of truth on every
    surface (no divergent JS re-implementation)."""
    b64 = base64.b64encode(identicon_svg(seed, px=px).encode("utf-8")).decode("ascii")
    return f"data:image/svg+xml;base64,{b64}"


# --- Constellation: a star-map of the user's interest graph -------------------------------------
#
# Each interest is a glowing star (a countable "light on each node"); related interests are joined
# by a luminous colour-thread whose linear gradient fades between the two stars' colours, so the
# graph itself is part of the light. Deterministic: identical (seed, nodes, edges, px) -> identical
# SVG. Every minted id is namespaced with a per-render ``uid`` so many of these can be inlined on
# one page without their gradients/filters cross-referencing each other.

_SKY_INNER = "#161d33"  # deep blue, brighter toward the centre (a cosmic vignette)
_SKY_OUTER = "#04060d"
_FALLBACK_STAR = "#8c8c8c"


def _esc(s) -> str:
    return (
        str(s)
        .replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
    )


def _prng(seed: str):
    """A deterministic float generator in [0, 1) seeded only from ``seed`` (xorshift64*), so the
    avatar is byte-stable across processes and deploys with no global RNG."""
    digest = hashlib.sha256((seed or "?").encode("utf-8")).digest()
    state = int.from_bytes(digest[:8], "big") or 1

    def nxt():
        nonlocal state
        state ^= (state >> 12) & 0xFFFFFFFFFFFFFFFF
        state ^= (state << 25) & 0xFFFFFFFFFFFFFFFF
        state ^= (state >> 27) & 0xFFFFFFFFFFFFFFFF
        return ((state * 0x2545F4914F6CDD1D) & 0xFFFFFFFFFFFFFFFF) / 2**64

    return nxt


def _constellation_layout(rnd, n, size):
    """Place n stars on a jittered ring (centre for n==1). The seeded global rotation + small
    per-node jitter keep two users with identical interests visually distinct, while the generous
    ring margin keeps every glow halo inside the viewport with no edge clipping."""
    cx = cy = size / 2.0
    if n == 1:
        return [(cx, cy)]
    radius = size * (0.30 if n <= 6 else 0.34)
    base = rnd() * math.tau  # seeded global rotation
    pts = []
    for i in range(n):
        ang = base + (math.tau * i / n) + (rnd() - 0.5) * (math.tau / n) * 0.35
        r = radius * (0.82 + 0.30 * rnd())
        pts.append((cx + r * math.cos(ang), cy + r * math.sin(ang)))
    return pts


def constellation_svg(seed: str, nodes, edges, *, px: int = 80) -> str:
    """A constellation SVG for ``seed`` over ``nodes`` (each a dict with at least ``color``) and
    ``edges`` (``(i, j[, kind])`` index pairs). Falls back to a bare night sky for zero nodes.
    Malformed / out-of-range edge indices are skipped rather than raising."""
    rnd = _prng(seed)
    n = len(nodes)
    uid = hashlib.sha256(f"{seed}|{px}|{n}".encode()).hexdigest()[:8]
    sky_id, star_blur, edge_blur = f"{uid}_sky", f"{uid}_sblur", f"{uid}_eblur"
    S = float(px)

    def col(i):
        return _esc(nodes[i].get("color") or _FALLBACK_STAR)

    parts = [
        f'<svg xmlns="http://www.w3.org/2000/svg" width="{px}" height="{px}" '
        f'viewBox="0 0 {px} {px}" role="img" aria-hidden="true">',
        "<defs>",
        f'<radialGradient id="{sky_id}" cx="50%" cy="44%" r="78%">'
        f'<stop offset="0%" stop-color="{_SKY_INNER}"/>'
        f'<stop offset="100%" stop-color="{_SKY_OUTER}"/></radialGradient>',
        # Star halo blur (strong enough to still glow at nav size) + a softer edge bloom.
        f'<filter id="{star_blur}" x="-70%" y="-70%" width="240%" height="240%">'
        f'<feGaussianBlur stdDeviation="{S * 0.022:.3f}"/></filter>',
        f'<filter id="{edge_blur}" x="-50%" y="-50%" width="200%" height="200%">'
        f'<feGaussianBlur stdDeviation="{S * 0.012:.3f}"/></filter>',
    ]

    pts = _constellation_layout(rnd, n, px) if n else []

    # Per-star radial halo (white-hot core -> colour -> transparent): one glow per node.
    halo_ids = []
    for i in range(n):
        c = col(i)
        hid = f"{uid}_h{i}"
        halo_ids.append(hid)
        parts.append(
            f'<radialGradient id="{hid}" cx="50%" cy="50%" r="50%">'
            f'<stop offset="0%" stop-color="#ffffff" stop-opacity="1"/>'
            f'<stop offset="22%" stop-color="{c}" stop-opacity="0.95"/>'
            f'<stop offset="55%" stop-color="{c}" stop-opacity="0.55"/>'
            f'<stop offset="100%" stop-color="{c}" stop-opacity="0"/></radialGradient>'
        )

    # Collect valid, de-duped edges so we can mint one gradient per drawn line.
    edge_list = []
    if n >= 2 and edges:
        seen = set()
        for e in edges:
            try:
                i, j = int(e[0]), int(e[1])
            except (TypeError, ValueError, IndexError):
                continue
            if i == j or not (0 <= i < n) or not (0 <= j < n):
                continue
            key = (min(i, j), max(i, j))
            if key not in seen:
                seen.add(key)
                edge_list.append((i, j))

    # Per-edge gradient fading between the two endpoint star colours, oriented along the actual
    # line (userSpaceOnUse) so each colour lands on its own star.
    edge_grad_ids = []
    for k, (i, j) in enumerate(edge_list):
        (x1, y1), (x2, y2) = pts[i], pts[j]
        gid = f"{uid}_e{k}"
        edge_grad_ids.append(gid)
        parts.append(
            f'<linearGradient id="{gid}" gradientUnits="userSpaceOnUse" '
            f'x1="{x1:.2f}" y1="{y1:.2f}" x2="{x2:.2f}" y2="{y2:.2f}">'
            f'<stop offset="0%" stop-color="{col(i)}"/>'
            f'<stop offset="50%" stop-color="#ffffff" stop-opacity="0.85"/>'
            f'<stop offset="100%" stop-color="{col(j)}"/></linearGradient>'
        )
    parts.append("</defs>")

    parts.append(f'<rect width="{px}" height="{px}" fill="url(#{sky_id})"/>')

    # Faint background dust for depth (deterministic; fewer when small to avoid noise).
    bg = []
    for _ in range(14 if px >= 90 else 7):
        bx, by = rnd() * S, rnd() * S
        br = max((0.4 + 0.7 * rnd()) * (S / 240.0) * 1.6, 0.4)
        bg.append(
            f'<circle cx="{bx:.2f}" cy="{by:.2f}" r="{br:.2f}" '
            f'fill="#cfd8ff" opacity="{0.12 + 0.22 * rnd():.2f}"/>'
        )
    if bg:
        parts.append(f"<g>{''.join(bg)}</g>")

    # Colour-lit edges: a soft blurred glow pass beneath a crisper bright thread.
    if edge_list:
        glow_w, thread_w = max(S * 0.022, 2.4), max(S * 0.008, 1.1)
        glow, thread = [], []
        for k, (i, j) in enumerate(edge_list):
            (x1, y1), (x2, y2) = pts[i], pts[j]
            url = f"url(#{edge_grad_ids[k]})"
            glow.append(
                f'<line x1="{x1:.2f}" y1="{y1:.2f}" x2="{x2:.2f}" y2="{y2:.2f}" '
                f'stroke="{url}" stroke-width="{glow_w:.2f}" stroke-opacity="0.42" '
                f'stroke-linecap="round"/>'
            )
            thread.append(
                f'<line x1="{x1:.2f}" y1="{y1:.2f}" x2="{x2:.2f}" y2="{y2:.2f}" '
                f'stroke="{url}" stroke-width="{thread_w:.2f}" stroke-opacity="0.55" '
                f'stroke-linecap="round"/>'
            )
        parts.append(f'<g filter="url(#{edge_blur})">{"".join(glow)}</g>')
        parts.append(f"<g>{''.join(thread)}</g>")

    # Stars: glow halo + coloured rim + white-hot core. Every node is lit here (countable).
    core_r, halo_r = max(S * 0.020, 1.5), max(S * 0.078, 6.5)
    for i in range(n):
        x, y = pts[i]
        c = col(i)
        parts.append(
            f'<g transform="translate({x:.2f} {y:.2f})">'
            f'<circle r="{halo_r:.2f}" fill="url(#{halo_ids[i]})" filter="url(#{star_blur})"/>'
            f'<circle r="{core_r * 1.9:.2f}" fill="{c}" opacity="0.95"/>'
            f'<circle r="{core_r:.2f}" fill="#ffffff"/></g>'
        )

    parts.append("</svg>")
    return "".join(parts)


def constellation_data_uri(seed: str, nodes, edges, *, px: int = 80) -> str:
    """The constellation as a ``data:image/svg+xml;base64,...`` URI (see ``identicon_data_uri``)."""
    svg = constellation_svg(seed, nodes, edges, px=px)
    b64 = base64.b64encode(svg.encode("utf-8")).decode("ascii")
    return f"data:image/svg+xml;base64,{b64}"
