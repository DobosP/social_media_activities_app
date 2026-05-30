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
- **Unique profile images** — `media.services.profile_image_is_taken` rejects a profile picture
  byte-identical (post-EXIF-strip `sha256`) to another user's **within the same cohort** (the
  single seam to refine "unique" later). Generic rejection message + rate-limited upload so it
  can't be used as an enumeration oracle. Best-effort, not perceptual / not impersonation-proof.
- **Consent & guardianship legibility (F13)** — two-sided read-only panels (`/wards/`, `/guardianship/`)
  stating exactly what a link grants, from `accounts.guardianship_capabilities`; guardian-side revoke
  reuses `accounts.revoke_guardian`. Ward side is legibility-only (no ward-initiated unlink).
- **Notification reasons & per-kind mute (F31)** — `NotificationPreference` + a mute gate in the single
  `notifications.notify()` choke point. **MODERATION (DSA Art.17) and SYSTEM (DSA Art.16) are never
  mutable** — checked first, before any lookup. Each notice carries a "why you got this" line.
- **Post-meetup "did we meet?" (F22)** — `Membership.met_confirmed_at`, settable only when the activity is
  COMPLETED; shows a member-only **count** ("Confirmed: N of M") — never a who-confirmed list and **never a
  per-person rating or cross-activity rollup**. Cleared on leave.
- **Age-proof provenance (F14)** — `accounts.assurance_provenance` renders a read-only profile panel: band +
  method + provider + verify/expiry dates + a re-verify nudge. Exposes **no DOB/identity/raw attestation**.
- **Your safety record (F19)** — `safety.safety_record_for` powers `/my-safety-record/`: a user's own DSA
  Art.16/17 record (moderation decisions about their account/activities/posts + reports they filed).
  Strictly self-scoped, field-allowlisted — never another user's data or the moderator's identity.
- **What-to-expect fields (F8)** — owner-curated `Activity.cost_band` / `difficulty` (choices) +
  `accessibility_notes`, routed through the F2 edit path; shown as cohort-visible chips (not member-gated).
- **Honest "why recommended" + beginners filter (F17)** — the home feed shows a true reason from the viewer's
  own declared interests ("matches your interest in X") or "soonest first" on cold-start, else the genuine
  "% match"; `Activity.beginners_welcome` adds a `?beginners=true` filter (the ranked strip stays unfiltered).
- **"Organize one here" prefill (F40)** — an event's "Organise" link seeds the create form's activity type +
  start time; `activity_create` validates every GET value (type exists/active, time parses) before seeding.
- **Catch-up thread digest (F35)** — `social.thread_digest` is a deterministic (no-ML) extractive recap
  (latest announcements + keyword-matched logistics + recent posts + going/total) shown member-only in a no-JS
  `<details>` "Catch up". Same digest for every member — **no per-user read-tracking**. Bounded queries.
- **First-timer welcome mat (F39)** — `_admit` marks a genuinely-new joiner's first membership (`welcomed_at`)
  and appends a line to their `JOIN_APPROVED` notice; a self-dismissing banner (7-day TTL) shows on the activity.
  **No thread Post is written** (avoids the required `Post.author` FK) — the welcome is unmistakably systemic.
- **Activity draft helper (F36)** — `social.draft_activity_text` composes a deterministic (template-only) draft
  title/description from the organizer's chosen type/place/time (+ a CHILD/TEEN safety reminder); `activity_create`
  seeds them via `setdefault` so it **never overwrites typed input**. Composes with F40's prefill.
