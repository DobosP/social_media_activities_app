# ADR-0001: Launch hosting = Hetzner single box + Hetzner Object Storage; render.yaml is demo-only

Date: 2026-06-19
Status: accepted

## Decision
Deploy the real launch on one Hetzner Cloud EU box (co-located Postgres 16 + PostGIS + pgvector,
Redis, daphne ASGI, Caddy TLS) with Hetzner Object Storage for media blobs and backups, provisioned
via the `deploy/` Terraform + cloud-init IaC (~€13/mo, all EU-owned). Treat `render.yaml` strictly
as a free-tier demo / Render-Frankfurt fallback — never the launch path.

## Context / why
Hard constraints: minors' data (relational + blobs + backups) must never leave the EU; cheap,
donations-funded; no per-user AI spend. Decided 2026-06-19 (`380c9f8` docs/HOSTING_EU.md,
`9bbe127` deploy/ IaC).
- **Why not Render**: 4–6x pricier at real-launch tier ($50–80/mo); US-owned processor → DPA + SCCs
  transfer burden for minors' data; free tier sleeps + ephemeral DB (demo only).
- **Why not Cloudflare R2 for media**: org hard rule — R2 serves ONLY the public non-personal Gold
  corpus; minors'/personal media go EU-owned storage, never R2
  (`unified-deployment-architecture/docs/infrastructure-decision.md`).
- **Why not MinIO**: banned org-wide (upstream repo archived 2026-02).
This ADR supersedes the May-era "Render one-blueprint deploy" / "(R2/MinIO)" parentheticals that
lingered in CLAUDE.md, RUNBOOK.md, RELEASE_READINESS.md and ROADMAP.md (all rewritten 2026-07-02).

## Consequences
- `deploy/` + `docs/HOSTING_EU.md` are the deploy source of truth; single-box SPOF is accepted at
  launch; the scale-out path is HOSTING_EU §6 / PRODUCTION_READINESS.
- The IaC has NEVER been applied — no infra exists; the org-level provider decision is
  RECOMMENDED-NOT-CONFIRMED. Never `terraform apply` (paid) without Paul.
- To revisit: box size (CPX22 here vs CX23 in the org plan) and the Render-vs-Hetzner framing in
  older docs — Paul to reconcile before procurement.
