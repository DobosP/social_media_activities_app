# ADR-0072: Build and qualify with the Go 1.27.3 security patch toolchain

Date: 2026-10-21
Status: accepted (landing blocker fix under Paul's 2026-10-21 Social main-landing authorization)
Amends: [ADR-0031](0031-go-public-serving-foundation.md) "Go 1.27.1 matches the Romanian app's toolchain".

## Decision

Release and qualification toolchains move to go1.27.3: both Dockerfile builders use
`golang:1.27.3-bookworm` (the sidecar keeps a digest pin), `services/agentapi` declares
`go 1.27.3`, and every dispatch workflow reads its version from that module. Local gates use
a sha256-verified go.dev go1.27.3 with `GOTOOLCHAIN=local`.

`services/server/go.mod` and `AGENTS.md` stay byte-identical. The GUI source-custody tests
(`internal/web/testdata/gui-preferences-get/registry.json`, `gui-public-get-walk/source.json`)
pin 138 product inputs including those two files, and refuse any unlisted byte change. The
server module therefore still declares the minimum `go 1.27.1`: an old local toolchain is not
refused, so release artifacts must come from the builders or gates above. `services/authcore`
stays byte-identical to its hash-pinned canonical snapshot.

## Context / why

ADR-0040 requires a canonical no-cache image, Trivy v0.75.0 and govulncheck v1.8.0 to pass
before an integration-branch landing. On 2026-10-21 the canonical image of `bbaa198` failed
Trivy (four HIGH standard-library CVEs) and govulncheck source mode reported reachable
standard-library vulnerabilities in net/http, its HTTP/2 package, crypto/tls, crypto/x509,
net/textproto and os, all fixed by go1.27.2 or go1.27.3. `main` has the same exposure; it
predates the GUI feature. go1.27.3 is the current stable release.

## Consequences

Raising the server module's `go` line, or editing `AGENTS.md`, first needs a reviewed
extension of the custody chain. Cat and Teacher remain on go1.27.1 until their own lanes bump
them. Historical receipts that name go1.27.1 remain true for their recorded sources.
