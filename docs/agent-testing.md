# Native Agent Testing Guide — social_media_activities_app

Last verified: 2026-10-05

## Environment and boundaries

- Go1.27.1 serves and qualifies the backend; Node24/TypeScript qualify the frontend.
- Default Docker/Compose runs Go. Never run pytest/manage.py inside that image.
- Explicit disposable PostgreSQL16/PostGIS/vector and native AVIF/WebP/FFmpeg codecs
  qualify database, media, erasure, admission, CLI and actual sidecar-process contracts.
- Python reference sources remain optional offline evidence while case ports are unresolved;
  they are not ordinary build/test/operator dependencies. [ADR-0038](adr/0038-native-verification-toolchain.md).
- CI is manual-only. Native Actions may be disabled remotely; source/local checks do not
  assert hosted success or authorize enabling workflows.

## Commands

| Scope | Command | Required result |
|---|---|---|
| Native source/hashes/hermetic tests | `scripts/check-native.sh /absolute/path/to/go` | GOROOT gofmt (missing/failing gofmt fails), portable auth hashes, vet, race, `node --test` offline service worker and whitespace pass; needs node |
| Backend only | `GOWORK=off go -C services/server test -race ./... && go -C services/server vet ./...` | pass; DB tests skipped without explicit DSN do not qualify a release |
| Shared auth | `GOWORK=off go -C services/authcore test -race ./... && go -C services/authcore vet ./...` | pass |
| Public service | `GOWORK=off go -C services/agentapi test -race ./... && go -C services/agentapi vet ./...` | pass; loopback test sockets permitted |
| Explicit native fixture | `scripts/qualify-native.sh GO IMAGE PRIVATE_NETWORK SYNTHETIC_DSN SCRATCH` | Linux host only; fails closed (grep, no rg dependency): every package has PASS>0 and zero skips; actual FK/codec/export-to-sidecar checks |
| Native gate harness | `bash scripts/test-native-gates.sh` | `native gate harness: N/N passed`; stubbed go/docker/gofmt/node, no rg, docker, network or database |
| Case retirement gate | `go -C services/server run ./cmd/check-contracts -root "$PWD" -summary` | no unresolved or invalid source-case evidence before deleting legacy behavior; its verified count is manifest-claimed (`runtime_verification`), not checked against a test run |
| Browser build | `cd frontend && npm ci && npm test && npm run build` | contracts/typecheck/build and initial bundle budget pass |
| Fleet docs | `python3 ~/work/agent-ops/scripts/check_docs.py .` | files varies; dead_links/stale_terms/retired_verbs/orphans all0 (scoped generic fleet tool exception) |
| Whitespace | `git diff --check` | no output |

Supply GOWORK=off, GOMAXPROCS=2, GOFLAGS=-p=2 and task-owned GOCACHE/GOMODCACHE/TMPDIR
when reviewing concurrently. Scratch stays under `~/work/_temp/<task-slug>`; no production
DSN/environment discovery. The fixture harness builds the independent Go sidecar and passes
its explicit binary path to export qualification. Schema bootstrap is native `--migrate-only`.

## Evidence requirements

- Count top-level tests separately from parameter/matrix cases.303 baseline native contracts
  are not2671 source test declaration equivalents (legacy expanded count2791 plus38 subtests).
- Source-case inventory, source hashes, named native tests/scenarios and assertion rationale
  live under `internal/contracts/testdata`. Missing/partial/unrun mappings block retirement.
- Qualify new behavior with positive and adversarial synthetic cases, actual FK/schema adoption,
  current authority/consent/cohort/block gates, erasure and bounded query growth as applicable.
- Benchmark only the fixture/machine explicitly tested. Use the bounded synthetic app command
  `app.test -test.run '^$' -test.bench BenchmarkNativeAnonymousContractPaths -test.benchtime=20x`
  with the explicit fixture flag. Record allocations/timings; do not infer production costs.
- Source/imported-package/linked-binary vulnerability gates use pinned govulncheck; final
  image gate uses pinned Trivy. Required but unimported advisories are recorded separately.

## Before commit and landing

1. Run affected native fixtures with explicit flags; no skipped integration qualification.
2. Run native format/vet/race, auth hashes, whitespace and relevant frontend gates.
3. Record exact commands/results/remaining gaps in TASK_RESULT.md (ignored) and WORKLOG;
   current truth goes in STATUS.md. Source changes require matching fresh-source evidence.
4. Run fleet doc/link gate for docs. Root coordinates all local commits, publication and landing.
5. Human auth/privacy/safety review remains required before landing; test success does not
   activate minors, providers, ingestion, schedules, paid infrastructure or deployment.

Optional historical Python verification is isolated in `reference.yml`, manually dispatched
only when that offline comparison is explicitly requested. Its schema/lint/security gates
remain intact; it cannot substitute for native qualification or hide unresolved case ports.
