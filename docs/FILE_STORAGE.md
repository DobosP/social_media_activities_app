# File storage — secure, EU-resident, cheap, smart-compressed

How private blobs (profile pictures, in-thread photos, PDF attachments, and — ADR-0026 —
short in-thread videos) are stored, served, and kept small. Design goals, in
priority order: **child-safe + secure → EU data residency → cheap**. PostgreSQL stays the single
primary datastore (relational + geo + graph + pgvector); only *bytes* live in object storage.

See also: `docs/HOSTING_EU.md` (where to run it), `docs/SAFETY.md` (safety invariants), and
`docs/adr/0026-private-thread-video-and-sota-image-compression.md` (codec choices + video design).

---

## 1. Architecture at a glance

```
upload ─▶ size/format/bomb checks ─▶ safety scan (original bytes, fail-closed)
       ─▶ EXIF/GPS strip + orientation bake ─▶ SMART COMPRESS (transcode → AVIF @ quality)
       ─▶ + one small card/stream rendition (thumbs/…)
       ─▶ StorageBackend.save(key, bytes, content_type)   [private objects]
                                   │
row in Postgres (Photo / media.Attachment): storage_key, content_type, byte_size, sha256, w/h, …
                                   │
serve ─▶ signed, per-viewer, membership-scoped token ─▶ view re-checks access ─▶ streams bytes
         or 307-redirects to a short presigned object-store URL when explicitly enabled
         (images inline + nosniff; PDF forced-download + nosniff; never a public bucket URL)
```

- **Pluggable backend** — `apps/media/storage.py` defines a tiny `StorageBackend` ABC
  (`save`/`open`/`exists`/`delete`). Two implementations:
  - `LocalStorageBackend` — filesystem under `MEDIA_ROOT/uploads` (dev/tests; *not* for prod scale).
  - `S3StorageBackend` — any S3-compatible service (Hetzner Object Storage / Cloudflare R2 / MinIO /
    AWS S3) via boto3, selected by `MEDIA_STORAGE_BACKEND`. Adding a new provider is one class; the
    rest of the app is unchanged.
- **The blob is never the source of truth for access.** A row in Postgres
  (`media.Photo` / `media.Attachment`) holds `storage_key` + metadata; access is decided at *serve*
  time by the membership gate, not by who holds a URL.

## 2. The upload pipeline (photos **and** attachments share it)

Both `media.upload_photo` and `media.attach_to_post` (in `apps/media/services.py`) run the same
steps; PDFs skip image processing (stored as-is, only ever served as a download):

1. **Size / format / decompression-bomb checks** — `processing.validate_and_strip` rejects
   oversized files and images whose *header-declared* pixel count exceeds `MEDIA_MAX_IMAGE_PIXELS`
   **before** decoding any pixels (a small file can declare gigapixels), with Pillow's own bomb
   guard armed as a second line.
2. **Safety scan on the ORIGINAL bytes** — fail-closed. With `MEDIA_REQUIRE_SCANNER=True` (prod
   default) an upload is refused unless an *effective* scanner is configured (hash blocklist or a
   managed CSAM service); the original bytes are what a CSAM hash set matches. PDFs additionally
   pass the document/AV seam (`MEDIA_REQUIRE_DOCUMENT_SCANNER`).
3. **EXIF/GPS strip + orientation bake** — the image is rebuilt from raw pixels, so **all metadata
   (EXIF, GPS, maker notes) is dropped** — a hard privacy requirement. EXIF *orientation* is applied
   first (`ImageOps.exif_transpose`) so a portrait phone photo doesn't end up sideways once the tag
   is gone.
4. **Smart compression** — the cleaned image is downscaled to `MEDIA_MAX_DIMENSION` (longest side)
   and transcoded to `MEDIA_IMAGE_OUTPUT_FORMAT` at `MEDIA_IMAGE_QUALITY`. **AVIF is the default**
   (ADR-0026): ~15–30 % smaller than WebP at matched perceptual quality, decodable by every
   evergreen browser and Safari/iOS ≥ 16.4; set `WEBP` to roll back with one env var (pre-existing
   objects are never re-encoded either way). `MEDIA_IMAGE_QUALITY=0` (default) auto-picks the
   matched-quality value per codec (AVIF 64 ≈ WebP 80). Alpha is preserved for AVIF/WebP/PNG;
   flattened onto white for JPEG. An animated image (animated WebP/GIF-like input) is flattened to
   its first frame — the *image* pipeline has no animation surface (video is its own gated
   pipeline, §9). Images larger than a codec's hard per-side limit (WebP 16383 px / AVIF 65535 px)
   are downscaled to fit, never rejected.
