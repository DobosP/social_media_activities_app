# Media filtering — how the big platforms do it, and our plan

> Researched 2026-06-13 (W8). This is the operating plan for screening images/PDFs
> before they are stored, and the roadmap from launch posture to scale posture.
> `docs/SAFETY.md` stays the authoritative invariant list.

## How major platforms filter malicious media

The industry pipeline (Google/YouTube, Meta, Discord, Snap) is consistently:

1. **Exact hash match** (MD5/SHA-256) against known-bad sets — cheap first tier; defeated
   by any re-encode.
2. **Perceptual hash match** — the layer that actually drives detection. *PhotoDNA*
   (Microsoft; access-controlled, free for vetted orgs, NCMEC reporting contractually
   required), *PDQ + TMK+PDQF/vPDQ* (Meta; open-source algorithms — useless without a
   vetted hash list), *CSAI Match* (Google; video). Apple's NeuralHash is the cautionary
   tale: collisions were produced within weeks and the program was abandoned —
   perceptual hashing resists casual re-encoding, not motivated adversaries.
3. **ML classifiers** for *novel* content (Google Content Safety API, Thorn's classifier)
   — used ONLY to prioritise a human review queue. The documented false-positive case
   (a father's telehealth photos flagged, account terminated, police involved, account
   never restored) is why classifier output must never auto-trigger reports or bans.
4. **Trained human review** before any action, then **report** (NCMEC CyberTipline in the
   US; in the EU, DSA Art. 18 → national police/Europol + the INHOPE hotline), remove,
   preserve evidence, and hash the confirmed content back into the shared sets.
5. **Documents**: AV scanning (ClamAV et al.) + never rendering inline
   (`Content-Disposition: attachment` + `nosniff`) + re-encoding images to destroy
   embedded payloads (OWASP file-upload guidance).

**E2EE**: Signal scans nothing and has repeatedly said it would leave markets rather than
add client-side scanning; WhatsApp scans only the unencrypted edges (profile/group
photos, user-reported plaintext). The EU's interim ePrivacy derogation for voluntary
scanning of private messages **expired 3 April 2026**; the permanent CSA Regulation is
still in trilogue (mid-2026). Scanning *hosted* group content remains lawful under DSA
Art. 7 (voluntary own-initiative) + GDPR legitimate interest.

## What this platform already does (structural mitigations)

- Images: format allowlist (PNG/JPEG/WEBP), decompression-bomb guard, **EXIF/GPS strip via
  full re-encode** (destroys embedded payloads), size caps.
- Fail-closed scanning seam (`MEDIA_REQUIRE_SCANNER`): uploads are refused unless an
  effective scanner is configured; scan happens on the ORIGINAL bytes inside the upload
  transaction (a rejected scan rolls the post back, no orphans).
- PDFs: adults-only (`MEDIA_FILE_COHORTS`), size-capped, **always served as a forced
  download** with `nosniff` — never executes inline. No video at all.
- **No media in E2EE DMs** (unscannable by design) — stricter than WhatsApp.
- Signed, expiring, per-viewer, membership-scoped URLs; private storage; rate limits;
  evidence-preserving purge (reported/hidden content survives ephemeral expiry).

## What W8 added

- **Perceptual layer** (`apps/media/perceptual.py`): a Pillow-only 64-bit dHash.
  - `HashBlocklistScanner` now also matches uploads within
    `MEDIA_PERCEPTUAL_MAX_DISTANCE` bits of `MEDIA_PERCEPTUAL_BLOCKLIST[_FILE]` entries,
    so a casual re-encode/resize of a known-bad image no longer evades the blocklist.
  - Profile-picture uniqueness (`profile_image_is_taken`) now rejects perceptual
    near-duplicates of another same-cohort avatar (`Photo.phash`), not just exact bytes.
  - Honest limits: dHash is NOT PhotoDNA — crops/rotations/adversarial edits defeat it.
- **Document scanner seam** (`apps/media/docscan.py`): `ClamdScanner` speaks clamd's
  INSTREAM protocol with stdlib sockets, fail-closed; default stays `NoopDocumentScanner`
  with `MEDIA_REQUIRE_DOCUMENT_SCANNER=False` until an operator deploys clamd.

## Launch plan (≈ €0 licensing, donations-compatible)

1. **Apply now** (both are free but have vetting lead time):
   - **Arachnid Shield** (Canadian Centre for Child Protection) — free API, images+video,
     exact + perceptual (incl. PhotoDNA) matching; the easiest credible primary scanner.
     Wire it as a `ManagedScanner`-style backend behind `MEDIA_IMAGE_SCANNER`.
   - **Microsoft PhotoDNA Cloud** — free for vetted orgs; second match layer. Its terms
     require NCMEC reporting, so also register as a foreign ESP with the CyberTipline.
   - **Google Child Safety toolkit** — the only free novel-content classifier a small org
     can get; relevant once volume justifies a triage queue. (CSAI Match is video-only —
     irrelevant while "no video" stands, which is itself the right call.)
2. **Run clamd** alongside web (one small container) and flip
   `MEDIA_DOCUMENT_SCANNER=apps.media.docscan.ClamdScanner` +
   `MEDIA_REQUIRE_DOCUMENT_SCANNER=True`.
3. **Human review before any report or permanent action** — blurred-first viewing,
   logged in the hash-chained audit log. Never auto-report on a single hash/classifier
   signal.
4. **Reporting channels**: NCMEC CyberTipline (registered foreign ESP), esc_ABUZ /
   Salvați Copiii (the Romanian INHOPE hotline), Romanian Police/Europol per DSA Art. 18.
   Preserve evidence 12 months. Basis: GDPR Art. 6(1)(f) + DSA Art. 7, documented.
5. **Don't build**: our own classifier, client-side scanning of E2EE, video support.

## At scale

- Self-host Meta **HMA / python-threatexchange (PDQ)** against accumulated confirmed
  hashes + NCMEC/IWF hash sets (IWF membership is paid; pursue when funded).
- **Google Content Safety API** classifier triage ahead of human review; **Thorn Safer
  Match** (~$30k/yr) only if grant-funded.
- Budget the **human layer**: IFTAS (a fediverse nonprofit running exactly this stack)
  measured ~4.3 matches per 100k media files — real, nonzero, reviewable at our scale —
  and shut down for lack of funding, not technology. Plan reviewer funding + wellbeing
  into the donations model.
