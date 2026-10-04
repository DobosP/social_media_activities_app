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

## Policy override inventory

[ADR-0034](../../../../docs/adr/0034-native-config-error-observability.md) records
native configuration and error reporting. Explicit malformed/empty numeric values
fail by setting name. The following formerly fixed controls have typed native
hooks; changes take effect at the shared domain boundary and retain safety gates.

| Setting names | Native bounds / source defaults |
|---|---|
| `CLOSURE_REPORT_THRESHOLD`, `OPEN_NOW_REPORT_THRESHOLD`, `EVENT_REPORT_THRESHOLD` | 1..3; source 3; only tighter withholding/warnings |
| `CLOSURE_REPORT_DECAY_SECONDS`, `OPEN_NOW_REPORT_DECAY_SECONDS`, `EVENT_REPORT_DECAY_SECONDS` | 1209600..31536000; source 1209600 |
| `FACT_QUORUM`, `CORRECTION_QUORUM`, `EDGE_QUORUM` | 3..100; source 3 |
| `CHAT_MAX_LENGTH` | 1..4000; source 4000 |
| `SOCIAL_THREAD_POST_LIMIT`, `SOCIAL_MEMBERSHIP_LIST_LIMIT`, `COMMUNITY_ACTIVITIES_PAGE_SIZE` | 1..1000; source 100 |
| `SERIES_SPAWN_LEAD_DAYS`, `INTEREST_LIFETIME_DAYS` | 1..90; source 14 |
| `SERIES_SPAWN_BATCH` | 1..1000; source 500 |
| `INTEREST_THRESHOLD` | 3..1000; source 3 |
| `PLACE_PROPOSAL_DEDUP_RADIUS_M` | 1..1000; source 60 |
| `SAVED_SEARCH_MAX_PER_USER` | 1..100; source 20 |
| `SAVED_SEARCH_MATCH_BATCH` | 1..10000; source 1000 |
| `DEFERRED_TASKS_BATCH`, `DEFERRED_TASKS_MAX_ATTEMPTS` | 1..1000 / 1..100; source 100 / 5; new task defaults only |
| `ARRIVAL_WINDOW_BEFORE_HOURS` | 0..24; source 2 |
| `ARRIVAL_WINDOW_AFTER_HOURS`, `DEPARTURE_WINDOW_AFTER_HOURS`, `ARRIVAL_RETENTION_HOURS` | 0..6 / 0..6 / 1..6; source 3 / 3 / 6; retention must cover after/departure windows |
| `UNSAFE_REPORT_COOLDOWN_SECONDS` | 1..300; source 300; existing open report deduplication remains |
| `MESSAGING_MAX_CIPHERTEXT_BYTES`, `MESSAGING_MAX_GROUP_MEMBERS` | 1..65536 / 2..256; source 65536 / 256 |
| `MAX_REQUEST_BODY_BYTES`, `DATA_UPLOAD_MAX_MEMORY_SIZE` | 1..8388608; source 8388608; native data caps described below |
| `MEDIA_IMAGE_QUALITY` | 0..100; source 0 selects source codec quality (AVIF64/WebP80) |
| `MEDIA_SIGNED_URL_TTL`, `MEDIA_PRESIGNED_TTL` | 1..300 / 1..60 seconds; source 300 / 60 |
| `MEDIA_ATTACHMENT_MAX_BYTES` | 1..7340032; source 7340032 |
| `MEDIA_EPHEMERAL_MIN_TTL_SECONDS`, `MEDIA_EPHEMERAL_MIN_TTL_MINORS_SECONDS` | 3600..86400 / 86400..604800; source 3600 / 86400 |
| `MEDIA_VIDEO_CRF`, `MEDIA_VIDEO_PRESET`, `MEDIA_VIDEO_AUDIO_BITRATE` | 18..40; `ultrafast`, `superfast`, `veryfast`, `faster`, `fast`, `medium`; `32k`, `48k`, `64k`, `96k`, `128k`; source 23 / `medium` / `96k` |
| `MEDIA_VIDEO_MAX_ATTEMPTS`, `MEDIA_VIDEO_STALE_PROCESSING_SECONDS` | 1..10 / 1..1800; source 3 / 1800; stale deadline must exceed every codec command/probe timeout; the whole processing pass expires before reclaim |
| `MEDIA_VIDEO_FRAME_SCAN_INTERVAL_SECONDS`, `MEDIA_VIDEO_FRAME_SCAN_MAX_FRAMES` | 1..5 / 25..100; source 5 / 25; capacity must cover ceil(duration/interval) |
| `MEDIA_PERCEPTUAL_PROFILE_SCAN_CAP` | 10000..100000; source 10000; cannot reduce safety coverage |
| `MEDIA_ATTACHMENTS_ENABLED` | boolean; source true; false disables new attachment admission |
| `GROUPS_USER_CREATION_COHORTS`, `SUPPORT_COMPANION_COHORTS`, `MEDIA_FILE_COHORTS`, `MEDIA_VIDEO_COHORTS` | `adult` or explicit empty (disabled); minors cannot be added |
| `LOG_LEVEL` | `DEBUG`, `INFO`, `WARNING`, `ERROR`, `CRITICAL`; source `INFO`; requests filter at status400/500/panic respectively; debug retains the same privacy-safe fields |
| `PERMISSIONS_POLICY` | reviewed source header, or replace `geolocation=(self)` with `geolocation=()`; other browser permissions stay denied |

