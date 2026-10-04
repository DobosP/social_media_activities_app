# Native Social runtime and operator entry points

This is the configuration reference for the native Go serving backend. Deployment
and activation status remain in [STATUS.md](../../../../STATUS.md); safety invariants
remain in [SAFETY.md](../../../../docs/SAFETY.md) and decisions in
[docs/adr](../../../../docs/adr/). No command below activates a recurring scheduler.

```sh
social-server --migrate-only
social-server --due
social-server --due --reminder-within-hours 48
social-server --job transcode_videos --job-options /private/video-options.json
social-server --job ingest_places --job-options -
social-server --job createsuperuser --job-options -
social-server --dev --site-root /app --static-dir /app/static \
  --media-dir /private/media --media-scratch /private/media-work
social-server --dev --dev-container --listen 0.0.0.0:8000 --site-root /app
```

`--migrate-only` installs native database contracts and exits without starting HTTP,
media processing, the live listener or jobs. `--migrate` installs those contracts before
the selected serving/job path. `--job` and `--due` are mutually exclusive. Job options
are one JSON object, at most 64 KiB and 32 top-level keys, from a regular nonsymlink
file or stdin; duplicate keys, trailing documents and unknown command fields fail.

The 27 due jobs use the concrete `internal/jobs` registry. Manual due overrides are:
`limit` for attachment purge/video processing/deferred tasks, `grace_hours` for activity
completion, `retention_hours` for arrivals, `within_hours` for reminders, `city` for
RO-EDU sync, and `window_hours`/`max_urls` for IndexNow. Caps are enforced before work.
The separate [manual command registry](../../internal/commands/README.md) exposes
native ingestion/enrichment, maintenance, seed and demo commands. Demo helpers require
validated loopback development mode; a `force` option cannot grant that mode.

Initial administrator bootstrap is an explicit `createsuperuser` operator command
(`create_superuser` is an alias). It accepts exactly `username` and `password` from
secret-producer JSON on stdin, never a saved job-options file or shell arguments.
It hashes through shared authentication and creates one fresh active staff/superuser
account with role `admin`, unknown age, unassigned cohort and no identity/consent proof.
Existing accounts, including case variants, are refused; privilege creation and audit
commit atomically. Output contains only the new numeric ID and status. This does not
grant activity participation or minor onboarding and never runs at startup or via demo
mode. Native audited staff policies remain the administrative write boundary.

## Configuration boundaries

The executable reads explicit environment variables; it does not load `.env`, import
Python providers or use credential/service files as a database fallback. `DATABASE_URL`
requires a PostgreSQL URL with explicit user/password, host and database. Errors report
setting names, never values. A strong `DJANGO_SECRET_KEY` is mandatory; enforced
identity uniqueness also requires a distinct strong `IDENTITY_BINDING_SECRET`.

Unset values and explicit empty values are distinguished. Empty connection cohorts
turn connections off. Empty hard-coded media cohort lists, malformed booleans/integers,
empty JSON provider maps and incompatible overrides fail by name. Unknown Python
provider paths fail; reviewed storage/scanner/booking/donation/EUDI/RO-EDU aliases map
to typed native implementations. Explicit production EUDI configuration requires
valid P-256 issuer public keys. Minor onboarding is off unless explicitly configured.

Supported native settings include OAuth/EUDI and account TTLs, conservative media
size/dimension/time budgets, sentiment/community thresholds, connection cohorts,
minute API rates, proxy trust, public site metadata, S3 policy, snapshots and source
clients. `MEDIA_ROOT` supplies local storage unless `--media-dir` is explicit. Local
storage and demo seeding require `--dev` or `DJANGO_DEBUG=true` plus a loopback canonical
origin. S3 requires both `MEDIA_EU_RESIDENCY_VERIFIED=true` and
`MEDIA_PRIVATE_BUCKET_VERIFIED=true`; `MEDIA_S3_SSE` accepts empty, `AES256`, `aws:kms`
or `aws:kms:dsse`, and addressing accepts `auto`, `path` or `virtual`.

Direct development listeners bind loopback. An explicit `--dev --dev-container`
exception permits a container listener such as `0.0.0.0:8000` while the canonical
origin remains loopback. Container ports must be published on host loopback only
(for example `127.0.0.1:8000:8000`), and development fixtures must not be exposed to
the Internet. The executable cannot verify Docker's external port publication;
this flag is an explicit operator assertion rather than an automatic exception.

`TRUSTED_PROXY_CIDRS` is empty unless explicitly configured. `NUM_PROXIES` selects a
bounded rightmost forwarded hop only when the immediate peer is trusted; spoofed or
malformed forwarding headers cannot select an arbitrary rate-limit identity. Production
runtime settings include HTTPS redirects, source HSTS headers and aggregate request logs;
`DJANGO_SECURE_SSL_REDIRECT`, `DJANGO_HSTS_SECONDS`, `REQUEST_LOGGING_ENABLED` and
`LOG_FORMAT=json|plain` map to native configuration. Logs omit queries, identities,
credentials, cookies and bodies. Public branding/verification settings map to escaped
native template context.

