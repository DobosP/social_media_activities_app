# Agent Map — social_media_activities_app

## What this repo owns
- Social/activity workflows for the RO-EDU ecosystem.
- Privacy/GDPR operations, media/moderation concerns, and deferred task foundations.

## Entry points
| Area | Path | Notes |
|---|---|---|
| Moderation & safety | `apps/safety/` | Sanctions, DSA Art.16/17 record, SSRF guard (`net.py`). |
| RO-EDU ingestion | `apps/ingestion/` | Source adapters, `sources/roedu_client.py` (app layer) + generated `sources/_roedu_client_core.py` (stamped, never hand-edit); commands `ingest_places`, `sync_roedu`. |
| Events | `apps/events/` | `sync_roedu_events`, `ingest_events`, `sync_event_feeds`. |
| Deferred work | `apps/ops/` | `DeferredTask` (`models.py:18`), `run_due_jobs`, `process_deferred_tasks`, `load_roedu_seed` (local/demo only). |
| Messaging & chat | `apps/messaging/`, `apps/chat/` | E2EE DMs / activity chat; websocket consumers exercised in each app's `tests/test_consumer.py`. |
| Profile visibility | `apps/connections/profiles.py` | The sole resolver (`docs/SAFETY.md` §Core rules 4). |
| Identity | `apps/accounts/identity/providers/eudi.py` | EUDI wallet provider. |
| Server-rendered UI | `apps/web/` | Session-auth web surface over the same services. |
| Settings | `config/settings/` | base/dev/prod/test — never read secret values. |
| Frontend | `frontend/` | Preact/Vite SPA. |
| Sidecar / infra | `services/agentapi/` (:8090), `deploy/` (never applied), `db/` (seed data) | See their READMEs. |
| Cross-app tests | `tests/` | API schema, security, prod-hardening. |
| Local services | `docker-compose.yml`, `docker-compose.local.yml` | The `.local` file is untracked/gitignored (dev machines only). |
| Status | `STATUS.md` | Current truth. |

## Common task routes
| Task type | Start here | Verify with |
|---|---|---|
| Deferred/off-request work | `apps/ops/` | containerized ops pytest |
| Privacy/GDPR behavior | relevant service/model/tests | targeted privacy tests + human review |
| Settings/deploy | `config/settings/`, compose files | targeted tests; never expose secrets |
| Docs/status | `STATUS.md`, `docs/` | `git diff --check` |
| RO-EDU ingestion/sync | `apps/ingestion/`, `apps/events/`, `docs/ROEDU_INTEGRATION.md` | `apps/ingestion/tests/`, `apps/events/tests/test_roedu_sync.py` |
| Moderation / DSA redress | `apps/safety/`, `apps/social/` | `apps/safety/tests/` + human review |
| Frontend | `frontend/` | `npm test && npm run build` (initial-bundle budget) |

## Do not load by default
- `.env` and secret settings
- Uploaded media or generated assets (`static/frontend/` build output, `var/agent_snapshot/`)
- Large container logs

## Known pitfalls
- Privacy/child-safety gates must not be weakened to make tests pass.
- Container may not expose bare `pytest`; use `python -m pytest`.
- `docker-compose.local.yml` exists only on dev machines (gitignored) — on a fresh clone use `docker-compose.yml`.
- The stamped `_roedu_client_core.py` is excluded from `ruff format` but not from `ruff check` (`STATUS.md`).
- `sync_roedu`/`sync_roedu_events` require `ROEDU_API_KEY`: absence is a skip in the scheduled job and a
  `CommandError` in the command, never a dev fallback.
- The chat/messaging `test_consumer.py` websocket tests were failing identically on pristine main as of
  2026-08-22 — see `docs/agent-testing.md` before "fixing" them.
