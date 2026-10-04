# Native private file storage

Verified against native Go on 2026-10-04. [ADR-0032](adr/0032-complete-native-go-backend.md)
records the native runtime; [ADR-0026](adr/0026-private-thread-video-and-sota-image-compression.md)
records the image/video policy. Safety, privacy, EU residency and source/license credits
remain mandatory. [STATUS](../STATUS.md) records launch and provider activation gates.

## Byte and authorization boundaries

[internal/media](../services/server/internal/media/) owns quarantined admission, bounded
native codecs, private storage and serving. `Store` provides `Put`, `OpenRange`, `Size`,
`Delete` and `PresignGet`; it controls bytes only. PostgreSQL photo/attachment rows hold
private references, scanner/processing metadata and expiry. A processed manifest or
object key never grants access.

`LocalStore` uses rooted filesystem operations and private directories for loopback
development/tests. Production requires native S3-compatible storage with HTTPS and
explicit `MEDIA_EU_RESIDENCY_VERIFIED` and `MEDIA_PRIVATE_BUCKET_VERIFIED` attestations.
R2 and MinIO endpoints are refused. The service creates no bucket or provider. Operator
verification must cover EU residency, bucket access policy, key scope, encryption and
lifecycle; endpoint/region spelling alone proves none of those facts.

S3 uses the native signature implementation, bounded range reads and redirects disabled
for storage HTTP calls. Supported addressing is `auto`, `path` or `virtual`; encryption
configuration accepts empty, `AES256`, `aws:kms` or `aws:kms:dsse`. With empty SSE, the app
makes no claim that provider-side encryption is enabled: verify the chosen storage policy.
No boto3/Pillow or Python worker is a serving dependency.

## Image and PDF admission

1. Recheck the actor, owning domain, cohort/consent/membership/block rules and upload
   budget. Stage bytes in service-owned private scratch. Images accept PNG, JPEG, WebP
   and AVIF; unsupported/malformed input fails rather than trusting the filename.
2. Parse image headers before decoding. Current hard ceilings are 5 MiB and 30 million
   pixels. A small pixel-bomb file does not bypass the dimension budget.
3. Scan the original digest through an effective content scanner. Derived fingerprints
   use the same fail-closed seam. Missing/invalid/ineffective or non-clean results refuse
   publication; a no-op scanner cannot produce a clean admission.
4. Decode/rebuild through bounded native tools, bake orientation into pixels and strip
   metadata including EXIF/GPS. Produce one still image at up to 2048 pixels on the long
   side; AVIF is the current configured default, WebP the supported alternative. Alpha
   is retained in these outputs. An eager rendition is capped at 800 pixels when needed.
5. Hash the encoded stored artifact. Store bytes privately and publish the owning row,
   media reference, audit and related notifications through the governed transaction.
   Profile uniqueness checks use exact digest and the bounded difference fingerprint;
   the fingerprint remains a duplicate heuristic, not a safety classifier.

PDF attachments are adult-only, capped at 7 MiB, checked for PDF magic, and require an
effective clean document scan. The native ClamAV INSTREAM adapter is bounded and fails
closed. PDFs are retained as documents and always served as downloads, never inline code.

`ffmpeg` and `prlimit` are required; AVIF additionally requires `avifenc`. Native processing
uses file-only protocols, thread/address-space/CPU/wall-time ceilings and process-group
cancellation. The actual codec runtime must pass qualification; absent tools or skipped
tests do not establish codec readiness. See the [native media contract](../services/server/internal/media/README.md).

## Atomic attachment and licensed-cover workflows

Prepared attachment artifacts are private before a post is created. `PreparedAttachment`
publishes inside the owning `pgx.Tx`, rechecks current permission, and can be used only
once. `Finish` records the commit outcome and cleans abandoned artifacts. An invalid/no-op
publisher cannot create a text-only post that claims an attachment. Failed/ambiguous
remote writes and cleanup failures retain durable object-deletion continuation.

Place covers require current governed place-management authority. An approved business
claim must still belong to an active verified partner for that exact place. The bounded
Commons import accepts reviewed licensed source data, preserves license/attribution/wiki
provenance and an existing cover, and uses the same scanner/codec/private-storage gates.
Its separate public-source download ceiling is 8 MiB; it does not enlarge ordinary
request or private-image admission budgets. Public serving still rechecks place/activity
visibility and withdrawal/moderation state.

## Private serving

Signed references bind model/row, viewer, variant and expiry with a purpose-specific MAC.
The configured token lifetime is 300 seconds. Each serving request reloads current
account status, ownership/membership, cohort/consent, blocks, moderation, scanner/ready
state and expiry. Anonymous references apply only to currently public venue/adult-activity
covers. A leaked token cannot replace current permission.

