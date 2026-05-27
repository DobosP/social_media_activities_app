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
| D2-eudi — finish identity | ✅ merged | EUDIWalletProvider (age-band only, no PII) over the verifier seam |
| D3 — social core | ✅ merged | activities, threads/posts, join-by-vote, place quorum, cohort isolation |
| D4 — safety & moderation | ✅ merged | reporting, blocking, moderation queue, hash-chained audit log |
| D7 — richer place data | ✅ merged | Overture adapter, Google enrichment, cross-source dedup, opening-hours/open-now |
| D5 — chat | ✅ merged | real-time per-thread rooms (ASGI/Channels), cohort/membership-scoped, moderation hooks |
| D8 — booking | ✅ merged | BookingProvider adapters, deep-links, bookings tied to activities |

## Active / available tracks

> Claim one by editing your row (branch + status). Unclaimed rows are up for grabs, in dependency
> order. "Depends on" lists tracks that must be **merged to `main`** first.

| Track | Branch | Status | Session | Owns (paths) | Depends on (merged) |
|------|--------|--------|---------|--------------|---------------------|
| **D6** media | `claude/d6-media-integrated` | in-review | integrator | `apps/media/` | D3, D4 |
| **D9** nonprofit/ops/launch | `claude/d9-ops` | blocked | — | deploy, donations, observability | D5, D6, D8 |

## Shared edit points (coordinate / keep minimal)

These few files are touched by many tracks — make **small, append-only** edits, or leave them to
the integrator:

- `config/settings/base.py` — `INSTALLED_APPS` (each new app adds one line)
- `config/urls.py` — each new app adds one `include(...)`
- `docs/ROADMAP.md`, this file — status updates (different rows/sections rarely conflict)
- `requirements*.in` — dependency changes go through the bump workflow in [SECURITY](SECURITY.md)
