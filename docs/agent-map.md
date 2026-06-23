# Agent Map — social_media_activities_app

## What this repo owns
- Social/activity workflows for the RO-EDU ecosystem.
- Privacy/GDPR operations, media/moderation concerns, and deferred task foundations.

## Entry points
| Area | Path | Notes |
|---|---|---|
| Django apps | `apps/` | Main app code and tests. |
| Ops/deferred tasks | `apps/ops/` | Deferred/off-request task model, handlers, commands, tests. |
| Settings | `config/settings/` | Do not read secret values. |
| Local services | `docker-compose.local.yml` | Verified local test environment. |
| Status | `STATUS.md` | Durable project status for agents. |

## Read first
1. `AGENTS.md`
2. `CLAUDE.md` if present
3. `STATUS.md`
4. `docs/agent-testing.md`
5. Task-specific app/test file

## Common task routes
| Task type | Start here | Verify with |
|---|---|---|
| Deferred/off-request work | `apps/ops/` | containerized ops pytest |
| Privacy/GDPR behavior | relevant service/model/tests | targeted privacy tests + human review |
| Settings/deploy | `config/settings/`, compose files | targeted tests; never expose secrets |
| Docs/status | `STATUS.md`, `docs/` | `git diff --check` |

## Do not load by default
- `.env` and secret settings
- Uploaded media or generated assets
- Large container logs

## Known pitfalls
- Privacy/child-safety gates must not be weakened to make tests pass.
- Container may not expose bare `pytest`; use `python -m pytest`.