HTTP serving binds background work to application lifetime. A committed private video
upload can trigger a bounded single-flight processing pass when
`MEDIA_VIDEO_INLINE_PROCESSING=true`; startup itself processes nothing. Shutdown marks
draining, waits for the bounded HTTP drain, then cancels/waits for background media and
releases live resources before closing storage/database handles. One-shot jobs never
reserve the PostgreSQL live-listener connection.

`ffmpeg` and `prlimit` are required for images; enabled video also requires `ffprobe`,
and AVIF output requires `avifenc`. Missing tools abort serving/job startup. Migration-only
execution is independent of those tools. Scanner failures withhold uploads; no effective
hash list or document scanner never produces a clean verdict.

## Exact unsupported override inventory

The following names currently require the listed source value because their native
policy has no configurable seam. An incompatible value aborts startup; it is not ignored.

| Setting names | Accepted value |
|---|---|
| `CLOSURE_REPORT_THRESHOLD`, `OPEN_NOW_REPORT_THRESHOLD`, `FACT_QUORUM`, `CORRECTION_QUORUM`, `EDGE_QUORUM`, `EVENT_REPORT_THRESHOLD`, `INTEREST_THRESHOLD` | `3` |
| `CLOSURE_REPORT_DECAY_SECONDS`, `OPEN_NOW_REPORT_DECAY_SECONDS`, `EVENT_REPORT_DECAY_SECONDS` | `1209600` |
| `CLOSURE_REPORT_RATE_LIMIT`, `OPEN_NOW_REPORT_RATE_LIMIT`, `EVENT_REPORT_RATE_LIMIT` | `10` |
| `CLOSURE_REPORT_RATE_WINDOW_SECONDS`, `OPEN_NOW_REPORT_RATE_WINDOW_SECONDS`, `FACT_VOTE_RATE_WINDOW_SECONDS`, `EVENT_REPORT_RATE_WINDOW_SECONDS`, `SAVED_SEARCH_RATE_WINDOW_SECONDS`, `GUARDIAN_INVITE_RATE_WINDOW_SECONDS`, `GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS`, `CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS`, `GROUP_CREATE_RATE_WINDOW_SECONDS`, `GROUP_JOIN_RATE_WINDOW_SECONDS`, `GROUP_QUESTION_RATE_WINDOW_SECONDS`, `AVATAR_UPLOAD_RATE_WINDOW_SECONDS` | `3600` |
| `FACT_VOTE_RATE_LIMIT` | `40` |
| `SERIES_SPAWN_LEAD_DAYS`, `INTEREST_LIFETIME_DAYS` | `14` |
| `SERIES_SPAWN_BATCH` | `500` |
| `SAVED_SEARCH_RATE_LIMIT`, `SAVED_SEARCH_MAX_PER_USER`, `GUARDIAN_INVITE_RATE_LIMIT`, `CONNECTIONS_REQUEST_RATE_LIMIT`, `GROUP_JOIN_RATE_LIMIT`, `MESSAGING_START_RATE_LIMIT`, `AVATAR_UPLOAD_RATE_LIMIT` | `20` |
| `SAVED_SEARCH_MATCH_BATCH` | `1000` |
| `SAVED_SEARCH_NOTIFY_RATE_LIMIT` | `50` |
| `SAVED_SEARCH_NOTIFY_WINDOW_SECONDS`, `MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS` | `86400` |
| `GUARDIAN_GUARDRAIL_RATE_LIMIT`, `THREAD_POST_RATE_LIMIT` | `30` |
| `THREAD_POST_RATE_WINDOW_SECONDS`, `THREAD_REACT_RATE_WINDOW_SECONDS`, `MESSAGING_RATE_WINDOW_SECONDS` | `60` |
| `THREAD_REACT_RATE_LIMIT`, `MESSAGING_SEND_RATE_LIMIT`, `PLACE_PROPOSAL_DEDUP_RADIUS_M`, `MEDIA_PRESIGNED_TTL` | `60` |
| `SOCIAL_THREAD_POST_LIMIT`, `SOCIAL_MEMBERSHIP_LIST_LIMIT`, `COMMUNITY_ACTIVITIES_PAGE_SIZE`, `DEFERRED_TASKS_BATCH` | `100` |
| `UNSAFE_REPORT_RATE_LIMIT` | `12` |
| `UNSAFE_REPORT_RATE_WINDOW_SECONDS` | `3600` |
| `UNSAFE_REPORT_COOLDOWN_SECONDS`, `MEDIA_SIGNED_URL_TTL` | `300` |
| `GROUP_CREATE_RATE_LIMIT`, `MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS`, `DEFERRED_TASKS_MAX_ATTEMPTS` | `5` |
| `GROUP_QUESTION_RATE_LIMIT`, `ARRIVAL_RETENTION_HOURS` | `6` |
| `MESSAGING_MAX_CIPHERTEXT_BYTES` | `65536` |
| `MESSAGING_MAX_GROUP_MEMBERS` | `256` |
| `ARRIVAL_WINDOW_BEFORE_HOURS` | `2` |
| `ARRIVAL_WINDOW_AFTER_HOURS`, `DEPARTURE_WINDOW_AFTER_HOURS`, `MEDIA_VIDEO_MAX_ATTEMPTS` | `3` |
| `MAX_REQUEST_BODY_BYTES`, `DATA_UPLOAD_MAX_MEMORY_SIZE` | `8388608` |
| `CHAT_MAX_LENGTH` | `4000` |
| `MEDIA_IMAGE_QUALITY` | `0` |
| `MEDIA_ATTACHMENT_MAX_BYTES` | `7340032` |
| `MEDIA_EPHEMERAL_MIN_TTL_SECONDS` | `3600` |
| `MEDIA_VIDEO_CRF` | `23` |
| `MEDIA_VIDEO_STALE_PROCESSING_SECONDS` | `1800` |
| `MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES` | `25` |
| `MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP` | `10000` |
| `CHILD_PUBLIC_VENUES_ONLY`, `MEDIA_REQUIRE_SCANNER`, `MEDIA_ATTACHMENTS_ENABLED`, `DB_POOL_ENABLED` | `true` |
| `PROGRESSION_AVATAR_PUBLIC`, `IDENTITY_ALLOW_DEV_PROVIDER`, `EUDI_SANDBOX`, `DB_POOLED`, `DJANGO_REQUIRE_SHARED_STATE` | `false` |
| `GROUPS_USER_CREATION_COHORTS`, `SUPPORT_COMPANION_COHORTS`, `MEDIA_FILE_COHORTS`, `MEDIA_VIDEO_COHORTS` | `adult` |
| `CHAT_MESSAGE_POLICY` | `apps.chat.policy.NudgeMessagePolicy` |
| `THREAD_REACTION_FACETS`, `EUDI_SANDBOX_ISSUER_KEY_PEM`, `REDIS_URL`, `SENTRY_DSN` | empty/unset |
| `MEDIA_VIDEO_PRESET`, `MEDIA_VIDEO_AUDIO_BITRATE`, `LOG_LEVEL` | `medium`, `96k`, `INFO`, respectively |
| `PERMISSIONS_POLICY` | `geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()` |
| `ROEDU_APP_PACK` | `roedu:social_media_activities_app:events_places:v1` |
| `DB_POOL_TIMEOUT`, `SENTRY_TRACES_SAMPLE_RATE` | `10`, `0`, respectively |