Action rate limits accept 1..10000; windows accept 1..86400 seconds. Their settings
and source defaults are:

| Limit / window names | Limit / window source default |
|---|---|
| `CLOSURE_REPORT_RATE_LIMIT` / `CLOSURE_REPORT_RATE_WINDOW_SECONDS`, `OPEN_NOW_REPORT_RATE_LIMIT` / `OPEN_NOW_REPORT_RATE_WINDOW_SECONDS`, `EVENT_REPORT_RATE_LIMIT` / `EVENT_REPORT_RATE_WINDOW_SECONDS` | 10 / 3600 |
| `FACT_VOTE_RATE_LIMIT` / `FACT_VOTE_RATE_WINDOW_SECONDS` | 40 / 3600 |
| `SAVED_SEARCH_RATE_LIMIT` / `SAVED_SEARCH_RATE_WINDOW_SECONDS`, `GUARDIAN_INVITE_RATE_LIMIT` / `GUARDIAN_INVITE_RATE_WINDOW_SECONDS`, `CONNECTIONS_REQUEST_RATE_LIMIT` / `CONNECTIONS_REQUEST_RATE_WINDOW_SECONDS`, `GROUP_JOIN_RATE_LIMIT` / `GROUP_JOIN_RATE_WINDOW_SECONDS`, `AVATAR_UPLOAD_RATE_LIMIT` / `AVATAR_UPLOAD_RATE_WINDOW_SECONDS` | 20 / 3600 |
| `GUARDIAN_GUARDRAIL_RATE_LIMIT` / `GUARDIAN_GUARDRAIL_RATE_WINDOW_SECONDS` | 30 / 3600 |
| `THREAD_POST_RATE_LIMIT` / `THREAD_POST_RATE_WINDOW_SECONDS`, `THREAD_REACT_RATE_LIMIT` / `THREAD_REACT_RATE_WINDOW_SECONDS` | 30 / 60; 60 / 60 |
| `UNSAFE_REPORT_RATE_LIMIT` / `UNSAFE_REPORT_RATE_WINDOW_SECONDS` | 12 / 3600 |
| `GROUP_CREATE_RATE_LIMIT` / `GROUP_CREATE_RATE_WINDOW_SECONDS`, `GROUP_QUESTION_RATE_LIMIT` / `GROUP_QUESTION_RATE_WINDOW_SECONDS` | 5 / 3600; 6 / 3600 |
| `MESSAGING_START_RATE_LIMIT`, `MESSAGING_SEND_RATE_LIMIT` / shared `MESSAGING_RATE_WINDOW_SECONDS` | 20, 60 / 60 |
| `SAVED_SEARCH_NOTIFY_RATE_LIMIT` / `SAVED_SEARCH_NOTIFY_WINDOW_SECONDS` | 50 / 86400 |

