# Production readiness

Reviewed for the native Go runtime on 2026-10-04. Current implementation and exact
qualification receipts live in [STATUS](../STATUS.md); runtime selection is recorded in
[ADR-0032](adr/0032-complete-native-go-backend.md). Source landing and a production launch
have separate requirements. Social has not been deployed or launched by this migration.
The earlier Python-era checklist is preserved as [historical reference](archive/production-readiness-native-go-reference.md).

## Implemented runtime

The native executable owns accounts, domain APIs, guarded administration, private media,
encrypted live delivery, schema adoption, HTML/SPA context and explicit jobs. Default
Docker, Compose, systemd, Render and cloud-init paths invoke Go; Render and fresh cloud-init cannot
boot yet. PostgreSQL/PostGIS/vector
and native codecs are dependencies; Django is an offline compatibility oracle.
[NATIVE_SERVER](NATIVE_SERVER.md), [SCALING](SCALING.md) and the
[CLI reference](../services/server/cmd/social-server/README.md) describe supported modes
and remaining implementation limits. Do not infer support from an old Python setting.

## Before an authorized production rollout

Open audit blockers: [RELEASE_READINESS](RELEASE_READINESS.md) §Before first deployment.

- **Release qualification:** format/vet/race tests, every PostgreSQL/codec contract with
  zero skips, portable-auth hash checks, frontend/reference CI and native dependency/image
  scans must pass on the exact candidate. Independent review before landing; human
  auth/erasure/privacy/safety code review before first deployment
  ([ADR-0040](adr/0040-landing-and-deployment-review-gates.md)). Preserve evidence; an unallocated CI runner is not a passed test.
- **Data adoption and rollback:** back up the selected database, restore it into an isolated
  recovery environment, run native migration/adoption and test rollback. Verify PostGIS/vector,
  retained identities/passwords/provider IDs, retired legacy sessions, private-media references
  and erasure continuation. Never reset an existing database to make migration succeed.
- **Ingress:** test the canonical TLS origin, allowed hosts, immediate-proxy CIDRs, HTTPS
  redirect/HSTS, cookies/CSRF, liveness/readiness and graceful drain. Treat extra replicas as
  unqualified until global admission, revocation, live fan-out and queue claims are exercised.
- **Identity providers:** registered Google/Facebook callbacks and the actual identity trust
  chain require provider acceptance tests. Synthetic signature/state/nonce/PKCE fixtures do
  not prove production registration. Bootstrap and permission management do not verify age.
- **Private media:** verify approved EU storage residency, bucket privacy, access controls,
  effective lawful scanners, byte/codec bounds, quarantine, deletion and retention behavior.
  Missing scanners withhold uploads. No public user-media CDN or minor video activation.
- **Operations:** configure aggregate error/latency/readiness alerts, approved error reporting,
  uptime monitoring and an explicit SLO. Schedule jobs only after operator authorization;
  verify due-job heartbeats, stalled media, consent/retention jobs and bounded retries.
- **Recovery:** provision approved EU backups and key recovery, then rehearse restoration.
  Preserve the nightly backup, at-least-30-day retention and quarterly restore targets in
  [RUNBOOK](RUNBOOK.md). Storage versioning must respect erasure and evidence holds.
- **Infrastructure:** select and authorize the provider/box before procurement. Terraform and
  cloud-init examples remain unapplied. Deploy from a qualified immutable artifact; retain
  the preceding artifact and recovery procedure. Never infer a hosting price from fixture RSS.

## Product and legal launch gates

[SAFETY](SAFETY.md), [COMPLIANCE](COMPLIANCE.md) and [RELEASE_READINESS](RELEASE_READINESS.md)
retain the standing public-beta gates. DPIA/ROPA, counsel review, DPO responsibilities,
processor DPAs and breach/reporting procedures need owner/legal sign-off. An approved
scanner must have a lawful integration and escalation path. Security review and an
independent penetration test precede a public beta.

Minor onboarding stays structurally off until a real reviewed identity trust anchor,
verified parental authority and consent/privacy requirements are met. A mutual guardian
click or OAuth login does not establish parental authority or age assurance. No forecast
of a future wallet launch date is acceptance evidence.

Real ingestion additionally requires a freshly promoted immutable RO-EDU app-pack release,
explicit scoped credentials and review of lifecycle, provenance and child-venue gates.
Source code landing activates none of these services or product permissions.

## Growth after measurement

Use aggregate metrics, representative synthetic load, database plans/pool waits, queue age,
codec throughput and private-storage latency to select the next constraint to address.
Keep authorization and consent reads on authoritative state. A session-capable PostgreSQL
connection is required for LISTEN when introducing PgBouncer. Read replicas, partitioning,
extra caches, HA infrastructure and edge protection require measured need and explicit
freshness/privacy contracts. See [SCALING](SCALING.md) and [HOSTING_EU](HOSTING_EU.md).

Held-event review UX, curated child-venue policy and localized taxonomy/cinema mapping are
product backlog, not evidence of a missing Python serving dependency. Verify historical
checkboxes against current Go code before treating them as unimplemented features.
