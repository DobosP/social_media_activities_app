# Building with multiple agents in parallel

This project is meant to be built by **several Claude Code agents/sessions working concurrently**,
not one at a time. This doc is the coordination contract: read it **before** starting work so
agents don't step on each other. It complements [ROADMAP](ROADMAP.md) (what to build, in what
order) and [ARCHITECTURE](ARCHITECTURE.md) (the seams).

The live **who-is-building-what registry is [WORKBOARD](WORKBOARD.md)** — read it on `main` and
**claim your track there before coding**. Each session is identified by its branch name.

> TL;DR: one agent per **app/module** on its **own branch (or git worktree)**; code against
> **shared interfaces defined up front**; serialize only where the [ROADMAP](ROADMAP.md)
> dependency graph has an edge; **CI must pass** before any merge; a **lead/integrator** merges in
> dependency order.

## Why parallel works here

The design is deliberately a **modular monolith**: focused Django apps (`taxonomy`, `places`,
`ingestion`, and future `accounts`, `social`, `chat`, `media`, `booking`) with narrow, documented
interfaces and adapter seams. Disjoint apps = disjoint files = low conflict surface. The hard part
isn't parallelism itself — it's the few **shared edit points** and **ordering constraints** below.

## The four conflict classes (and how we avoid each)

1. **Same-file edits.** Two agents editing the same module.
   → **File/app ownership.** Each work item owns a disjoint set of paths (table below). Agents do
   not edit files outside their ownership without coordinating.

2. **Migration collisions.** Two agents both add `0002_*` in the same app, or create conflicting
   migration graphs.
   → One **migration owner per app per work item**; agents in *different* apps rarely collide.
   If two heads appear, resolve with `python manage.py makemigrations --merge` and rebase before
   merge. Never hand-edit applied migrations.

3. **Shared central config.** `config/settings/*` (`INSTALLED_APPS`), `config/urls.py`,
   `requirements*.in` are edited by many.
   → Keep edits **small and append-only**; or let the **integrator** own `config/` and wire new
   apps in after they land. New apps expose their own `urls.py`; the integrator adds one
   `include(...)` line. Dependency changes go through the bump workflow in [SECURITY](SECURITY.md).

4. **Shared contracts/schema.** An app depends on another's models/interface that's still
   changing.
   → **Contract-first.** Define the interface before parallel work starts (e.g. the
   `IdentityProvider` protocol and the `User` model in D2; the `SourceAdapter`/`BookingProvider`
   interfaces). Downstream agents code against the agreed contract, with a stub if needed.

## Sequencing rules (from the dependency graph)

- **Do the cross-cutting integrated steps first**, single-agent, and merge before fanning out —
  especially **IS-1 (custom `User` model)** and **IS-2 (CI gates)**. Branching social/chat work
  before the `User` model exists guarantees rework.
- **Parallelize only deliverables with no edge between them** in the [ROADMAP](ROADMAP.md) graph.
  Within a deliverable, split by app/file ownership.
- A deliverable with upstream deps waits for them to **merge** (not just start).

### What can run concurrently (current snapshot)

Already merged to `main`: **D1**, **IS-2**, **D2 (scaffold)**. Live registry: [WORKBOARD](WORKBOARD.md).

| Track | Owns (paths) | Depends on (merged) | Status |
|---|---|---|---|
| **D7** richer place data | `apps/ingestion/sources/`, `apps/places/` enrich | D1 | available now |
| **D2-eudi** finish identity | `apps/accounts/identity/` | D2 | available now |
| **D3** social core | `apps/social/` (new) | D2 | available now |
| **D4** safety/moderation | `apps/safety/` | D2, **D3** | blocked on D3 |
| **D6** media | `apps/media/`, storage backend | D3, D4 | blocked |
| **D5** chat | `apps/chat/`, `config/asgi.py` | D3, D4 | blocked |
| **D8** booking | `apps/booking/` | D3, D7 | blocked |

So *right now* three tracks run in parallel with disjoint paths: **D7**, **D2-eudi**, **D3** (see
the execution plan below). D4/D5/D6/D8 unblock as their upstreams merge.

## The workflow per agent

1. **Claim a track** on the [WORKBOARD](WORKBOARD.md) (set branch + status) as your **first commit**,
   so other sessions can see it.
