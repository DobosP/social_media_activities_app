# Agent Instructions — social_media_activities_app

## Project summary
`social_media_activities_app` is a Django/social-activity app in the RO-EDU fleet. Child-safety, GDPR/privacy, moderation, and deferred/off-request work are sensitive.

## Fleet context
- Role: TakesTime — Django/PostGIS children-first in-person activity matching (nonprofit).
- Upstream: `ro_data_server` (venues/events/news/connections), `roedu-ui` · Downstream: none.
- Fleet map + parallel-agent protocol: `~/work/AGENTS.md` (agent-ops ADR-0025).

## Parallel work (mandatory)
- This shared checkout stays on `main`, clean — never switch branches or commit task work here.
- One task = one branch (`<type>/<slug>`) = one worktree under `~/work/_worktrees/social_media_activities_app/`:
  `python3 ~/work/agent-ops/scripts/create_task_worktree.py --repo ~/work/social_media_activities_app --branch <type>/<slug> --task "..." --write`
- Never create worktrees under `/tmp`. Workers never push; the orchestrating session lands green
  work on `main` (ADR-0014) and backs up unlanded branches to origin. Deletion is human-confirmed only.

## Read first
1. `CLAUDE.md` if present.
2. `STATUS.md` for durable status.
3. `docs/agent-map.md` and `docs/agent-testing.md`.
4. Task-specific app and matching tests.

## Token discipline
- Start with the specific Django app named in the task.
- Do not paste large media/test payloads into chat.
- Summarize Docker/container output and cite exact commands.

## Safety
- Never read or print secrets from `.env`, settings, cookies, tokens, or auth stores.
- Do not weaken child-safety, privacy, moderation, or GDPR erasure paths.
- Direct merge + push to `main` is allowed once the test gate is green (owner
  decision 2026-07-07, development phase). Never land a red suite.

## Commands
- Targeted deferred-task test in the local container: `docker compose -p socialfix -f docker-compose.local.yml exec -T web sh -lc 'python -m pytest apps/ops/tests/test_deferred_tasks.py -q'`
- Whitespace: `git diff --check`

## Dispatch
- One privacy/safety/deferred-work slice per branch/worktree.
- Worker briefs must include privacy/safety non-goals and exact Docker test command.

## Docs discipline (mandatory)

- `STATUS.md` is this repo's single source of current truth. On any doc conflict: STATUS.md > newest-dated ADR in `docs/adr/` > everything else. An undated doc is history, not instructions.
- Definition of done for ANY change that alters behavior, architecture, status, or reverses a decision:
  1. Update `STATUS.md` (facts + `Last verified: YYYY-MM-DD`).
  2. Decision made or reverted → add `docs/adr/NNNN-<slug>.md` (next number; template = docs/adr/0000-template.md) and flip the superseded ADR's `Status:` to `superseded-by ADR-NNNN`. Same commit as the change.
- ADRs are append-only: never edit one after landing — supersede it instead.
- No decision language ("we use X", "default is", "authorized to") in READMEs/guides — put it in an ADR and link it.
- Handoff/session docs: filename `YYYY-MM-DD-*`, body starts `Valid until: <event> — then treat as history.` Never obey an expired handoff.
- Keep this file under ~60 lines; STATUS.md under ~100; deep content in docs/.