5. **Rendition** — one extra small object (`MEDIA_THUMB_DIMENSION`, default 800 px) is generated
   eagerly from the clean bytes and served on card/stream surfaces (discovery cards, thread
   streams, photo grids, avatars) — with signed URLs straight off object storage there is no CDN
   to negotiate formats or resize on the fly, so the rendition must exist as its own object.
   Sources already that small get none (serving falls back to the full object, which is also the
   behaviour for every pre-rendition row — no backfill needed). Renditions are never used for
   hashing, dedup, or scanning.
6. **Store** — `get_storage().save(key, clean_bytes, content_type=…)`. The DB row records the
   *post-compression* `byte_size` + `sha256`, so dedup/quotas reflect the stored object.

> Profile pictures additionally enforce same-cohort uniqueness (exact `sha256` + a perceptual hash)
> before storing, with a generic rejection message so it can't be used as a presence oracle.

## 3. Serving — private by construction

Blobs are **never** exposed via a public bucket URL. A viewer gets a short-lived signed token
(`MEDIA_SIGNED_URL_TTL`, default 300 s) that resolves through `MediaFileView` /
`AttachmentFileView`, which **re-checks access at request time** (membership + cohort + consent +
not-blocked + not-hidden, via `can_read_thread` / the photo gate) and then streams the bytes with:

- `X-Content-Type-Options: nosniff` on every response;
- images served inline (`Content-Type` from the row, e.g. `image/webp`);
- **PDF forced to download** (`Content-Disposition: attachment`) so a PDF can never execute inline.

Because the app streams bytes through the gate, a leaked token still can't outlive its TTL or cross
the membership wall, and the object's own ACL is irrelevant — it stays private.

## 4. Security properties

| Property | How |
| --- | --- |
| **Private at rest** | No public ACL is set on `put_object`; objects are reachable only via the signed, per-viewer, membership-scoped serving view. |
| **Encrypted at rest** | Optional **server-side encryption** — set `MEDIA_S3_SSE=AES256` (SSE-S3) where the provider supports it; otherwise rely on the provider's default-at-rest encryption. |
| **Encrypted in transit** | The S3 endpoint is HTTPS; the app is served over TLS. |
| **No location/metadata leak** | EXIF/GPS stripped on every image upload (re-encode from raw pixels). |
| **No inline code execution** | `nosniff` everywhere; PDFs forced-download; only PNG/JPEG/WebP images are decoded. |
| **Fail-closed safety** | Uploads refused unless an effective content scanner is configured (prod). |
| **EU data residency** | Enforced at boot — see §5. |

## 5. EU data residency (GDPR Ch. V — minors' data)

`config/settings/prod.py` **hard-fails at boot** if the S3 backend is selected without an EU region
or an explicit (EU) endpoint:

```python
if MEDIA_STORAGE_BACKEND.endswith("S3StorageBackend") and not (
    MEDIA_S3_REGION.lower().startswith("eu") or MEDIA_S3_ENDPOINT_URL
):
    raise ImproperlyConfigured("Media object storage must be in an EU region …")
```

Recommended EU-resident, S3-compatible providers:

| Provider | Residency | Egress | Notes |
| --- | --- | --- | --- |
| **Hetzner Object Storage** | EU-owned (DE/FI) | ~€1/TB after 1 TB included | Recommended default; EU-owned, cheapest, already the `docs/HOSTING_EU.md` stack. |
| **Cloudflare R2** (EU jurisdiction) | EU jurisdiction flag | **zero egress** | US processor (DPA/SCCs); great if media bandwidth grows. |
| **Backblaze B2** (EU) | EU region (Amsterdam) | free up to 3× stored | US processor; CDN-frontable. |
| **MinIO** (self-hosted, EU box) | wherever you run it | n/a | Full control; you operate it. |

