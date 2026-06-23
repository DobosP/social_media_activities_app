# Agent Instructions — social_media_activities_app

## Project summary
`social_media_activities_app` is a Django/social-activity app in the RO-EDU fleet. Child-safety, GDPR/privacy, moderation, and deferred/off-request work are sensitive.

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
- Do not push or merge unless Paul explicitly asks.

## Commands
- Targeted deferred-task test in the local container: `docker compose -p socialfix -f docker-compose.local.yml exec -T web sh -lc 'python -m pytest apps/ops/tests/test_deferred_tasks.py -q'`
- Whitespace: `git diff --check`

## Dispatch
- One privacy/safety/deferred-work slice per branch/worktree.
- Worker briefs must include privacy/safety non-goals and exact Docker test command.
