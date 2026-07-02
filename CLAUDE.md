# CLAUDE.md

Guidance for working in this repo. Read this first, then `STATUS.md` (current truth) and `README.md`.

Docs discipline: see AGENTS.md — STATUS.md + ADR update is part of definition of done.

## What this is

A **nonprofit, open-source, text-first** platform that helps people — **children first**, also
adults — meet **in person** to do real group activities (sport, endurance/outdoor, fitness,
board/video games, reading, participatory culture). It already **knows the places** (parks,
libraries, sports venues, seeded from open data) and **what's happening** (events), so a user's
job is just to *find people and go*. First launch city: **Cluj-Napoca, Romania (EU)**. The full
product engine (D1–D10) is built; see `STATUS.md` and `docs/PRODUCTION_READINESS.md` for state.

## Stack

- **Django 5.2 LTS + DRF + PostGIS** (GeoDjango); **PostgreSQL is the single primary datastore**
  (relational + geospatial + graph + `pgvector`); ASGI/Channels realtime; S3-compatible blobs.
- **Server-rendered web UI** in `apps/web/` (session auth, Leaflet) over the API-first backend.
- **Launch deploy: single Hetzner EU box** (`deploy/`, `docs/HOSTING_EU.md`, ADR-0001);
  `render.yaml` = free-tier demo only. `daphne` in prod, `runserver` in dev.

## Hard invariants — every change must uphold ALL of these

These are the product, not preferences. A change that breaks one is wrong even if it passes tests.

1. **Text-first.** No public photo feeds, no short-video, no image-perfect surfaces. Photos exist
   only privately inside an activity thread; one profile picture max.
2. **No ads, no behavioural tracking, no engagement-maxxing.** No dark patterns, no per-user
   reliability/attendance history, no vanity metrics. Funded by donations only.
3. **Child safety is the core promise.** Age-**cohort isolation**; **no adult↔minor private
   contact**; verified + parental-consented participation for under-16; conservative defaults.
   Any guardian fan-out must key on an **ACTIVE `GuardianRelationship`**, never a loose flag.
4. **Privacy by default + EU compliance** (GDPR, DSA, eIDAS/EUDI). Minimise PII (store age
   **band**, not DOB). **Never store user location** (proximity uses request-only coordinates).
5. **Real, in-person, healthy group activities** at real places — not an online-only app.
6. **Cheap, scalable, open-source.** Postgres-primary; lean EU hosting; avoid heavy/ML deps and
   per-user cloud-AI spend.

`docs/SAFETY.md` is the authoritative list of safety invariants.

## Architecture essentials

Full conventions + app map: `docs/ARCHITECTURE.md`. The five rules that gate every change:

- Domain logic lives in `apps/<app>/services.py`; DRF views and web views call the *same*
  services, so safety gates (cohort, consent, blocking) hold on both surfaces — never in views.
- State-changing services are `@transaction.atomic`; audit via `safety.services.record_audit`
  called *inside* the transaction (it takes a row lock).
- All notifications go through the single `notifications.services.notify()` chokepoint.
- Periodic jobs = management commands fanned out by `apps/ops` `run_due_jobs` (`DUE_JOBS`).
- Cohort gates: `social.services.visible_activities` / `can_see_activity` + `blocked_user_ids`.

## Built features — do not rebuild

The behavioral-contract catalog of everything shipped (D1–D10 + 2026-06 waves, with invariant
gates) is `docs/FEATURES_BUILT.md`; ops-level list: `docs/PRODUCTION_READINESS.md` §0. Check both first.

## Run & test

Quickstart + the local Docker recipe (host Postgres on 5432 → untracked `docker-compose.local.yml`,
**no `--build` needed**): `README.md` "Quick start". Targeted test commands: `docs/agent-testing.md`.

**CI gates** (all must pass): `ruff check .` · `ruff format --check .` ·
`python manage.py makemigrations --check --dry-run` · `pytest` · `docker build .` · `pip-audit`.

## Git policy

Branch/worktree per slice; commit locally on green; **do not push or merge unless Paul explicitly
asks**. `git diff --check` before finishing. Full agent rules: `AGENTS.md`.

## Docs map

- Current truth: `STATUS.md` · gap list / P0s: `docs/PRODUCTION_READINESS.md`
- Built features + gates: `docs/FEATURES_BUILT.md` · safety authority: `docs/SAFETY.md`
- Architecture + conventions: `docs/ARCHITECTURE.md` · async queue: `docs/ASYNC_TASKS.md`
- Deploy/ops: `docs/HOSTING_EU.md` + `deploy/` + `docs/RUNBOOK.md`
- Decisions: `docs/adr/` (conflict order: STATUS.md > newest ADR > other docs) · legal drafts
  (pending DPO): `docs/legal/` · full doc index: `docs/README.md`
