# ADR-0026 — Private-thread video attachments + state-of-the-art image compression

- **Status:** Accepted (owner request 2026-07-12: "robust, cheap and fast photos/videos sharing…
  add a state of the art compressing layer"; video capability ships **default-off** — see §5)
- **Date:** 2026-07-13
- **Supersedes:** the "no video" decision line of ADR-0004 (`0004-media-screening-hash-blocklist-first.md`),
  which itself said video "needs its own ADR". This is that ADR. Everything else in ADR-0004
  (hash-blocklist-first, fail-closed, no own classifier, no E2EE scanning, no media in DMs) stands.

## Context

W8 (2026-06-19) gave every image upload a single compressed object: EXIF/GPS strip via
from-pixels re-encode, ≤2048 px, WebP q80, private storage, signed per-viewer URLs. Three cost
levers were still on the table, and video did not exist at all:

1. **Codec headroom.** AVIF is ~15–30 % smaller than WebP at matched perceptual quality at
   realistic web quality ranges (Cloudinary CID22 SSIMULACRA2 measurements; industrialempathy
   matched-quality table: JPEG80 ≈ WebP82 ≈ AVIF64). Browser support is ~93–94 % (all evergreen
   browsers; Safari/iOS ≥ 16.4). Pillow ships native AVIF wheels since 11.3.0; our pin (12.x)
   has it (`features.check("avif") == True`, verified in the dev container). JPEG XL was
   evaluated and **rejected for now**: no native Pillow support and Chrome support still behind
   a default-off flag as of this writing.
2. **Serving cost.** We serve the full 2048 px object everywhere, including cards and thread
   streams. Every large platform serves size-appropriate renditions; with no CDN in front of
   signed URLs, the rendition must exist as its own stored object (S3 cannot content-negotiate).
   Industry practice that transfers to our scale: *transcode once at upload — serve forever*;
   eager generation beats on-demand when there is no edge cache to absorb the first hit.
3. **Video.** Deferred by ADR-0004 pending a video-CSAM-scanning decision
   (`FEATURES_BUILT.md` "No video — deferred…", `MEDIA_FILTERING.md` "Don't build … video
   support"). The owner has now asked for video sharing. The industry cost recipe for short
   clips on commodity hardware is settled: validate with ffprobe, transcode once to a single
   progressive H.264/AAC MP4 (`+faststart`), poster frame, serve over HTTP Range — no HLS/ABR
   ladder for ≤2-minute clips, and no AV1 as sole format in 2026 (encode is 3–5× x264 on CPU;
   Apple hardware decode still a minority).

## Decision

### 1. Canonical image format becomes AVIF (config-reversible)

- `MEDIA_IMAGE_OUTPUT_FORMAT` default flips `WEBP` → `AVIF`. Already-stored WebP objects are
  **not** re-encoded (a lossy→lossy pass wastes CPU and quality; the win applies to new uploads).
- `MEDIA_IMAGE_QUALITY` default becomes `0` = *auto per codec* (AVIF 64, WebP 80, JPEG 80 — the
  matched-quality equivalents). An explicit value still wins for every codec.
- `processing.py` gains AVIF in the allow-list, extension map, alpha handling (same as WebP),
  and the codec clamp table (`_CODEC_MAX_SIDE["AVIF"] = 65535`, the AV1 spec frame limit —
  encoder verified OK at 65536 in-container; the W8 codec-clamp lesson is honoured).
- Prod boot guardrail: configuring an AVIF output without a Pillow that can encode it is
  `ImproperlyConfigured` (fail at boot, not at first upload). Rollback is
  `MEDIA_IMAGE_OUTPUT_FORMAT=WEBP` — one env var.

### 2. Eager thumbnail rendition per image

- One additional rendition (`MEDIA_THUMB_DIMENSION`, default 800 px longest side — covers a
  ~400 px card at 2× DPR) is generated at upload time from the already-clean full-size bytes,
  same codec/quality, stored under `thumbs/…`, tracked by a nullable `thumb_storage_key` on
  `Photo`, `Attachment`, `ActivityCover`.
- Sources already ≤ the thumb size get no thumb (serving falls back to the full object — also
  the behaviour for every pre-existing row, so no backfill is required; a backfill command can
  come later if serving data shows it is worth it).
- Signed URLs carry a variant flag; card/stream surfaces (discovery cards, thread streams,
  photo grids) serve the thumb, click-through/detail serves the full object. `PlaceCover` is
  deliberately out of scope for this slice (same pattern can follow).
- Thumbnails are **never** used for sha256/dHash dedup or scanning — those stay keyed on the
  original/full bytes exactly as before (determinism).

### 3. Video attachments — private group threads only

A new `Attachment.Kind.VIDEO`, allowed **only** as a member's own-post attachment in a
cohort-gated activity thread. Explicitly still forbidden everywhere else: no discovery/cover
video, no public surface, no DMs (ADR-0006 unchanged), no autoplay, no loops, no view counts —
the player is a plain `<video controls preload="metadata">` with a poster.

**Admission (synchronous, in the upload request):**
size cap → container magic sniff → cohort gate → full `can_read_thread` write gate →
fail-closed scanner gate + **SHA-256 of the original bytes** checked against the blocklist /
managed service (streamed, the file is never fully buffered in RAM) → original stored to a
quarantine prefix (`video-src/…`) → row created **withheld** (`status=pending`). Per
`docs/ASYNC_TASKS.md`'s load-bearing rule, deferral never moves a safety gate: the withheld row
is invisible/unservable until processing succeeds.

**Processing (asynchronous, off the request path):**
The row's own status machine is the queue — `pending` rows are claimed
(`select_for_update(skip_locked=True)`, flip to `processing`, commit) by the
`transcode_videos` management command, and the multi-minute work runs **outside any DB
transaction** (the DeferredTask queue was evaluated and rejected for this job: it holds its
claim transaction open across the handler, which would pin one of the ~4 pooled prod
connections for minutes). Steps, all on scratch disk with a sandboxed ffmpeg
(`-nostdin`, `-protocol_whitelist file`, wall-clock timeout, `RLIMIT_CPU`/`RLIMIT_AS`,
own process group killed on timeout, thread cap):

1. `ffprobe` validation: container whitelist (MP4/MOV family, Matroska/WebM), codec whitelist
   (video h264/hevc/vp9/av1/mpeg4; audio aac/mp3/opus/vorbis or none), exactly one real video
   stream, duration ≤ `MEDIA_VIDEO_MAX_DURATION_SECONDS` (default 90), per-side source cap,
   sane pixel format — decode-bomb classes rejected before any transcode.
2. Transcode once: x264 High@4.1, `-crf 23 -preset medium`, ≤720p-class (`≤1280 px` side,
   never upscaled), yuv420p, AAC 96k, `-map_metadata -1 -map_chapters -1` (+ ffmpeg 7
   autorotate bakes any rotation into pixels), `-movflags +faststart` → one progressive MP4.
   Re-encoding *is* the metadata strip — GPS/device tags cannot survive it.
3. Poster frame extracted from the **output** (never the raw upload) and pushed through the
   ordinary image pipeline (`validate_and_strip` → AVIF/WebP), so the poster inherits every
   image safety property.
4. **Frame scan:** sampled frames (default one per 5 s, capped, always including frame 0) each
   run through the configured image scanner — the perceptual dHash blocklist matches known-bad
   imagery appearing inside the video (hash-blocklist-first, per ADR-0004; still no ML
   classifier). Any match → `status=blocked`: never served to anyone, audited; staff see an
   explicit "blocked by safety screening" placeholder, and the quarantined source bytes are
   retained for moderation **at the storage/bucket level** (deliberately not servable through
   any in-app URL — an evidence pull is an operator action, not a web view).
5. Finalise (short transaction): store MP4 + poster **under deterministic per-attachment keys**
   (a crashed attempt's partial output is overwritten by the retry, never orphaned), set
   dimensions/duration/byte size, delete the quarantined original (it still contains source
   metadata — privacy), `status=ready`, audit. Transient failures leave the row `processing`
   and the stale-cutoff re-admits it on a LATER run (attempts spread out, never burned
   back-to-back); a worker crash heals the same way. Terminal failure → `status=failed`, blobs
   reclaimed, audited. Concurrency hygiene on the 4-connection prod pool: the inline
   post-upload kick is **single-flight per process** (one drain thread at a time; the timer is
   the durable fallback) and DB connections are released before the minutes of ffmpeg work.
   The ephemeral purge locks the Attachment row itself before touching a video's keys, so it
   can never race a finaliser into destroying blocked-evidence or clobbering a fresh READY row.

**Serving:** same signed per-viewer, membership-scoped tokens; the streaming view gains
single-range HTTP Range support (206) + `Accept-Ranges` so seeking works; the poster serves
via the image path. `MEDIA_REDIRECT_TO_PRESIGNED=True` remains the documented scale lever
(S3 handles Range natively). Non-`ready` videos render as a calm placeholder, never bytes.
Everything (main/poster/source/thumb) is covered by the existing ephemeral-expiry + purge +
evidence-preservation machinery and the GDPR Art. 17 blob-cleanup signals.

**Live chat parity:** the thread WebSocket layer renders attachments first-class. The group
broadcast carries attachment **IDs only** — a signed media URL is per-viewer, so it can never
ride a group payload; each member's consumer (which already re-authorizes every delivery)
resolves them through the same `attachments_for_posts` gate and mints that viewer's URLs.
When a video finishes (or fails) processing, a `chat.attachments` update swaps the "being
prepared" placeholder for the player live, no reload. The E2EE surfaces (1:1 DMs, D10 group
messaging) remain media-free — ADR-0006's "no media where scanning is impossible" is untouched.

### 4. Safety posture (what makes this compatible with ADR-0004's rationale)

- **Adult cohorts only at launch:** `MEDIA_VIDEO_COHORTS=["adult"]` — the exact precedent set
  by PDF attachments ("a NEW media type → adults only, none for minors"). Minor-cohort video
  stays structurally off until a lawful video CSAM matcher (e.g. CSAI Match) is adopted via its
  own decision — that unresolved question was ADR-0004's reason to defer video, and it remains
  unresolved **for minors**; this ADR does not touch it. The standing minors NO-GO release
  posture (SAFETY.md) is unaffected.
- **Fail-closed stays fail-closed:** `MEDIA_REQUIRE_SCANNER` applies to video exactly as to
  images; without an effective scanner, video uploads are refused in prod.
- **CLAUDE.md invariant #1** ("No short-video…") is narrowed by this ADR to what its
  surrounding text already scoped: no short-video **surfaces** — no public/discovery video, no
  video feeds, no engagement mechanics. A private, member-only, capped-length clip attached to
  a thread message is the moving-picture equivalent of the already-sanctioned private thread
  photo. The invariant wording in CLAUDE.md is updated alongside this ADR — **owner sign-off on
  that wording is part of landing this change.**

### 5. Default-off

`MEDIA_VIDEO_ENABLED` defaults to **False** (on in local dev settings). Merging this ADR
changes no running deployment: enabling video in prod is a deliberate operator/owner act, and
the prod guardrail refuses to boot video-enabled without ffmpeg/ffprobe present.

## Cost model (why this is the cheap-and-scalable shape)

- Images: AVIF ≈ 15–30 % less storage + egress on every new upload; thumbs cut the hot
  card/stream surfaces from ~200–400 KB to ~30–60 KB per image — the single biggest egress
  lever available without a CDN.
- Video: one ~90 s 720p CRF-23 clip ≈ 15–25 MB stored (vs 75–150 MB raw phone footage);
  encode cost is paid exactly once on our own CPU (≈2–4× realtime on the launch box), zero
  per-view compute; Range-served progressive MP4 needs no packaging infrastructure.
- Both pipelines stay storage-agnostic behind `StorageBackend` (local/S3/any S3-compatible EU
  bucket — the owner picks buckets later; nothing here binds to a provider).

## Consequences

- ffmpeg (+ffprobe) joins the Docker image AND the CI pytest job AND the cloud-init package
  list (apt, `--no-install-recommends`); Trivy will report its CVE surface — mitigated by the
  demuxer/codec allow-lists, resource limits, no-network processing, and routine base-image
  rebuilds. The transcode command gets its own frequent systemd timer (installed/enabled by
  cloud-init, `--limit 2` per run so systemd can never kill a clip mid-encode);
  `run_due_jobs` keeps a bounded daily safety-net entry. Caddy gains a 90 MB edge body cap
  (it previously had none) as defence-in-depth above the app's per-path middleware caps.
- New settings: `MEDIA_THUMB_DIMENSION`, `MEDIA_VIDEO_ENABLED`, `MEDIA_VIDEO_COHORTS`,
  `MEDIA_VIDEO_MAX_UPLOAD_BYTES` (80 MiB), `MEDIA_VIDEO_MAX_DURATION_SECONDS` (90),
  `MEDIA_VIDEO_MAX_SOURCE_SIDE`, `MEDIA_VIDEO_TARGET_MAX_SIDE` (1280), `MEDIA_VIDEO_CRF` (23),
  `MEDIA_VIDEO_PRESET` (medium), `MEDIA_VIDEO_AUDIO_BITRATE` (96k), `MEDIA_VIDEO_THREADS`,
  `MEDIA_VIDEO_FFMPEG_TIMEOUT`, `MEDIA_VIDEO_MAX_ATTEMPTS`,
  `MEDIA_VIDEO_STALE_PROCESSING_SECONDS`, `MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS`,
  `MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES`, `MEDIA_VIDEO_INLINE_PROCESSING`.
  The request-size middleware exempts only the thread-post endpoint, only when video is
  enabled, up to the video cap — the global 8 MiB body cap is unchanged for everything else.
- Documentation updated in lockstep: `FILE_STORAGE.md` (new video section + AVIF/renditions),
  `SAFETY.md` D6, `FEATURES_BUILT.md`, `MEDIA_FILTERING.md`, CLAUDE.md invariant #1 wording.
- **Never build** (unchanged from ADR-0004, restated): our own content classifier, E2EE
  client-side scanning, public/discovery video surfaces, engagement mechanics on media.
  **Deferred:** minor-cohort video (needs a video-CSAM matcher decision), AV1 delivery
  (revisit when decode support is universal or CPU is free), HLS/ABR (only if clips ever grow
  past the short-clip shape), PlaceCover thumbs, thumbnail backfill for pre-existing images.