- **Accessibility facts + access preference (F15)** — `places.accessibility_facts` derives honest states
  (true/limited/false/**unknown**) from a venue's existing OSM tags at **read time** (never written back — re-ingest
  would clobber). A per-user `AccessPreference` (a *stated* setting, not inferred) drives a **soft** "matches your
  access needs" badge that **never hides** unknown-accessibility places. `/access/` edits it.
- **WCAG chrome + JS-free places list (F16)** — a server-rendered `/places/list/` text fallback for the Leaflet
  map (mirrors the API filter/proximity, `.distinct()`), plus a skip link, ARIA landmarks, visible `:focus-visible`
  styles, and an `aria-live` chat region (muted during history load so screen readers don't replay the backlog).
- **Donation transparency (F29)** — `/transparency/` shows aggregate `completed_total_cents` raised next to
  staff-entered `SpendEntry` rows by category (two separate sections, **never** an "X of Y goal" bar; no donor
  PII); `/my-donations/` gives a donor their own plain receipts (self-only, no card data). `|cents` templatetag.
- **Earmarked campaigns (F34)** — staff `Campaign` + optional `Donation.campaign` FK (`SET_NULL`); `/campaigns/`
  shows a **calm static** progress bar (integer percent, capped 100; no countdown/scarcity/vanity). Inactive
  campaigns are blocked at all 3 layers (form/serializer/`start_donation`); general fund stays the default.
- **Verified civic partners (F37)** — `places.Partner` (text-only; **no image/logo field**), `/partners/` and a
  one-line place_detail credit. `Partner.objects.public()` (verified+active) is the single visibility chokepoint;
  website sanitised via `safe_external_url`; blurb capped at 280; neutral alphabetical order (no pay-for-placement).
- **User-proposed places, co-created (F25)** — `social.propose_place_with_venue` creates a `source=USER` `Place` +
  a `UserPlaceProposal` that needs **N independent confirmers** (`confirm_place`, proposer excluded) before going
  public. `places.public_places()` is the **single visibility chokepoint** — a *positive keep-filter* (`~USER OR
  proposal.PUBLISHED`, so a USER place with **no proposal row** is correctly hidden, not leaked by `NULL IN`). EVERY
  AllowAny Place surface (API `PlaceViewSet`, discovery `NearMe`/`Happening`, web list/detail) routes through it;
  `place_detail` 404s a pending place to everyone but its proposer/staff. Duplicate guard: 60 m hard / 25 m soft
  (`allow_nearby` override). Pending UI shows confirm **counts only**, never the proposer/confirmer identities.
- **Crowd confirm/dispute of activity edges (F26)** — `places/edges.py`: members `vote_on_edge` confirm/dispute a
  `PlaceActivity`. Tally lives in `ActivityEdgeVote` (one row per (edge,user); a mind-change updates it) — **ingest
  never touches that table**, so it survives re-ingest. A quorum (3) of disputes sets the **ingest-safe**
  `PlaceActivity.is_disputed` (absent from `ingest_places` `defaults`, so re-ingest can't clear it) and every read
  surface hides the edge; a quorum of confirms promotes an **INFERRED** edge to **CONFIRMED** (then in
  `PROTECTED_ORIGINS`, so ingest won't demote it). Only INFERRED edges auto-flip — a CONFIRMED edge is **not**
  crowd-hideable (no griefing); `moderator_reverse_edge` (demote/restore/reset) is the only reversal. Disputes are
  weighed **before** confirms (accuracy-first). `edge_vote_summary` exposes counts + the viewer's own vote only.
- **Open-now accuracy reports (F28)** — `places.open_now_status` returns open/closed from parsed hours, **downgraded
  to `"unverified"`** when ≥3 recent member reports (`OpenNowReport`) say the posted hours are wrong; `None` if hours
  are unknown. A **dedicated overlay** model (never on `Place`, which re-ingest clobbers) with **read-time decay**
  (reports outside `OPEN_NOW_REPORT_DECAY_SECONDS`=14 d stop counting — hours self-heal). `file_open_now_report` gates
  on `can_participate`, is **rate-limited** across venues and **idempotent** per reporter/place/window (anti-brigading);
  `clear_open_now_reports` is the staff reset. `PlaceViewSet` annotates `recent_report_n` so the serializer avoids N+1.
