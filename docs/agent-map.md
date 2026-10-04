# Agent Map — social_media_activities_app

## Ownership
- Native Go activity/social workflows, safety/privacy, private media and deferred work.
- PostgreSQL/PostGIS/vector is primary; TypeScript/PReact remains the client.
- Python under apps/config/tests is an offline contract/migration oracle.

## Entry points
| Area | Native path | Notes |
|---|---|---|
| Assembly/config | `services/server/internal/app`, `cmd/social-server` | HTTP, providers, lifetime, settings and commands. |
| Identity | `internal/accounts`, `services/authcore` | Password/OAuth, signed EUDI, guardian/consent, erasure; snapshot hashes. |
| Social/voting | `internal/social` | Activities, memberships, groups, series, threads and guarded mutations. |
| Safety | `internal/safety`, `internal/admin` | Sanctions/appeals/audit, governed operator actions; raw sensitive CRUD refused. |
| Catalog/ingestion | `internal/catalog`, `internal/commands`, `internal/jobs` | Facts-only producer boundary, native adapters and one-shot sync. |
| Private media | `internal/media` | Real codecs, effective scanning, licensed covers, private serving and durable cleanup. |
| E2EE/live | `internal/messaging`, `internal/chat` | Client ciphertext and ID-only PG notifications; fresh authority on delivery. |
| Discovery | `internal/discovery`, `internal/recommendations` | Deterministic composition and current read gates. |
| Finance/notices | `internal/booking`, `internal/donations`, `internal/notifications` | Provider transactions and native notification chokepoint. |
| HTML/SPA | `internal/web`, `templates`, `apps/web/templates`, `locale` | Native rendering over shared presentation data. |
| Jobs/ops | `internal/jobs`, `internal/ops` | PostgreSQL queue,27 due jobs, readiness/metrics/private logs. |
| Client | `frontend` | Preact/Vite TypeScript, encrypted client transport. |
| Optional sidecar | `services/agentapi` | Independent read-only public snapshot service; native exporter owns input. |
| Deploy/DB | `deploy`, `Dockerfile`, `docker-compose.yml`, `db` | Native artifact templates; no Social production deployment. |
| Current truth | `STATUS.md`, `docs/NATIVE_SERVER.md` | Status, operating guide; history in WORKLOG/ADRs. |

Paths beginning internal/ above are under `services/server`. Domain READMEs/tests sit beside code.

## Task routing and gates
- Start with the native package named by the task, its tests and the relevant safety contract.
- Use corresponding apps/<app>/tests only for offline differential or migration evidence.
- Run targeted native race/vet; supply an explicit disposable PostgreSQL DSN and actual codecs
  for database/media changes. Default skipped integration cases cannot qualify a release.
- Frontend changes run Node24 contracts/build/bundle budget. Docs run the fleet doc/link gate.
- Deployment/configuration uses native CLI docs and typed defaults; never inspect actual env values.
- Source privacy/safety behavior requires human review before landing; owner approval for the
  complete conversion does not authorize later regressions or production activation.

## Pitfalls
- Do not defer safety, cohort, consent, block or scan admission until after an action is visible.
- General API/social/catalog rates are process-local; Redis-required/Sentry profiles are refused.
- LISTEN needs a session connection if adding PgBouncer; one-shot jobs reserve none.
- Secrets/auth stores, uploaded media, local databases and raw transcripts are not fleet docs.
- Original Python launch commands are reference-only; default Docker/Compose executes Go.
- Root/worker/worktree rules remain in AGENTS.md; shared main stays clean.
