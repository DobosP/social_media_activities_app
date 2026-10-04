# ADR-0032 — Complete native Go backend

Date: 2026-10-04
Status: accepted owner direction; implementation awaiting required human review
Supersedes: ADR-0031's public-only implementation scope; preserves its public contract.

## Decision

The owner selected Go and requested the complete serving implementation for both
Cât de român ești? and the Social Activities app. The Social app's native executable
owns HTTP, authentication, domain services, safety/guardian policy, media, live
transport, HTML/SPA hydration, operations and deferred jobs. Its release image and
launchers execute no Django/Python server, proxy or worker. Python remains an offline
compatibility oracle; JavaScript/TypeScript remains the client.

The shared identity core is canonical in Cat's `shared-go/authcore`. Social carries
an identical MIT-licensed portable copy under `services/authcore`, verified by
`SOURCE.json` hashes and a native CI checker. Updates require reviewing one canonical
change and refreshing the consumer snapshot; there is no production checkout dependency.

PostgreSQL preserves existing relational contracts, roles, privacy/consent gates,
license/provenance metadata and deferred queues. Bootstrap adopts existing data without
dropping/resetting tables; legacy sessions retire on migration and users sign in again.
OAuth subject identity never links accounts by email. Login is not age verification.

## Policy and operational boundaries

Native administration exposes audited curated edits and domain-specific transitions.
Raw account age/consent/cohort, membership, ciphertext, scanner and payment-ledger edits
and unrestricted generic deletion are unavailable; their governed services own changes.
The native operator bootstrap creates a fresh unverified/unassigned administrator only,
never promotes an existing identity or grants parental/age assurance.

The initial native release retains bounded process-local API/social/catalog rate histories.
Database-backed authentication budgets/state and ID-only PostgreSQL live notifications are
shared. Redis-required mode and Sentry integration, arbitrary Python plugins and listed
unsupported nondefault policy settings fail startup by name; they are not silently ignored.
This restriction must be resolved before deployment that requires global domain budgets.

Private media stays in owner-verified private EU-native storage, with fail-closed scanning,
strip/re-encode, bounded codecs, signed serving and durable cleanup. Uploads may trigger
one bounded native video drain; durable queues/timers provide retries. No startup job
scheduler or real ingestion is automatically activated by the migration.

## Landing and release gates

Auth/privacy/safety requires human review under AGENTS.md before landing. Product launch
still requires the GDPR/DPIA/parental-authority and operations gates. Providers, paid
infrastructure, live ingestion and minors are not activated by code publication.

Native gates include portable module builds, race/vet, explicit disposable PostgreSQL with
real foreign keys, actual AVIF/WebP/FFmpeg codecs, independent Django payload/markup/cursor
goldens, fresh/adopted/preextended bootstrap, live session revocation, erasure continuation,
image/runtime scans and a Python-free nonroot read-only release image. Exact completed
results belong in STATUS.md and WORKLOG.md.

The canonical local/CI PostgreSQL16 image uses supported Debian Bookworm packages for
PostGIS/vector. The previous postgis16-3.5 Bullseye base had unavailable archived security
packages. The PostgreSQL major/volume contract stays16; migration and actual-FK fixtures
qualify the revised build. This does not upgrade any real deployment or existing volume.
