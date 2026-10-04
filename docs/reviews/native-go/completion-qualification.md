# Native Go completion qualification

Valid until: the qualified runtime or its policy/authorization contracts change — then treat as history.

Verified: 2026-10-04. Runtime source: c0f1fe0a74772b69e72fc8872fd34540a4dc43c4;
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
fixture, read-only source, Python-free native codec image and `-race`:280 top-level tests,
zero skips or failures. The final affected App/Web rerun follows the documentation-route fix.

| Lane | Passed |
|---|---:|
| configuration | 33 |
| accounts | 16 |
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
| media | 29 |
| messaging | 11 |
| notifications | 2 |
| recommendations | 6 |
| safety | 15 |
| social | 21 |
| web | 37 |

Commands follow `scripts/qualify-native.sh`; fixture network `go-migration-finish-test`,
release tag `social-native:go-migration-finish-20261004`, scratch under `_temp/go-migration-finish`.
Only synthetic data/credentials were supplied. No production database, credentials, real ingestion,
recurring work, registered providers or minors were activated.

## Release and audits

Final image: `sha256:453f7048c53d1b9b65ea5fc498cf389d32b0a457ce37413467507e4ae57820d9`.
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
