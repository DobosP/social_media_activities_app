> **COMPLETED (2026-07-02):** every track on this board is merged (Phase 1 D1–D10 + the Phase-2
> tracks). The multi-agent workboard/branch-claim workflow itself is superseded by the fleet
> `AGENTS.md` (2026-06-24; single-session cadence, no push/merge without Paul's ask) — see
> `../../STATUS.md` for current state. Historical registry.

# Workboard — who is building what

Live registry of active work so parallel **sessions/agents don't collide**. It is the
cross-branch **synchronization point**: a branch can't see another branch's uncommitted work, but
everyone can read this file on `main`.

**Rules**
1. **Read this on `main` before you start.**
2. **Claim a track here in your first commit** (set the branch + status + your owned paths) so
   other sessions see it.
3. **One track per session.** Stay strictly inside your "Owns" paths.
4. Keep your row's **Status** current: `claimed → in-progress → in-review → merged`.
5. The branch name *is* the session identifier — use `claude/<track>-<slug>`
   (e.g. `claude/d3-social-core`). See [MULTI_AGENT_BUILD](MULTI_AGENT_BUILD.md).

## Done (already on `main`)

| Track | Status | Landed |
|------|--------|--------|
| D1 — foundation & RO place data | ✅ merged | knowledge graph, OSM ingestion, geo API |
| IS-2 — CI / Dependabot / pre-commit | ✅ merged | gates every PR |
| D2 — identity scaffold | ✅ merged | custom user, cohorts, consent gate, provider interface (EUDI **stub**) |
| D2-eudi — finish identity | ✅ merged | EUDIWalletProvider: real OpenID4VP/ES256 verification (trust list + nonce/audience), age-band only, no PII; `/api/accounts/verify-age/` |
| D3 — social core | ✅ merged | activities, threads/posts, join-by-vote, place quorum, cohort isolation |
| D4 — safety & moderation | ✅ merged | reporting, blocking, moderation queue, hash-chained audit log |
| D7 — richer place data | ✅ merged | Overture adapter, Google enrichment, cross-source dedup, opening-hours/open-now |
| D5 — chat | ✅ merged | real-time per-thread rooms (ASGI/Channels), cohort/membership-scoped, moderation hooks |
| D8 — booking | ✅ merged | BookingProvider adapters, deep-links, bookings tied to activities |
| D6 — media | ✅ merged | profile + private thread photos, EXIF/GPS stripping, safety scan, signed URLs |
| D9 — nonprofit/ops/launch | ✅ merged | donations (no ads/tracking), `/healthz` + aggregate stats, ASGI prod, runbook + release gate |
| Data sources & collection | ✅ merged | parks/libraries/archives/reservation venues; website+GPS; provider registry ([DATA_PROVIDERS](DATA_PROVIDERS.md)) |
| Events | ✅ merged | iCal feeds + event→activity classification; `/api/events/` |
| Activity taxonomy v2 | ✅ merged | endurance/outdoor, fitness, culture; `wellness` + `family_friendly` traits |
| Guardian-accompanied activities | ✅ merged | child + verified-adult guardian (supervisory, group-only) |
| Render deploy | ✅ merged | one-blueprint hosting (web + PostGIS) + WhiteNoise static |
| Roles & guardianship | ✅ merged | admin/moderator/user roles; legal-guardian links; guardian manages ward; schools as places |
| P1 — discovery & feed API | ✅ merged | near-me / happening / activities feeds over places·events·activities |
| P7 — security review | ✅ merged | STRIDE threat model, findings, pen-test checklist |
| D10 — secure messaging | ✅ merged | E2EE direct/group chat, cohort-isolated, guardian read-only *(was missing from this board)* |
| web UI · notifications · recommendations | ✅ merged | server-rendered UI, opt-in notifications, pgvector recommendations *(P3/P6 shipped despite "unclaimed" rows below)* |
| 2026-05 audit — Wave 0 | 🔧 `claude/wave0-safety-hardening` | verified audit + child-safety/security fixes; see [AUDIT_2026-05](AUDIT_2026-05.md) |

## 🎉 Phase 1 complete — all roadmap deliverables (D1–D9) + enhancements are on `main`

CI is green (ruff, format, migrations, ~209 tests, pip-audit, Docker build). What's
left is **go-live + the experience layer**, not new core systems. The next phase is
planned in **[PHASE_2_PLAN](PHASE_2_PLAN.md)** — claim a track there.

## Active / available tracks (Phase 2 — see PHASE_2_PLAN.md)

> Claim a row (branch + status) in your first commit. "Depends on" lists tracks that
> must be **merged to `main`** first. Most are independent → high parallelism.

| Track | Branch | Status | Session | Owns (paths) | Depends on (merged) |
|------|--------|--------|---------|--------------|---------------------|
| **P1** discovery & feed API | `claude/p1-discovery` | in-review | `claude/p1-discovery` | `apps/discovery/` (new); read-only views over places/events/activities | D3, D7, events |
| **P2** live data adapters | `claude/p2-live-data` | _unclaimed_ | — | `apps/ingestion/sources/` (Foursquare, Ticketmaster, Wikidata, Geofabrik) | D7, events |
| **P3** recommendations | `claude/p3-recommend` | _unclaimed_ | — | `apps/discovery/` ranking; interest similarity (pgvector) | P1 |
| **P4** notifications | `claude/p4-notifications` | in-review | integrator | `apps/notifications/` (new); opt-in, no tracking | D3, D5 |
| **P5** compliance & legal | `claude/p5-compliance` | _unclaimed_ | — | `docs/` (DPIA, ToS, Privacy, DSA), consent UX | D2, D4 |
| **P6** i18n (RO/EN) | `claude/p6-i18n` | _unclaimed_ | — | `locale/`, DRF/Django i18n wiring | — |
| **P7** security review | `claude/p7-security` | in-review | `claude/p7-security` | threat model, pen-test fixes, rate-limit coverage | D4, D9 |

## Shared edit points (coordinate / keep minimal)

These few files are touched by many tracks — make **small, append-only** edits, or leave them to
the integrator:

- `config/settings/base.py` — `INSTALLED_APPS` (each new app adds one line)
- `config/urls.py` — each new app adds one `include(...)`
- `docs/ROADMAP.md`, this file — status updates (different rows/sections rarely conflict)
- `requirements*.in` — dependency changes go through the bump workflow in [SECURITY](SECURITY.md)