## 6. Compression strategy & cost

- **Why AVIF** (ADR-0026) — at matched perceptual quality AVIF is ~15–30 % smaller than WebP
  (which is itself far smaller than the source PNG/JPEG). A 4 MB phone photo commonly lands
  around ~200 KB after downscale-to-2048 + AVIF@64, plus a ~30–60 KB card rendition. Encode cost
  is sub-second per photo at our sizes (paid once, at upload).
- **Renditions** — most media views are cards and thread streams; serving the 800 px rendition
  there instead of the 2048 px object cuts per-view egress ~4–8× on the hottest surfaces. This is
  the "transcode once, serve size-appropriate copies forever" pattern every large platform uses,
  minus the ladder we don't need at this scale.
- **What it buys** — storage and egress scale with *stored bytes*. Cutting the average blob and
  serving small where small is displayed cuts both the monthly storage bill and per-view egress —
  the cheapest levers, with no architecture change.
- **Tuning** — set `MEDIA_IMAGE_QUALITY` explicitly for crisper/smaller output (0 = per-codec
  auto); lower `MEDIA_MAX_DIMENSION` (e.g. 1600) for an even smaller footprint;
  `MEDIA_IMAGE_OUTPUT_FORMAT=WEBP` to roll back the codec; `""` preserves the source format.

## 7. Settings reference

```bash
# --- storage backend ---
MEDIA_STORAGE_BACKEND=apps.media.storage.S3StorageBackend   # default: LocalStorageBackend (dev)
MEDIA_S3_BUCKET=socialapp-media
MEDIA_S3_ENDPOINT_URL=https://fsn1.your-objectstorage.com   # Hetzner Object Storage endpoint (EU)
MEDIA_S3_REGION=eu-central                                  # any eu* OR rely on the endpoint
MEDIA_S3_ADDRESSING_STYLE=virtual                           # "path" for MinIO
MEDIA_S3_SSE=AES256                                         # optional server-side encryption (SSE-S3)
AWS_ACCESS_KEY_ID=<key>                                     # boto3 default credential chain
AWS_SECRET_ACCESS_KEY=<secret>

# --- smart compression (photos AND attachments) ---
MEDIA_IMAGE_OUTPUT_FORMAT=AVIF        # transcode codec (WEBP = rollback; "" = preserve source)
MEDIA_IMAGE_QUALITY=0                 # 0 = auto per codec (AVIF 64 / WebP 80); else 1-100
MEDIA_THUMB_DIMENSION=800             # card/stream rendition longest side (0 disables)
MEDIA_MAX_DIMENSION=2048              # longest-side downscale cap
MEDIA_MAX_IMAGE_PIXELS=30000000       # decompression-bomb ceiling (header-declared)
MEDIA_MAX_UPLOAD_BYTES=5242880        # profile/photo size cap
MEDIA_ATTACHMENT_MAX_BYTES=7340032    # thread-attachment size cap

# --- video attachments (ADR-0026; default ON since 2026-07-13) ---
MEDIA_VIDEO_ENABLED=true              # requires ffmpeg/ffprobe (boot-checked in prod); false = kill switch
MEDIA_VIDEO_COHORTS=adult             # adults-only at launch (the PDF precedent)
MEDIA_VIDEO_MAX_UPLOAD_BYTES=83886080 # 80 MiB source cap
MEDIA_VIDEO_MAX_DURATION_SECONDS=90
MEDIA_VIDEO_TARGET_MAX_SIDE=1280      # one 720p-class progressive MP4 (never upscaled)
MEDIA_VIDEO_CRF=23                    # x264 quality (lower = better/bigger)
MEDIA_VIDEO_PRESET=medium

# --- serving ---
MEDIA_SIGNED_URL_TTL=300              # signed-token lifetime (seconds)
```

### Wiring Hetzner Object Storage (example)

1. Create a private bucket + an S3 access key in the Hetzner console (EU location, e.g. `fsn1`).
2. Set the env above (`MEDIA_STORAGE_BACKEND`, `MEDIA_S3_*`, `AWS_*`). Keep the bucket **private** —
   the app never needs public read.
