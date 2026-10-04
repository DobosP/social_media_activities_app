# EU hosting and native release operations

Verified against native Go on 2026-10-04. Runtime selection is recorded in
[ADR-0032](adr/0032-complete-native-go-backend.md); hosting history is in
[ADR-0001](adr/0001-launch-hosting-hetzner-single-box.md). Provider procurement remains
undecided and deployment templates remain unapplied. Approved code landing does not
complete product GDPR/DPIA/parental-assurance or operations gates; see [STATUS](../STATUS.md).

This guide describes the release contract and checks for a subsequently authorized
rollout. It does not authorize purchasing infrastructure, Terraform apply, deployment,
real ingestion, provider activation or minor onboarding.

## Release artifact

Build and qualify the exact source candidate in the native CI environment. Current Go
modules require Go 1.27.1; the frontend build uses Node 24. Export the Docker `release`
target into task scratch, package the resulting executable/static/templates/locale/
reviewed reference data, and record the archive's exact SHA-256. Preserve its provenance
and license metadata. Follow [NATIVE_SERVER](NATIVE_SERVER.md) and
[deploy/README](../deploy/README.md) for the artifact layout and templates.

Cloud-init accepts the release URL and expected digest, verifies before extraction, and
installs the compiled executable and prepared assets. The serving host does not clone
application source, install Python dependencies, or compile Go/Node. The native runtime
executes no Django, Daphne, Gunicorn, Uvicorn or Python worker. The reference image is an
offline test oracle and is not a fallback production server.

Retain the previous artifact and verified database recovery path. A real rollout requires
an artifact/database rollback drill; no existing volume is upgraded by documentation or
by building a new image.

## Host and database contract

The current templates place PostgreSQL 16, PostGIS and pgvector behind private/loopback
interfaces and Caddy in front of native HTTP on loopback. Native bootstrap also retains
preinstalled spatial/extension schemas and their data. Run `social-server --migrate-only`
against the explicitly selected database before serving; this adopts existing domain
rows and retires legacy sessions rather than resetting data. Migration-only does not
start media, the live listener or jobs.

The host needs native `ffmpeg` and `prlimit`; video requires `ffprobe`, and AVIF output
requires `avifenc` from `libavif-bin`. Missing tools fail serving/job startup. Provide a
service-owned private scratch directory (0700), CPU/memory/PID limits, capability drops
and no privilege escalation. Follow the qualified release image rather than adding
unreviewed codecs or Python substitutes on the host.

The process uses a bounded `pgxpool` (zero-to-four connections by default) and 5-second
SQL statement timeout. HTTP live delivery reserves one connection for PostgreSQL LISTEN;
NOTIFY contains IDs and fresh domain checks decide delivery. Do not expose database,
application, scanner, metrics or optional sidecar ports publicly. Only the reviewed TLS
front should accept public traffic.

## Canonical origin and proxy

Set the exact `SITE_BASE_URL`, allowed-host configuration and explicit trusted
immediate-proxy CIDRs from the approved configuration. The templates trust loopback
Caddy only. `NUM_PROXIES` bounds which rightmost forwarded hop may identify a caller;
forwarded HTTPS/identity headers from an untrusted peer are ignored. Allowed-host checks
precede redirects. Production settings map native HTTPS redirects, HSTS and safe aggregate
logs. Check a real local proxy-to-app transport during rollout rather than inferring
correctness from a supplied header.

Direct loopback `GET /healthz` can bypass HTTPS redirect for a health probe, while still
requiring an allowed canonical Host. `GET /readyz` retains normal HTTPS/proxy policy and
checks database/configured dependencies and draining. Distinguish process liveness from
readiness; a live process can be degraded.

## Private storage, identity and secrets

Private user media requires an approved private EU-native object store. Native S3 startup
requires both `MEDIA_EU_RESIDENCY_VERIFIED` and `MEDIA_PRIVATE_BUCKET_VERIFIED`; endpoint
and region names alone are not verification. R2 and MinIO endpoints are rejected. No
provider/bucket is created by the service; residency, access policy, key scope, lifecycle
and processor review are operator prerequisites. Local storage is loopback development
only. See [FILE_STORAGE](FILE_STORAGE.md) for scanner, encryption and serving constraints.

Values come from the approved fleet secrets workflow; document setting names, never real
values. The executable reads explicit environment settings and does not discover `.env`
or auth/service credential files. A protected systemd `EnvironmentFile` supplies settings
in the templates. Native EUDI requires reviewed issuer public keys and identity binding;
login and administrator bootstrap do not grant age/parental assurance. Minor onboarding
is off until its separate real-assurance configuration and product gates are satisfied.

## Explicit jobs and optional public snapshot

The native systemd units run schema migration, HTTP, `social-server --due` and bounded
`social-server --job transcode_videos` operations. `--due` executes the 27 concrete due
jobs once and exits. Timer installation/activation requires its own owner authorization;
HTTP startup runs no recurring scheduler or network ingestion. Upload-triggered video
processing is a bounded post-commit pass with durable timer retry, not a global scheduler.

Source-sync, IndexNow, external heartbeat, donation and other provider work remain
explicitly configured/gated. Running due work does not grant provider authority. Review
its selected source configuration before activation; never turn on real ingestion to
prove a release works.

The optional `services/agentapi` Go sidecar serves an immutable exported public snapshot.
It is not required for native application serving and must not receive private data or
import producer internals. Snapshot export must preserve public visibility and source/
license credits. Rust research and offline Python tools do not become launch dependencies.

## Recovery, scale and remaining gates

Preserve the established EU backup/retention and quarterly restore-drill targets in
[RUNBOOK](RUNBOOK.md). Qualify restoration on a separate recovery fixture, including
extensions, auth/CSRF, media references and deletion continuation. Media versioning and
lifecycle must respect erasure and safety/evidence holds.

API/social/catalog/saved-search/CSP budgets now share PostgreSQL admission/capacity state;
required-shared mode checks the native contract. No Redis service is required. Reviewed
policy overrides and bounded privacy-safe Sentry have native boundaries. Run the additive
migration and qualify actual replicas, pool behavior, revocation and ingress before scaling.
See [SCALING](SCALING.md); there is no verified user-capacity, monthly-cost or procurement promise here.

The implementation is approved for landing. Production still needs owner procurement,
exact release/image qualification, rollback/restore evidence, scanner/storage/identity
trust review, proxy/health verification and the product launch gates. No real VPS has
been qualified by these templates. Older provider comparisons, price examples, Python
installation tutorials and optional-service proposals are preserved in the complete
[historical hosting reference](archive/hosting-eu-native-go-reference.md).