Streaming returns `private, no-store`, `nosniff` and a restrictive media CSP. PDFs have
attachment disposition; videos support a single bounded byte range. Authorization is
checked at request admission, not on every byte. Expiring media also binds the streaming
context deadline; the next request must pass current gates again.

`MEDIA_REDIRECT_TO_PRESIGNED=true` is opt-in for S3. After the same application access
check it returns a short private object URL, at most 60 seconds and no longer than the
media's remaining lifetime. PDF download/content-type overrides remain. A copied presign
can survive a subsequent block, moderation hide or consent revocation until it expires;
this is the explicit revocation trade-off. Do not make the bucket public or place private
media behind a public CDN.

## Private thread videos

Videos are confined to adult private activity/group threads with current membership and
consent. They do not appear in discovery/covers or DMs, and add no autoplay/loop/view-count
surface. Enabled video requires `ffprobe` as well as the native processing tools.

Admission scans the original digest and commits a withheld pending row with a private
quarantined source. Current ceilings are 80 MiB, 90 seconds, source side 3840 and output
side 1280. The worker checks approved MP4/WebM codec/container inputs, produces one
progressive H.264/AAC MP4 plus AVIF/WebP poster, and scans bounded sampled frames including
frame zero. Short claim/finalization transactions surround codec work; a ready row also
requires fresh domain authorization.

With `MEDIA_VIDEO_INLINE_PROCESSING=true` (current default), a committed authorized upload
may kick one application-lifetime, single-flight pass of at most two queued videos.
Startup performs no video pass. False disables the kick; the explicit `transcode_videos`
job/timer remains the retry path. Stale processing is reclaimable after 30 minutes and
three attempts exhaust processing. Terminal processing failure deletes quarantined source
bytes through durable cleanup; safety-blocked source remains private evidence with no
in-app byte URL, including for staff. Pending/failed/blocked rows have no ready byte URL.

Only designated media multipart routes permit the bounded larger video body. Ordinary
API/HTML bodies remain capped at 8 MiB; a group post's multipart video path uses the same
native media budget and atomic attachment contract.

## Supported settings and cleanup

| Setting names | Current default / bound |
|---|---|
| `MEDIA_IMAGE_OUTPUT_FORMAT` | `AVIF`; `WEBP` supported, empty/preserve-source rejected |
| `MEDIA_MAX_UPLOAD_BYTES`, `MEDIA_MAX_IMAGE_PIXELS` | 5 MiB, 30 million; may tighten |
| `MEDIA_MAX_DIMENSION`, `MEDIA_THUMB_DIMENSION` | 2048, 800; positive values may tighten, zero does not disable renditions |
| `MEDIA_IMAGE_QUALITY` | Fixed codec policy (`0`); arbitrary quality overrides refused |
| `MEDIA_ATTACHMENT_MAX_BYTES` | Fixed 7 MiB PDF ceiling |
| `MEDIA_VIDEO_ENABLED`, `MEDIA_VIDEO_INLINE_PROCESSING` | True; false is supported |
| `MEDIA_VIDEO_MAX_UPLOAD_BYTES`, `MEDIA_VIDEO_MAX_DURATION_SECONDS` | 80 MiB, 90; may tighten |
| `MEDIA_VIDEO_MAX_SOURCE_SIDE`, `MEDIA_VIDEO_TARGET_MAX_SIDE` | 3840, 1280; may tighten within validated bounds |
| `MEDIA_SIGNED_URL_TTL`, `MEDIA_PRESIGNED_TTL` | Fixed 300 seconds, at most 60 seconds |

The [CLI reference](../services/server/cmd/social-server/README.md) owns the exact complete
setting inventory and unsupported overrides. Values come only from the approved secrets
workflow; configuration names and examples do not authorize provider activation.

Ephemeral image attachments retain the one-hour adult and 24-hour minor TTL floors.
Expiry is gated independently from physical deletion. Delete triggers and the private
outbox capture main/rendition/poster/source keys across direct row/domain/account deletion;
bounded drains retry storage failures. Pending safety reports, removal holds and blocked/
processing evidence are not blindly purged. Preserve the governed erasure/evidence policy
when configuring bucket lifecycle/versioning, and never clear an outbox to hide failures.

See [ASYNC_TASKS](ASYNC_TASKS.md), [HOSTING_EU](HOSTING_EU.md) and [RUNBOOK](RUNBOOK.md).
The complete earlier storage guide is retained as
[historical reference](archive/file-storage-native-go-reference.md).
