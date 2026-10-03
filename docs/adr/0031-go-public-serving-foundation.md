# ADR-0031: Start Go conversion at the public serving boundary

Date: 2026-10-04
Status: accepted direction; implementation awaiting human review

## Decision

Use Go for the social application's server migration, starting with its existing
stdlib-only, database-free `services/agentapi` boundary. Harden and extend this
service while Django continues to own the live domain and reviewed public export.
Keep the product's existing HTTP/data-contract boundary. Replace further domains
only after differential tests establish their full behavior and safety gates.
This is the owner's Go direction, implemented as a first reviewable slice.

## First slice

- Public event queries gain place filtering, description/venue search, split
  coordinate aliases and deterministic nearest ordering. The `/agent/v1` envelope
  remains unchanged; this is a snapshot API with UTC date-only filters. It has no
  `/api/v1` compatibility alias because live historic/GeoJSON contracts differ.
- Publisher and reader use schema 2 with exact-file SHA256 checksums. Schemas,
  counts, generation timestamps and the unchanged manifest must agree before an
  atomic swap. The reader enforces reviewed fields, scalar/nested shapes, unique
  canonical JSON keys, positive unique IDs and adult-only activity records.
- Snapshot files are bounded, regular, flat and rooted. Unchanged manifests skip
  dataset parsing. Invalid reloads retain the last validated public snapshot.
  Health exposes generation age and load age separately, with five-minute skew
  tolerance. A maximum age/revocation policy remains a rollout gate.
- Configuration diagnostics and logs omit values, query strings, identifiers and
  peer IPs. Request budgets, local limiter cardinality, timeouts, security headers,
  conditional caching and gzip/HEAD behavior have regression coverage.
- Go 1.27.1 matches the Romanian app's toolchain. A digest-pinned builder emits a
  static scratch image, UID 65534, with a native loopback-only healthcheck. Optional
  Compose ingress is loopback-only; filesystem/snapshot mounts are read-only,
  capabilities dropped, privileges disabled and CPU/memory/PIDs bounded.
- CI runs format/vet/race, the real Django-exporter → Go contract, native OCI
  qualification, Trivy and Go package/binary vulnerability scans. Current tagged
  govulncheck's source SSA cannot analyse Go 1.27 syntax, so no source call-graph
  result is claimed. New actions are pinned and dependency updates are reviewed.

## Uploaded images and video

Preserve the current media contract during conversion: original-byte screening,
withheld state, authorization at every serve, expiring access, deletion/retention
and private object storage. Image processing already bounds input to 5 MiB/30 MP,
strips metadata/orientation, creates ≤2048-pixel AVIF/WebP derivatives and an
800-pixel thumbnail with bounded encoder threads. Video is a separate adult-only
private-thread pipeline with deferred, sandboxed FFmpeg processing and frame
screening. These are native codecs already; Go can orchestrate a bounded worker
without reimplementing compression or transferring approval decisions to it.

Use opaque object references and typed processing results across the worker
boundary. Go serving returns already-approved derivatives using the existing
per-viewer authorization and short private presigns. A result must bind source
and derivative digests, processing-policy version, dimensions/type, and an
explicit scanner verdict. Never accept a worker success as publication consent.
No uploads or codec jobs are enabled by this first slice.

Organization storage policy is authoritative: personal uploads belong in private
EU-native storage; R2 is limited to public nonpersonal corpus data and MinIO is
excluded. Older generic provider examples in this app need a separate reviewed
correction before production storage configuration.

## Cross-project maintenance

The organization remains a polyrepo. `company-ops` owns governance and release
criteria; it holds no product code. The existing `dobolabs-ci` and repository
template are the destination for shared Go quality/security/container workflows.
Their private live workflow sources were unavailable to this session, so this
branch adds local checks rather than claiming verified shared-workflow adoption.
No repository is created, transferred, made public or provisioned.

Version narrow Go HTTP/config/limits helpers only when a second consumer shares
an actual contract. Keep product authorization, cohort, moderation and database
transactions local to the owning app; a language migration must preserve atomic
mutation + hash-chained audit semantics. Contract fixtures, race checks,
vulnerability audits and clear ownership provide the maintenance boundary for
future AI changes. Native Rust components remain possible behind a measured,
versioned worker contract; this Go slice introduces none.

## Consequences and next gates

The public serving path can be qualified independently and stays free of Python
runtime and DB credentials. The overall social application remains transitional
Go + Django. No end-to-end cost or capacity claim follows from this small slice.

Before landing, obtain the human privacy/safety review required by `AGENTS.md`.
Before routing live traffic, approve export cadence, stale-data/revocation and
withdrawal/erasure behavior, edge/shared abuse limits, realistic full-capacity
load measurements and reverse-proxy wiring. Port live read contracts next, then
transactional domains with auth/safety/audit parity, followed by chat and jobs.
Deployment and child-facing activation retain their existing explicit gates.

A separate media safety finding needs its own reviewed fix: `ManagedScanner`
currently treats `{}` and nonboolean false-like responses as a clean verdict.
Require a documented explicit boolean verdict before relying on that provider.
This branch does not change the scanner, auth, storage or codec behavior.
