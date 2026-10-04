# Agent Instructions — social_media_activities_app

## Project summary
- Nonprofit, open-source, children-first platform for organizing **in-person** local activities: Cluj-Napoca
  first, EU residency required, donations only, no ads and no engagement ML. Not launched. Child-safety,
  GDPR/DSA, moderation and deferred/off-request work are the sensitive surfaces.
- Runtime: native Go (`services/server`) owns HTTP/auth/domain/media/jobs/HTML/live delivery on PostgreSQL/
  PostGIS + pgvector; Preact/Vite stays in `frontend/`. `services/authcore` is a hash-pinned shared copy.
  Django is an offline oracle (`Dockerfile.reference`); deployment status and gates are in `STATUS.md`.
- Current truth: `STATUS.md`.

## Fleet context
- Role: consumer of the RO-EDU data platform (romania_scraper `/v1`) and the children-first social-activities engine; pre-launch. Canonical role/status/next: vault note `dobo-brain/paul-brain/projects/social-media-activities-app.md`
  (fleet view: vault `projects/index.md` + `NOW.md`; agent-ops ADR-0032). Upstream: `romania_scraper` (producer), `ro_data_server` (serving) · Downstream: none.
- Fleet map + parallel-agent protocol: `~/work/AGENTS.md` (agent-ops ADR-0025/0026). Global session, git,
  scratch and secrets rules: `~/.claude/CLAUDE.md` (agent-ops ADR-0027/0028/0037/0063/0074/0077) — cited here, not restated.
- Delegation: roles and rungs per the `agent-routing` skill (the ladder in `fleet-tiers.sh`); Codex is opt-in (agent-ops ADR-0065).

## Parallel work (mandatory)
- This shared checkout stays on `main`, clean — clean includes untracked (agent-ops ADR-0063): `git status --porcelain`
  is empty when you finish; a stray file blocks the next task-worktree/Ctrl-N session here. A stray file you
  did not write gets reported, not deleted.
- One task = one branch (`<type>/<slug>`) = one worktree `~/work/_worktrees/social_media_activities_app/<slug>`, never under `/tmp`:
  `python3 ~/work/agent-ops/scripts/create_task_worktree.py --repo ~/work/social_media_activities_app --branch <type>/<slug> --task "..." --write`
  Scratch and one-off scripts: `~/work/_temp/<slug>/`, run against this repo by path (agent-ops ADR-0028).
- Workers never push. The orchestrating session lands green work on `main` (agent-ops ADR-0014) and finishes the landing
  in the same session (agent-ops ADR-0037): delete the verified-merged branch (local + origin), its worktree, `_temp/<slug>/`.
  Unmerged work is deleted only per item, human-confirmed. A branch reaches origin only on land or by
  `ops publish social_media_activities_app <branch>` — `ops sync` never creates a remote ref (agent-ops ADR-0077).

## Read first
1. `STATUS.md` — current truth.
2. `docs/agent-map.md` (entry points, routes, pitfalls) and `docs/agent-testing.md` (gates + expected output).
3. `docs/SAFETY.md` §Core rules — hard child-safety invariants, never weaken.
4. `docs/ARCHITECTURE.md` §Working conventions — the 5 gating rules (services layer, atomic+audit, `notify()`
   chokepoint, `DUE_JOBS`, cohort gates).
5. `docs/FEATURES_BUILT.md` before building anything "new".
6. The native `services/server/internal/<domain>` service plus its tests; use `apps/<app>` only as an offline oracle.

Product overview: `README.md` · full doc index: `docs/README.md` · phasing map (not status): `docs/ROADMAP.md`.

## Commands
| Purpose | Command |
|---|---|
| Native local stack (http://127.0.0.1:8000) | `docker compose up --build` |
| Native schema bootstrap/adoption | `social-server --migrate-only` with explicit configured PostgreSQL |
| Native checks (Go 1.27.1) | `go -C services/server test -race ./... && go -C services/server vet ./...` |
| Native PostgreSQL + codecs | `scripts/qualify-native.sh` with explicit isolated fixture arguments (docs/agent-testing.md) |
| Native format | `test -z "$(gofmt -l services/server services/authcore)"` |
| Offline oracle migration drift | `python manage.py makemigrations --check --dry-run` in the reference environment |
| Frontend (Node 24) | `cd frontend && npm ci && npm test && npm run build` |
| Native dependency/image audits | `.github/workflows/native.yml` (source/package/binary + Trivy) |
| Offline oracle checks | Python pytest/Ruff/pip/Bandit in `.github/workflows/ci.yml` |
| Whitespace | `git diff --check` |

Expected outputs and known-failing tests: `docs/agent-testing.md`. Full CI matrix: `.github/workflows/ci.yml`.

## Safety
- Never read or print secrets from `.env`, settings, cookies, tokens, or auth stores. Env var NAMES live in
  `.env.example`; values deploy from the agent-ops SOPS store (agent-ops ADR-0027).
- Do not weaken child-safety, privacy, moderation, or GDPR erasure paths; those and auth changes require human review before landing.
- Landing green work directly on `main` is allowed in the development phase (agent-ops ADR-0014; owner
  decision 2026-07-07). Never land a red suite.
- Never run real network ingestion, enable scheduled sync, deploy, apply Terraform, or authorize minors from a task; paid infrastructure needs owner authorization.
- `apps/ingestion/sources/_roedu_client_core.py` is generated and stamped (`VENDORED_SHA256`) — never hand-edit it.
- Dispatch: one privacy/safety/deferred-work slice per branch/worktree; worker briefs carry privacy/safety
  non-goals and the exact container test command.
- Token discipline: start with the named app; never paste large media/test payloads; summarize container output.

## Docs discipline (mandatory)
- `STATUS.md` is this repo's single source of current truth. On conflict: `STATUS.md` > newest-dated ADR in
  `docs/adr/` > everything else. An undated doc is history, not instructions.
- Definition of done for any change of behavior, architecture, status, or decision — same commit: update
  `STATUS.md` (facts + `Last verified: YYYY-MM-DD`); a decision made or reverted gets `docs/adr/NNNN-<slug>.md`
  (claim the number in `docs/adr/README.md`; flip the superseded ADR's `Status:`).
- ADRs are append-only. No decision language ("we use X", "default is") in READMEs/guides — link the ADR.
- Continuation lives in the tab handoff and the vault task note (agent-ops ADR-0078), never a new dated handoff. Dated
  records open with `Valid until: <event> — then treat as history.`
- Budgets: this file ≤ 80 lines, `CLAUDE.md` ≤ 12 non-blank, `STATUS.md` ≤ 120, `docs/agent-map.md` ≤ 60,
  `docs/agent-testing.md` ≤ 80. History overflows to `WORKLOG.md`. Convention: `agent-ops/docs/29-doc-governance.md`.
