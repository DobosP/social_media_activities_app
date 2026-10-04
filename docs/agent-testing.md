# Agent Testing Guide — social_media_activities_app

Last verified: 2026-10-04

## Environment
- Review runtime: Go 1.27.1 + native codecs/PostgreSQL, `docs/NATIVE_SERVER.md`.
- Python/Django Compose commands below are offline compatibility-oracle gates.
- Verified local compose project name: `socialfix` (its `docker-compose.local.yml` is untracked/gitignored).
- Use `python -m pytest` in the container; bare `pytest` may not be on PATH.
- `-p socialfix` targets that dev host's compose project; omit it if you created the project with a plain
  `docker compose -f docker-compose.local.yml up` (Compose then names it after the directory).
- CI matrix: `.github/workflows/ci.yml` (jobs: `frontend`, `lint-test`, `docker-build`, `audit`).

## Commands
| Scope | Command | Expected |
|---|---|---|
| Targeted deferred-task tests | `docker compose -p socialfix -f docker-compose.local.yml exec -T web sh -lc 'python -m pytest apps/ops/tests/test_deferred_tasks.py -q'` | `N passed`, no failures. Do not hard-code `N` — the file grows. |
| Full suite (container) | `docker compose -p socialfix -f docker-compose.local.yml exec -T web sh -lc 'python -m pytest -q'` | all pass; historical failures below did not recur in 2026-10-04 CI |
| Full suite (CI-equivalent env) | `docker compose -f docker-compose.local.yml exec -T -e DJANGO_SETTINGS_MODULE=config.settings.test -e DJANGO_SECRET_KEY=ci-secret-not-for-prod -e DATABASE_URL=postgis://app:app@db:5432/app web pytest -q` | all pass (see `README.md` §Local variant) |
| Lint | `ruff check . && ruff format --check .` | `All checks passed!` / `N files already formatted` |
| Migration drift | `python manage.py makemigrations --check --dry-run` | `No changes detected`; needs the app deps (container or a venv with `requirements*.txt`) — a bare host raises `ModuleNotFoundError: environ` |
| Frontend | `cd frontend && npm ci && npm test && npm run build` | tests green; build within the initial-bundle budget (40 KiB gzip) |
| Dependency audit | `pip-audit -r requirements.txt -r requirements-dev.txt` | `No known vulnerabilities found` (report-only on PRs, enforcing on main) |
| Python SAST | `bandit -r apps config -q --severity-level high --confidence-level high` | no findings |
| Doc gate (any doc change) | `python3 ~/work/agent-ops/scripts/check_docs.py .` | `dead_links=0 stale_terms=0 retired_verbs=0 orphans=0` (only `files=` varies); exit 0 |
| Go public service | `cd services/agentapi && go vet ./... && go test -race ./... -count=1` | all pass; loopback sockets required for healthcheck tests |
| Native exporter contract | build `services/agentapi/agentapi-test`, then isolated PostGIS pytest `apps/web/tests/test_agent_snapshot.py apps/web/tests/test_agent_go_contract.py` | schema/bytes/gates match; native test must not skip in CI |
| Whitespace | `git diff --check` | no output |

## Before commit
1. Run `git diff --check`.
2. Run the targeted container test for touched ops/deferred-task code.
3. For privacy/moderation changes, document manual review needs.
4. Record exact command output in the worker result (`TASK_RESULT.md`, gitignored — never committed) and, on landing, in `STATUS.md` §Verification record.
5. Docs touched? Run the doc gate above and paste its `files=…` line into `STATUS.md` §Verification record.

## Known failing / blocked
- Historical 2026-08-22 chat/messaging websocket failures did not recur in full 2026-10-04 CI:
  2791 tests + 38 subtests passed (job111301416554). Expect a green suite; never weaken a gate.
- If containers are down, report `docker compose ... ps` / the startup blocker instead of inventing test output.
- Do not expose secrets from settings or env files.
- `python manage.py check --deploy` needs the CI env block in `.github/workflows/ci.yml` (prod settings +
  dummy EUDI trust anchor) — CI-only unless you replicate that environment.

## Complete native Go candidate

- `GOWORK=off go -C services/server run ./cmd/check-authcore` verifies the portable shared copy.
- `go -C services/authcore test -race ./... && go -C services/authcore vet ./...`.
- `go -C services/server test -race ./... && go -C services/server vet ./...` checks hermetic contracts;
  database-required tests skip here and do not qualify a release.
- Build the release (`docker build -t social-native:test .`), bootstrap a disposable PostGIS/vector
  database with `social-server --migrate-only`, then run `scripts/qualify-native.sh GO IMAGE NETWORK DSN SCRATCH`.
  DSN is explicit synthetic fixture only; private Docker network, no published DB ports, no real data.
  Scratch is task-owned under `~/work/_temp/<slug>`, sources read-only, real codecs, `-race`, zero skips.
  The harness includes all19 CLI/domain lanes, including configured policy and shared-budget contracts.
- Source/binary audits: `govulncheck@v1.8.0 ./...` and `-mode=binary` on the release executable;
  module-only unimported advisories are described separately from reachable/imported findings.
- `.github/workflows/native.yml` runs native bootstrap and all required database/codec lanes.
- Auth/privacy/safety human review still precedes landing: [ADR-0032](adr/0032-complete-native-go-backend.md).
