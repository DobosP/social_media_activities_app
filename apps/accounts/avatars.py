"""Deterministic, generated avatars (identicons).

A user's avatar is a pure function of a stable seed (their username) — no upload, no external
service, no per-user cloud spend (inv.6), and it reveals no PII (the seed is one-way hashed; a
minor's username is a system handle anyway). The SAME seed always yields the SAME mirrored 5x5
identicon + colour, so a person looks consistent across chat, connections, and their profile
without storing anything. It is the universal *default* avatar; an uploaded profile picture (one
max, see media) can override it on the profile page only.
"""

import base64
import hashlib

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
