# Native Go completion qualification

Valid until: the qualified runtime or its policy/authorization contracts change — then treat as history.

Verified: 2026-10-05. Runtime source: f8874bc74dc3e3024166a71269a65d9b9b832cfc;
subsequent coordinator changes add test/documentation evidence only. This is a source/release
qualification receipt, not production deployment, provider acceptance or product launch evidence.

## Completed scope

- PostgreSQL admission for API/social/catalog/saved-search/CSP quotas, bounded per-scope/subject
  histories, per-key locks, constant-time capacity accounting, expiry and account-erasure coverage.
- Typed operational policy overrides and privacy-safe bounded Sentry capture/shutdown; no real
  Sentry outbound. Unsafe floors and retired Python/unused Redis settings fail by name.
- Guarded fixed admin permission presets, current eligibility, atomic credential revocation/audit,
  last-manager protection and current participation authority.
- Complete registered API field contracts; public schema/guide routes are reachable. The emitted
  OpenAPI has380 operations,318 paths and166 schemas including its own typed protocol model.

Decisions: ADR-0037/0034/0035/0036. Human auth/privacy/safety review remains required before landing.

## Native qualification

Go1.27.1 `test -race ./...`, `vet ./...`, gofmt and portable auth-source hashes pass.
All19 CLI/domain lanes qualified on an explicitly supplied synthetic PostgreSQL16/PostGIS/vector
fixture, read-only source, Python-free native codec image and `-race`:303 top-level tests,
zero skips or failures. All166 tests in the six affected account/app/media/safety/social/web lanes
were rerun after the review fixes; the remaining137 unchanged contracts retain their prior passing
qualification. The full combined hermetic race/vet suite and release/audits were refreshed.

| Lane | Passed |
|---|---:|
| configuration | 33 |
| accounts | 20 |
| admin | 18 |
| app | 25 |
| booking | 7 |
| budgets | 9 |
| catalog | 11 |
| commands | 9 |
| discovery | 2 |
| donations | 5 |
| export | 2 |
| jobs | 22 |
| media | 31 |
| messaging | 11 |
| notifications | 2 |
| recommendations | 6 |
| safety | 20 |
| social | 26 |
| web | 44 |

Commands follow `scripts/qualify-native.sh`; fixture network `go-migration-finish-test`,
release tag `social-native:go-review-final-20261004`, scratch under `_temp/go-migration-finish`.
Only synthetic data/credentials were supplied. No production database, credentials, real ingestion,
recurring work, registered providers or minors were activated.

## Release and audits

Final image: `sha256:4a660c95cf61e035384969c62292d6598f82f3ba4fce3f2c6070e47810123a33`.
UID10001, read-only execution, native ffmpeg/ffprobe/avifenc/prlimit; Python/pip absent.
The OS/codec base layers match the prior Go-only qualification image. The final executable
applies native `--migrate-only` on the synthetic database. HTTP readiness, schema and guide
return200; actual schema counts380/318/166 match the tests. Shutdown exits0 without OOM.
No host ports or production ingress were published.

Source, imported-package and same-source unstripped linked-binary govulncheckv1.8.0 audits
pass with zero reachable/imported vulnerabilities. The existing required-module advisory
GO-2026-5932 concerns unmaintained `golang.org/x/crypto/openpgp` in x/crypto0.57.0; the package
is neither imported nor called, and no fix version exists. It is recorded separately from
reachable/imported findings, not suppressed. Trivyv0.75.0 scans the final image archive with
`--severity HIGH,CRITICAL --ignore-unfixed --exit-code1`:zero fixable findings in OS or Go binary.

Documentation/link/whitespace gates pass; current status and any hosted-CI/review limits are
in STATUS.md. Template YAML parses and contains no retired Python runtime assignments.
Actual production storage/scanner/provider/backup/restore/ingress acceptance remains a launch gate.

## Publication and hosted validation

Completion is published for human review in PR108. Parallel main commit cd006e3 introduced
accepted ADR-0033 on-demand Actions and is incorporated in the candidate. The proposed
shared-budget decision is ADR-0037; config/admin/authority remain0034/0035/0036.
The native Actions page visibly reports that the workflow is manually disabled; no remote
run was queued or a passing hosted result claimed. Full local qualification above passes.
No workflow was re-enabled, and the manual-only trigger policy is preserved.

## Independent review follow-up

Existing completed worker sessions were reused for rotated review; no duplicate chat was
created. Configuration/Sentry review found no actionable issue. Other reviews confirmed
and the coordinator integrated these repairs:

- Profile disclosure reloads current viewer state after rate admission, preserving source
  minimal/shared visibility while refusing inactive/unassigned/cross-cohort views and clamping
  current minors. Real admission-time state changes, API/HTML and private-photo gates are tested.
  Person templates receive their actual card/report/block context; photos use existing fresh
  media authorization and signed serving rather than raw signing.
- Composer MIME/capabilities honor attachment/file/video switches and cohort policy. Effective
  disappearance choices/labels roundtrip into stored expiry, while zero/blank Keep and admission
  floors remain. Native explicit false never falls back to offline-oracle template defaults.
- Group creation uses one policy helper in domain and HTML/SPA capability projection.
- The inherited fixed-window expiry/erasure inversion is repaired by separately committed
  bounded skip-locked pruning and user-before-budget admission. Actual erasure under a
  two-connection pool fails with40P01 in an old-source overlay and passes with the repair.

All new fix branches remain unlanded pending the same human sensitive-code review gate.
The previous280-test/image453f7048 receipt is preserved in WORKLOG as historical qualification.