2. **Isolate**: work on a dedicated branch `claude/<track>-<slug>` — the branch name **is** your
   session id — or a **git worktree** (Claude Code's `Agent` tool supports `isolation: "worktree"`)
   so checkouts don't clash.
3. **Contract-first**: if others depend on you, land the interface/stub early and announce it.
4. **Stay in your lane**: edit only owned paths; need a shared-file change? keep it minimal or hand
   it to the integrator.
5. **Green before merge**: `ruff check . && pytest && python manage.py makemigrations --check &&
   pip-audit` must pass locally; CI re-checks on the PR.
6. **PR referencing the deliverable**; the **integrator** merges in dependency order and resolves
   any migration/config conflicts.
7. **Update status** to ✅ on merge.

## Cross-branch synchronization

Branches are isolated — a session can't see another session's *uncommitted* work. So we
synchronize through a few explicit, shared points rather than trying to share live state:

1. **`main` is the single source of truth.** Everyone branches from `main` and integrates back into
   `main`. Nothing is "done" until it's merged there.
2. **The [WORKBOARD](WORKBOARD.md) is the shared intent.** Claim your track (branch + owned paths)
   before coding; read it before starting so you know what others own. This is how sessions "see"
   each other across branches.
3. **Ownership keeps branches disjoint.** Stay in your "Owns" paths → branches rarely touch the
   same files → merges stay clean. The handful of shared files are listed on the workboard.
4. **Rebase on `main` frequently** — at least after any other track merges — so you pick up others'
   work early and resolve drift in small increments instead of one big conflict at the end.
5. **Contract-first for dependencies.** If others depend on you, land the interface/stub to `main`
   *first* and note it on the workboard; dependents code against the merged contract.
6. **PRs are the only integration path, and CI is the gate.** Merges are serialized through PRs;
   CI (ruff + pytest + migrations + pip-audit + Docker build) must be green. The integrator merges
   in dependency order and owns conflict resolution on shared files.
7. **Migrations**: one migration-owner per app; if two heads appear, `makemigrations --merge` +
   rebase. Different apps don't collide.

If two sessions genuinely must touch the same file, one waits — coordinate via the workboard rather
than racing.

## Recommended parallel execution plan (how many sessions, and what to tell them)

**Run 3 sessions now.** Three is the sweet spot: it covers every track that's unblocked today with
**disjoint ownership**, and avoids contention on shared files (`config/`, `apps/accounts`). More
than 3 would mostly block on dependencies (D4→D3, D5/D6→D3+D4, D8→D3+D7) or queue on shared edits.
**Expand to 4–5 once D3 merges** (that unblocks D4, then D5/D6).

| Session | Track | Branch | Owns |
|---|---|---|---|
| 1 | **D7** richer place data | `claude/d7-place-data` | `apps/ingestion/sources/` (Overture adapter), `apps/places/` enrichment |
| 2 | **D2-eudi** finish identity | `claude/d2-eudi` | `apps/accounts/identity/` (implement `EUDIWalletProvider`) |
| 3 | **D3** social core | `claude/d3-social-core` | `apps/social/` (new app) |

Contention note: D2-eudi and D3 both live under `apps/` but in disjoint subpackages
(`apps/accounts/identity/` vs new `apps/social/`); only `config/INSTALLED_APPS` + `config/urls.py`
are shared (D3 adds one line each — keep append-only).

### Copy-paste briefs for each session

Give each new session this (swap in the track):

> You are one of several parallel agents on this repo. **Before coding**, read `docs/WORKBOARD.md`,
> `docs/MULTI_AGENT_BUILD.md`, `docs/ROADMAP.md`, `docs/ARCHITECTURE.md`, plus `docs/SAFETY.md`,
> `docs/COMPLIANCE.md`, `docs/SECURITY.md`. Branch from `main` as **`<branch>`**, and **claim your
> row on `docs/WORKBOARD.md` in your first commit**. Build **`<track>`** per its `docs/ROADMAP.md`
> section, touching **only** your owned paths (`<paths>`); for the shared `config/` files make
> minimal append-only edits. Reuse existing seams (D1 `Place`/`ActivityType`, D2 `accounts.User` +
> `can_participate` cohort/consent gate). Enforce the product invariants (text-first; cohort
> isolation — children only with similar-age peers). The DB needs a fresh `migrate` (custom user
> model). Before opening a PR into `main`, make `ruff check .`, `ruff format --check .`,
> `python manage.py makemigrations --check`, `pytest`, and `pip-audit` all green. Open a PR titled
> for the track; don't merge others' work.

Per-track specifics:
- **D7** — implement the `OvertureAdapter` stub (DuckDB over the Overture places release), optional
  Google enrichment behind a flag, cross-source dedup; don't change the `Place` schema casually
  (coordinate if you must — it's a shared contract).
- **D2-eudi** — implement `EUDIWalletProvider.verify(...)` returning an `AssuranceResult` (age band
  only, no PII); keep `DevIdentityProvider` working for tests; don't change `services.py`/models
  unless you also own the migration.
- **D3** — new `apps/social/`: `Activity` (= `Place` + `ActivityType` + time + owner), `Thread`/
  `Post` (text-only), `Membership`, **join-by-vote** (default two-thirds), user-place quorum;
  gate visibility/joining with the D2 cohort + `can_participate`. Add `apps.social` to
  `INSTALLED_APPS` and one `include()` in `config/urls.py` (append-only).

## Roles

- **Lead / integrator** (one agent or a human): owns merge order per the dep graph, owns
  `config/` wiring, resolves cross-cutting conflicts, keeps the [WORKBOARD](WORKBOARD.md) +
  [ROADMAP](ROADMAP.md) current.
- **Track agents**: build one app/deliverable each within their ownership.

## Pre-flight checklist (every agent, before coding)

- [ ] Read [ROADMAP](ROADMAP.md), [ARCHITECTURE](ARCHITECTURE.md), and this doc.
- [ ] My track's upstream deps are **merged** (not just started).
- [ ] I'm on my own branch/worktree.
- [ ] I'm only touching my owned paths (or have coordinated a shared-file change).
- [ ] Shared interfaces I depend on are agreed/stubbed.
- [ ] I will run the full green-gate before opening a PR.
