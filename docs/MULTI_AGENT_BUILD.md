# Building with multiple agents in parallel

This project is meant to be built by **several Claude Code agents/sessions working concurrently**,
not one at a time. This doc is the coordination contract: read it **before** starting work so
agents don't step on each other. It complements [ROADMAP](ROADMAP.md) (what to build, in what
order) and [ARCHITECTURE](ARCHITECTURE.md) (the seams).

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

| Track | Owns (paths) | Depends on | Can run alongside |
|---|---|---|---|
| **IS-2** CI & quality gates | `.github/workflows/`, `requirements*.in` | — | anything |
| **D2** identity/accounts | `apps/accounts/` + `IdentityProvider` contract | IS-1, IS-2 | D7 |
| **D7** richer place data | `apps/ingestion/sources/`, `apps/places/` enrich | D1 (done) | D2 |
| **Docs** | `docs/` | — | anything |
| **D3** social core | `apps/social/` | D2 merged | D7 |
| **D4** safety/moderation | `apps/safety/`, admin queues | D2, D3 | — |
| **D5** chat | `apps/chat/`, `config/asgi.py` | D3, D4 | D6 |
| **D6** media | `apps/media/`, storage backend | D3, D4 | D5 |
| **D8** booking | `apps/booking/` + `BookingProvider` contract | D3, D7 | — |

So *right now* you could run, in parallel: **IS-2 (CI)**, **D2 (identity)**, **D7 (place data)**,
and **Docs** — four agents, disjoint paths.

## The workflow per agent

1. **Claim a track** (update its status in [ROADMAP](ROADMAP.md) to ▶️, with your branch name).
2. **Isolate**: work on a dedicated branch `claude/<track>-<slug>`, or a **git worktree** (Claude
   Code's `Agent` tool supports `isolation: "worktree"`) so checkouts don't clash.
3. **Contract-first**: if others depend on you, land the interface/stub early and announce it.
4. **Stay in your lane**: edit only owned paths; need a shared-file change? keep it minimal or hand
   it to the integrator.
5. **Green before merge**: `ruff check . && pytest && python manage.py makemigrations --check &&
   pip-audit` must pass locally; CI re-checks on the PR.
6. **PR referencing the deliverable**; the **integrator** merges in dependency order and resolves
   any migration/config conflicts.
7. **Update status** to ✅ on merge.

## Roles

- **Lead / integrator** (one agent or a human): owns merge order per the dep graph, owns
  `config/` wiring, resolves cross-cutting conflicts, keeps the [ROADMAP](ROADMAP.md) board current.
- **Track agents**: build one app/deliverable each within their ownership.

## Pre-flight checklist (every agent, before coding)

- [ ] Read [ROADMAP](ROADMAP.md), [ARCHITECTURE](ARCHITECTURE.md), and this doc.
- [ ] My track's upstream deps are **merged** (not just started).
- [ ] I'm on my own branch/worktree.
- [ ] I'm only touching my owned paths (or have coordinated a shared-file change).
- [ ] Shared interfaces I depend on are agreed/stubbed.
- [ ] I will run the full green-gate before opening a PR.
