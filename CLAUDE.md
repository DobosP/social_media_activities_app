# CLAUDE.md

Guidance for working in this repo. Read this first, then `README.md` and `docs/ROADMAP.md`.

## What this is

A **nonprofit, open-source, text-first** platform that helps people — **children first**, also
adults — meet **in person** to do real group activities (sport, endurance/outdoor, fitness,
board/video games, reading, participatory culture). It already **knows the places** (parks,
libraries, sports venues, seeded from open data) and **what's happening** (events), so a user's
job is just to *find people and go*. First launch city: **Cluj-Napoca, Romania (EU)**. The full
product engine (D1–D10) is built; see `docs/ROADMAP.md` and `docs/AUDIT_2026-05.md` for state.

## Stack

- **Django 5.2 LTS + DRF + PostGIS** (GeoDjango). **PostgreSQL is the single primary datastore**
  (relational + geospatial + graph + `pgvector`). No separate graph/vector DB.
- **ASGI/Channels** for real-time chat; **S3-compatible object storage** for blobs (photos).
- **Server-rendered web UI** in `apps/web/` (session auth, Leaflet maps) over the API-first backend.
- **Render** one-blueprint deploy (`render.yaml`); `daphne` in prod, `runserver` in dev.

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

## Architecture conventions

- **Domain logic lives in `apps/<app>/services.py`.** Both the DRF views (`apps/<app>/views.py`)
  and the web views (`apps/web/views.py`) call the *same* service functions, so the safety gates
  (cohort isolation, consent, blocking) hold identically on both surfaces. Don't put business
  logic in a view or template — add/extend a service.
- All state-changing services are `@transaction.atomic`. Audit via the hash-chained log:
  `from apps.safety.services import record_audit` (it takes a row lock, so call it *inside* the
  transaction).
- In-app notifications only: `apps.notifications.services.notify(recipient, kind, title, ...)`.
  Adding a `Notification.Kind` needs a (no-op) `makemigrations notifications` to keep CI green.
- Periodic jobs are management commands fanned out by `apps/ops/.../run_due_jobs.py` (`DUE_JOBS`).
- Cohort isolation: `social.services.visible_activities`/`can_see_activity` gate by the viewer's
  cohort; `blocked_user_ids(user)` excludes blocked pairs from feeds and notification fan-outs.

### Apps

`taxonomy` (activity graph) · `places` (PostGIS + geo API) · `ingestion` (OSM/Overture adapters)
· `accounts` (custom User, cohorts, EUDI age assurance, guardian links) · `social` (activities,
threads, join-by-vote, memberships) · `safety` (reporting, blocking, moderation, audit) · `chat`
(realtime per-thread) · `messaging` (E2EE direct/group) · `media` (profile + private photos) ·
`events` (iCal feeds) · `booking` · `discovery` + `recommendations` (feeds, pgvector) ·
`notifications` · `donations` · `ops` (`/healthz`, jobs, GDPR erasure) · `web` (server-rendered UI).

## Local run & tests (Docker)

The host already runs Postgres on 5432, so use the untracked local compose (db has no host port):

```bash
docker compose -f docker-compose.local.yml up -d          # NOTE: no --build (see below)
# pgvector once: exec -T db bash -lc "apt-get update && apt-get install -y postgresql-16-pgvector"
docker compose -f docker-compose.local.yml exec -T web pip install -r requirements-dev.txt
docker compose -f docker-compose.local.yml exec -T \
  -e DJANGO_SETTINGS_MODULE=config.settings.test -e DJANGO_SECRET_KEY=ci-secret-not-for-prod \
  -e DATABASE_URL=postgis://app:app@db:5432/app web pytest -q
```

The compose volume-mounts `./:/app`, so the running container always uses current code (no rebuild
needed). The production image installs `requirements.txt` only (no pytest) — install dev deps as above.

**CI gates** (all must pass): `ruff check .` · `ruff format --check .` ·
`python manage.py makemigrations --check --dry-run` · `pytest` · `docker build .` · `pip-audit`.

## "Show-up & safety" feature set

Built on the social core; see services/tests for exact behaviour. All uphold the invariants above.

- **Activity lifecycle** — `cancel_activity` / `complete_activity` (`social/services.py`) +
  `auto_complete_activities` command; cancel notifies members and blocks joins.
- **Edit before start** — `update_activity` (whitelisted `ACTIVITY_EDITABLE_FIELDS`; place/type/
  cohort locked); a time change re-notifies and **supersedes the stale reminder** (`_supersede_reminders`).
- **Organiser announcements** — `post_announcement` (`Post.is_announcement`), pinned + notifies all.
- **Logistics card** — owner-curated `meeting_point` / `what_to_bring` / `organizer_note` on
  `Activity`, edited via the same `update_activity` path, shown to members only (stricter than
  `description`, which is cohort-visible).
- **RSVP intent** — transient `Membership.attendance_intent`; per-activity go/no-go count only,
  reset on leave, **never** aggregated into per-user history (`set_attendance_intent`/`attendance_summary`).
- **Arrival ping** — self-declared `mark_arrived` (`Membership.arrived_at`): no location, no free
  text, idempotent, notifies other members and (for a CHILD) the active guardian(s); cleared by
  `expire_arrivals` so it never becomes a presence record.
- **Parent meetup manifest** — read-only `/wards/` view of each ward's upcoming place/time/type.
- **Safe-exit card** + **use-my-location** (request-only proximity) in `apps/web/`.