3. Deploy. The prod boot guardrail confirms EU residency; uploads now land in Hetzner, compressed.

## 8. Scaling & future options

- **Direct GETs (presigned redirect) — IMPLEMENTED, opt-in.** Set `MEDIA_REDIRECT_TO_PRESIGNED=True`
  (S3 backend only): after the per-viewer access check, the serving view 307-redirects to a
  short-lived (`MEDIA_PRESIGNED_TTL`, default 60s) presigned object-store URL so the bytes never
  transit the app process — the biggest single-process saturation fix. PDFs keep forced-download +
  content-type via the presign response overrides. **Trade-off:** while a presigned URL is live a
  block / moderation-hide / consent revocation / ephemeral expiry is not yet enforced (the streaming
  path re-authorizes per byte; the redirect does not) — hence the short, *decoupled* TTL. Default
  OFF keeps the secure streaming model. Front it with a CDN for further egress savings.
- **Thumbnails — IMPLEMENTED (ADR-0026).** One eager card/stream rendition per image (§2 step 5),
  tracked by `thumb_storage_key`; card and stream surfaces serve it, detail/click-through serves
  the full object. Pre-existing rows simply fall back to the full object; a backfill command is a
  possible follow-up if serving data shows it is worth it.
- **Lifecycle rules** — ephemeral ("disappearing") pictures already expire + purge in-app; you can
  add a provider lifecycle rule as defence-in-depth.

## 9. Video attachments (ADR-0026)

Short clips in private, cohort-gated activity/group threads only (adults-only at launch).
Enabled by default since 2026-07-13 (`MEDIA_VIDEO_ENABLED=false` is the kill switch; prod
refuses to boot video-enabled without ffmpeg). Rendered solely inside the owning thread —
never on discovery/cover/feed surfaces, never in DMs, no autoplay/loops/view counts.

```
upload ─▶ size cap ─▶ magic sniff ─▶ cohort + membership gates
       ─▶ fail-closed scanner gate + streamed SHA-256 of the ORIGINAL vs blocklist/service
       ─▶ original stored to video-src/ (quarantine) ─▶ Attachment row status=pending (WITHHELD)

transcode_videos (systemd timer / post-upload kick; claim = select_for_update skip_locked):
  ffprobe validation (container/codec/pixel-format whitelists, 1 video + ≤1 audio stream,
                      duration + dimension caps — decode-bomb classes rejected up front)
  ─▶ ffmpeg transcode: ONE progressive MP4 — x264 High@4.1, CRF 23, ≤1280px, yuv420p, AAC,
      -map_metadata -1 -map_chapters -1 (the re-encode IS the GPS/metadata strip),
      autorotate baked, +faststart  [sandboxed: -nostdin, protocol whitelist=file, wall-clock
      timeout + process-group kill, RLIMIT_CPU/AS, thread cap]
  ─▶ poster frame from the OUTPUT through the ordinary image pipeline (AVIF/WebP)
  ─▶ FRAME SCAN: sampled frames (1 per 5s, capped) through the configured image scanner —
      the perceptual dHash blocklist catches known-bad imagery inside the video (fail-closed;
      a match ⇒ status=blocked, never served, source retained as moderation evidence)
  ─▶ store MP4 + poster ─▶ status=ready, quarantined original DELETED (it still carried the
      source metadata) ─▶ audit

serve ─▶ same per-viewer signed tokens; the streaming view supports HTTP Range (206) so the
        player can seek; posters serve via the image path. Non-ready rows render as a calm
        placeholder. MEDIA_REDIRECT_TO_PRESIGNED offloads Range serving to the object store.
```

Failure honesty: transient errors retry with an attempt cap; a crashed worker's `processing`
row is reclaimed after `MEDIA_VIDEO_STALE_PROCESSING_SECONDS`; terminal failures show a
"couldn't be processed" placeholder and reclaim every blob. Ephemeral expiry + purge + the
Art. 17 blob-cleanup signals cover all four keys (main / poster / thumb / quarantined source).
