# Agent Testing Guide — social_media_activities_app

## Environment
- Runtime: Django/Python in Docker Compose local environment.
- Verified local compose project name: `socialfix`.
- Use `python -m pytest` in the container; bare `pytest` may not be on PATH.

## Commands
| Scope | Command | Expected success |
|---|---|---|
| Deferred task tests | `docker compose -p socialfix -f docker-compose.local.yml exec -T web sh -lc 'python -m pytest apps/ops/tests/test_deferred_tasks.py -q'` | `22 passed` on current setup |
| Whitespace | `git diff --check` | no output |

## Before commit
1. Run `git diff --check`.
2. Run the targeted container test for touched ops/deferred-task code.
3. For privacy/moderation changes, document manual review needs.
4. Record exact command output in worker result files.

## Known blockers
- If containers are down, report `docker compose ... ps` / startup blocker instead of inventing test output.
- Do not expose secrets from settings or env files.
