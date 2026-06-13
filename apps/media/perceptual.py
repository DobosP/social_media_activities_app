"""Perceptual (difference-hash) image fingerprinting — Pillow-only, no numpy/ML deps.

W8: an ADDITIONAL detection layer over the exact-SHA-256 blocklist. A 64-bit dHash
survives re-encoding, resizing and small quality changes, so a known-bad image that was
trivially re-saved no longer evades the blocklist (the documented weakness of exact
hashing). It is deliberately NOT presented as a CSAM matcher of PhotoDNA's calibre:
dHash is defeatable by crops/rotations/adversarial perturbation. The real known-CSAM
layer is an external vetted service (Arachnid Shield / PhotoDNA Cloud — see
docs/MEDIA_FILTERING.md); this keeps the local blocklist honest against casual evasion
and powers near-duplicate profile-picture detection.

Privacy: a dHash is derived from the image alone, stored as 16 hex chars, and is not
reversible to the image."""

import io

from PIL import Image

from .processing import DEFAULT_MAX_PIXELS

# Hamming distance (in bits, of 64) at or below which two dHashes are "the same image".
# 0 = identical structure; ~10 still means near-duplicate. Conservative default.
DEFAULT_MAX_DISTANCE = 8


def dhash_hex(data: bytes, *, max_pixels: int = DEFAULT_MAX_PIXELS) -> str | None:
    """The 64-bit difference hash of image bytes, as 16 hex chars.

    Returns None when the bytes are not a decodable image (e.g. a PDF) or exceed the
    decompression-bomb pixel budget — callers treat None as "no perceptual signal",
    never as clean-vs-dirty."""
    try:
        with Image.open(io.BytesIO(data)) as im:
            # Bomb guard before any real decode (Image.open only parses the header).
            width, height = im.size
            if width * height > max_pixels:
                return None
            gray = im.convert("L").resize((9, 8), Image.LANCZOS)
            pixels = list(gray.tobytes())  # 9*8 grayscale bytes, in row-major order
    except Exception:
        return None
    bits = 0
    for row in range(8):
        for col in range(8):
            left = pixels[row * 9 + col]
            right = pixels[row * 9 + col + 1]
            bits = (bits << 1) | (1 if left > right else 0)
    # A degenerate hash (all zeros / all ones) means the image has no gradient structure
    # at 9x8 — e.g. a solid colour. EVERY flat image collides there, so it carries no
    # identity signal; report "no fingerprint" rather than matching all flat avatars.
    if bits in (0, (1 << 64) - 1):
        return None
    return f"{bits:016x}"


def hamming_hex(a: str, b: str) -> int:
    """Bit distance between two equal-length hex fingerprints."""
    return (int(a, 16) ^ int(b, 16)).bit_count()