Ordinary JSON/form bodies use the smaller of `MAX_REQUEST_BODY_BYTES` and
`DATA_UPLOAD_MAX_MEMORY_SIZE`. Multipart adapters enforce the latter against
aggregate non-file field bytes, alongside their smaller per-field caps; uploaded
file bytes stream into private scratch under separate media caps. This native cap
is distinct from Django's buffering/exception implementation. Media-route request
exceptions retain the existing bounded upload envelope. Browser/API defaults are
unchanged.

### Intentionally fixed or retired controls

| Setting names | Accepted source value / reason |
|---|---|
| `CHILD_PUBLIC_VENUES_ONLY`, `MEDIA_REQUIRE_SCANNER` | true; mandatory child venue/scanner gates |
| `PROGRESSION_AVATAR_PUBLIC` | false; private progression never becomes public |
| `IDENTITY_ALLOW_DEV_PROVIDER`, `EUDI_SANDBOX`, `EUDI_SANDBOX_ISSUER_KEY_PEM` | false / false / empty; synthetic identity proof cannot grant production assurance |
| `THREAD_REACTION_FACETS` | empty/unset; reviewed plural-sentiment vocabulary remains fixed |
| `CHAT_MESSAGE_POLICY` | `apps.chat.policy.NudgeMessagePolicy`; custom Python policy imports need a reviewed native adapter |
| `REDIS_URL` | empty/unset; native shared state uses PostgreSQL (ADR-0033); an unused Redis dependency is refused |
| `CHANNEL_LAYER_BACKEND` | `postgres` or the legacy source-default alias `channels.layers.InMemoryChannelLayer`; the Redis/custom Python classes are refused |
| `DB_POOL_ENABLED`, `DB_POOLED` | true / false; native session-aware pgx pooling, no Python pool or unqualified transaction-pool mode |
| `ASGI_THREADS`, `DJANGO_SETTINGS_MODULE` | unset only; retired Python worker/profile settings; native profile uses `--dev` / `DJANGO_DEBUG` and explicit security settings |
| `DB_POOL_TIMEOUT` | unset only; retired Python queue timeout cannot be mapped to pgx caller deadlines; explicit values are refused |
| `ROEDU_APP_PACK` | `roedu:social_media_activities_app:events_places:v1`; immutable reviewed data contract |
| `SENTRY_TRACES_SAMPLE_RATE` | finite zero only; profiling, traces, session/request telemetry remain off |

`DJANGO_REQUIRE_SHARED_STATE=true` asserts the PostgreSQL-backed native rate/state
contract and rejects a service graph with missing/mismatched admission pools; PostgreSQL is required in both modes. `REVERIFY_SWEEP_BATCH` and
`CONSENT_SWEEP_BATCH` must agree because the native runner has one sweep batch.
Media caps accept tightening within reviewed hard limits. Sentiment/privacy
floors cannot be lowered. API throttle strings support positive minute rates only.
Database pools remain bounded to two through four connections. External Python
provider/source/storage/scanner classes require their documented native aliases.

### Optional error reporting

A nonempty valid HTTPS `SENTRY_DSN` enables the isolated sentry-go v0.49.0 reporter;
unset/empty leaves it disabled even if an SDK global/environment fallback exists.
`SENTRY_ENVIRONMENT` accepts `production`, `staging`, or `development` (source
`production`). Legacy password-bearing DSNs, query/fragment data and malformed
values fail by name. No raw errors or panic text are supplied to the reporter.

Recovered panics, 5xx responses, startup failures and failed one-shot jobs emit
only fixed error categories, allowlisted HTTP methods and coarse route families.
The event is reconstructed before sending: no request, body, identity, cookies,
headers, tokens, IPs, query, stack trace, attachments, breadcrumbs or arbitrary
contexts survive. SDK tracing/profiling/log/session integrations are disabled.
The queues are bounded; capture drops when full and never waits for network I/O.
Shutdown drains/flushes for at most two seconds in the CLI; timeout/drop is best
effort and does not claim delivery. All qualification uses synthetic mock transports;
production Sentry delivery has not been performed.

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
