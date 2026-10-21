# ADR-0073: Owner-authorized main landing of the functional Go interfaces

Date: 2026-10-21
Status: accepted (Paul's explicit 2026-10-21 authorization)
Authority: Paul, 2026-10-21: "This is my explicit authorization for Cat/Social main landing and main
pushes after green qualification and independent review. It supersedes older 'wait for Paul' restrictions
for those operations only. It does not authorize deployment, real-account/child activation,
provider/government connections, or content, clinical and privacy policy decisions."

## Decision

Land `feat/gui-app-migration-a` (39 new grouped Go destinations, [ADR-0071](0071-functional-go-interface-completion.md))
plus the [ADR-0072](0072-go-1-27-3-security-toolchain.md) toolchain fix on `main` as a fast-forward from
`5426e915`, after the [ADR-0040](0040-landing-and-deployment-review-gates.md) gates passed on the exact
product head `a70e0242` and an independent review of the whole `main..` diff approved it
([record](../reviews/gui-main-landing/README.md)). The closing documentation commit changes no build or
test input.

## Context / why

The pre-existing feature evidence used pinned fixture images and did not qualify a new application image.
The landing therefore re-ran every ADR-0040 gate against a fresh `--no-cache` build. The first canonical
image failed Trivy/govulncheck on go1.27.1 (ADR-0072).

## Consequences

Formal S3 milestones, deployment, provider/government/minor activation, and privacy, content or clinical
rulings remain held; this is not production readiness. Two review findings are owner decisions, not
landing fixes:
- the GUI source-custody tests byte-pin at least 138 product inputs (including `go.mod`, `go.sum` and `AGENTS.md`),
  so routine edits on `main` turn `internal/web` red until a custody epoch is authored or the family is retired;
- the native wards page deliberately replaces the `data-confirm` revoke prompt with an explanation inside a
  disclosure, and a test forbids the attribute.
