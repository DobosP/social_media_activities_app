# File storage — secure, EU-resident, cheap, smart-compressed

How private blobs (profile pictures, in-thread photos, PDF attachments) are stored, served, and
kept small. Design goals, in priority order: **child-safe + secure → EU data residency → cheap**.
PostgreSQL stays the single primary datastore (relational + geo + graph + pgvector); only *bytes*
live in object storage.

See also: `docs/HOSTING_EU.md` (where to run it) and `docs/SAFETY.md` (safety invariants).

---

## 1. Architecture at a glance

```
upload ─▶ size/format/bomb checks ─▶ safety scan (original bytes, fail-closed)
       ─▶ EXIF/GPS strip + orientation bake ─▶ SMART COMPRESS (transcode → WebP @ quality)
       ─▶ StorageBackend.save(key, bytes, content_type)   [private object]
                                   │
row in Postgres (Photo / media.Attachment): storage_key, content_type, byte_size, sha256, w/h, …
                                   │
serve ─▶ signed, per-viewer, membership-scoped token ─▶ view re-checks access ─▶ streams bytes
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
   and transcoded to `MEDIA_IMAGE_OUTPUT_FORMAT` at `MEDIA_IMAGE_QUALITY`. **WebP is the default** —
   for a typical phone photo it is far smaller than the source PNG/JPEG, which is the whole point:
   cheaper EU object storage + less egress. Alpha is preserved for WebP/PNG; flattened onto white
   for JPEG. **One upload still equals one stored object** — there is no separate thumbnail to track
   (kept deliberately simple; a display/thumbnail variant is a possible future optimisation, see §7).
  An animated image (animated WebP/GIF-like input) is flattened to its first frame — there is no
  animation/short-video surface, consistent with the text-first invariant. Images larger than a
  codec's hard per-side limit (WebP 16383 px) are downscaled to fit, never rejected.
5. **Store** — `get_storage().save(key, clean_bytes, content_type=…)`. The DB row records the
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

- **Why WebP** — lossy WebP at a moderate quality is typically 25–35 % smaller than equivalent JPEG
  and *much* smaller than a PNG photo, with no visible loss at chat display sizes. A 4 MB phone
  photo commonly lands well under ~300 KB after downscale-to-2048 + WebP@80.
- **What it buys** — storage and egress scale with *stored bytes*. Cutting the average blob ~5–10×
  cuts both the monthly storage bill and per-view egress by the same factor — the cheapest lever,
  with no architecture change.
- **Tuning** — raise `MEDIA_IMAGE_QUALITY` (e.g. 85) for crisper images, lower it (e.g. 72) to save
  more; lower `MEDIA_MAX_DIMENSION` (e.g. 1600) for an even smaller footprint. Set
  `MEDIA_IMAGE_OUTPUT_FORMAT=""` to preserve the source format (no transcode) if ever needed.

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
MEDIA_IMAGE_OUTPUT_FORMAT=WEBP        # transcode codec ("" = preserve source format)
MEDIA_IMAGE_QUALITY=80                # lossy quality 1–100
MEDIA_MAX_DIMENSION=2048              # longest-side downscale cap
MEDIA_MAX_IMAGE_PIXELS=30000000       # decompression-bomb ceiling (header-declared)
MEDIA_MAX_UPLOAD_BYTES=5242880        # profile/photo size cap
MEDIA_ATTACHMENT_MAX_BYTES=7340032    # thread-attachment size cap

# --- serving ---
MEDIA_SIGNED_URL_TTL=300              # signed-token lifetime (seconds)
```

### Wiring Hetzner Object Storage (example)

1. Create a private bucket + an S3 access key in the Hetzner console (EU location, e.g. `fsn1`).
2. Set the env above (`MEDIA_STORAGE_BACKEND`, `MEDIA_S3_*`, `AWS_*`). Keep the bucket **private** —
   the app never needs public read.
3. Deploy. The prod boot guardrail confirms EU residency; uploads now land in Hetzner, compressed.

## 8. Scaling & future options (not built — keep the design simple first)

- **CDN / direct GETs** — today the app streams every blob through the gate (correct + simple, fine
  at launch scale). If image bandwidth grows, front the *serving view* with a CDN, or issue
  short-lived presigned GETs for already-authorised viewers (the object `ContentType` is set on
  upload precisely so a future presigned/CDN path serves the right type).
- **Thumbnails** — a separate small display variant would cut per-view bytes further, at the cost of
  a second stored object per image. Deferred to keep the one-upload-one-object design.
- **Lifecycle rules** — ephemeral ("disappearing") pictures already expire + purge in-app; you can
  add a provider lifecycle rule as defence-in-depth.
