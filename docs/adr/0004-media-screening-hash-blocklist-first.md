# ADR-0004: Media screening — fail-closed hash blocklist now, vetted perceptual-hash vendor later

Date: 2026-06-13
Status: accepted

## Decision
Screen uploaded media with a **fail-closed, in-repo hash layer now** — exact SHA-256 blocklist +
a Pillow-only 64-bit dHash perceptual supplement (`apps/media/perceptual.py`), scanning ORIGINAL
bytes inside the upload transaction, uploads refused unless a scanner is effective
(`MEDIA_REQUIRE_SCANNER`) — and adopt a **vetted managed perceptual matcher later** (Arachnid
Shield / PhotoDNA-class via the `ManagedScanner` seam). Structural rules stand regardless:
EXIF-strip via full re-encode, PDF adults-only + forced download, **no video**, **no media in
E2EE DMs**. Decided 2026-06-13 (W8, `f11e3eb`); full plan `docs/MEDIA_FILTERING.md`.

## Context / why
A child-first platform must screen for CSAM, but the industry-grade layer (PhotoDNA, PDQ hash
lists) is access-controlled and needs org vetting — unavailable at launch.
- **Why not ship an ML classifier**: documented false-positive catastrophes (accounts terminated,
  police involved); classifiers may only ever prioritise a human review queue, never auto-act.
- **Why not client-side scanning of E2EE**: deliberately rejected (see ADR-0006); instead E2EE
  DMs carry **no media at all** — stricter than WhatsApp.
- **Why dHash despite weakness**: it defeats casual re-encode/resize evasion of the exact
  blocklist at zero licensing cost; it is honestly documented as NOT PhotoDNA (crops/rotations/
  adversarial edits defeat it).
- **Fail-closed is the invariant**: no effective scanner configured → uploads OFF in prod. This
  keeps minor-cohort photos impossible until a lawful matcher exists (PRODUCTION_READINESS §2e).

## Consequences
- Launch posture: photo uploads stay dark in prod until Arachnid Shield / PhotoDNA vetting lands
  (apply early — weeks of lead time); clamd is the document-scanner path when deployed.
- Human review precedes any report/permanent action; reporting channels (NCMEC as foreign ESP,
  esc_ABUZ/INHOPE, DSA Art. 18) must be registered before scale.
- Never build: own classifier, E2EE client-side scanning, video support (each needs its own ADR).
- The perceptual-vendor choice is an **open DPO/product call** (tracked in the gap tracker).
- Supersedes: none (first media-screening decision record). Superseded-by: none.