`REVERIFY_SWEEP_BATCH` and `CONSENT_SWEEP_BATCH` must agree because the shared native
runner currently exposes one sweep batch. Media caps accept tightening within the
native processor's hard limits. Supported sentiment/privacy floors cannot be lowered
below the reviewed source defaults. API throttle strings support positive minute rates
only. Database pools are bounded to two through four connections. These are explicit
configuration constraints, not evidence of production readiness.

API, authentication and several domain abuse budgets remain process-local. PostgreSQL
provides shared live fanout and the durable deferred queue, but does not make those
budgets global. Consequently nonempty `REDIS_URL`, enabled
`DJANGO_REQUIRE_SHARED_STATE` and configured Sentry are refused. Production scale/shared
abuse controls and native external error tracking still require qualified native seams.

## Source clients and qualification

RO-EDU is an HTTP consumer of the immutable canonical facts-only app pack, never a
producer-internal import. `ROEDU_SYNC_ENABLED` is explicit; enabled sync requires a URL
and key. Commons scratch is the same private codec scratch. IndexNow and snapshot export
retain opt-in/public gates. Heartbeats execute only after a clean complete due pass.

Google enrichment requires `GOOGLE_PLACES_ENABLED` plus a key and an explicit manual
command. It persists only the source durable overlay/contact fields, never live opening
status. Wikidata uses bounded 50-QID batches and fills missing public websites only.
Normal external HTTP pins checked public DNS addresses, rejects redirects and caps data;
all qualification transports are synthetic.

Overture uses pinned `parquet-go` v0.32.0, local globs, HTTPS conditional range reads or
anonymous public S3 prefix listing. It projects source columns, filters an explicit bbox,
skips unrelated row groups using bbox indexes and preserves source category/address/website
normalization. Bounds: 1000 files, 2 GiB per file, 8 MiB footer, 5 million scan/file rows,
8 MiB individual remote reads and 256 MiB transferred per file. Unsupported/mutable sources
fail rather than producing an apparently complete import.

Hermetic tests use only synthetic env maps, files and HTTP transports. The explicit
PostGIS IndexNow regression uses an isolated per-test schema in the disposable task database;
no live credentials, media, source feeds, ingestion or scheduler activation are part of
qualification. Qualified codec/container and full application receipts belong to the
repository verification record.
