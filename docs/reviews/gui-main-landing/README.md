# Main landing of the functional Go interfaces — 2026-10-21

Valid until: the next change to `main` — then treat as history.

Scope: `main` `5426e915` → product head `a70e0242` = feature tip `bbaa198` (119 commits: account/family/
security native pages and the 39 grouped Go destinations, [ADR-0071](../../adr/0071-functional-go-interface-completion.md))
plus the go1.27.3 toolchain fix ([ADR-0072](../../adr/0072-go-1-27-3-security-toolchain.md)). Authority:
[ADR-0073](../../adr/0073-owner-authorized-functional-gui-main-landing.md). Operator: Claude Code orchestrator,
independent of the Codex implementation. Exact commands, images, hashes and per-package counts: [results.json](results.json).

## ADR-0040 gates on `a70e0242` — all pass

Fresh `docker build --no-cache --pull` release, DB and sidecar images from a `git archive` of the head;
`check-native` (fresh cache) and gate harness 17/17; govulncheck v1.8.0 8/8 clean; Trivy v0.75.0 0 HIGH/CRITICAL
on both images; `--migrate-only`; `qualify-native.sh` on the fresh image: 21 packages, 980 top-level / 2,734 named
passes, 0 skips/failures/races; container smoke; Node 24 frontend 36.72/40 KiB; `check-contracts` verified 1020 /
invalid 0; doc and whitespace gates. The canonical image of `bbaa198` failed Trivy and govulncheck on go1.27.1.

## Independent review

Five read-only dimensions over `main..bbaa198` (authority/family/consent/security; output safety and privacy;
contracts, routes and defaults; test integrity; evidence), one refutation pass each, final adjudication:
ready-with-notes, no code blocker. Verified among others: CSRF on 116/116 native POST forms, language action
allow-list/open-redirect, policy-gated sitemap, cohort/roster and trust self-only gates, no PII beyond policy copy,
`SOCIAL_REACT_UI` still false, vendored graph bundle differs from main's pinned bundle only by two removed style
injectors. The toolchain fix was reviewed separately (APPROVE-WITH-NOTES): every shipped artifact builds with
go1.27.3, none of the custody-pinned inputs or `services/authcore` changed.

| ID | Severity | Finding | Disposition |
|---|---|---|---|
| TI-1 | high | Custody/frozen-reference tests in `internal/web` byte-pin at least 138 inputs (incl. `accounts/service.go`, `go.mod`, `go.sum`, `AGENTS.md`); one newline turns 8–13 tests red | Owner decision: retire/archive the family or author custody epochs; see STATUS |
| CRD-2 | medium | Native wards revoke drops `data-confirm`; a test forbids it; a disclosure explains instead | Deliberate design kept; owner may restore the prompt |
| TI-2 | medium | `check-native.sh` runs `go test` without `-count=1` (stale cache risk) | Follow-up; this landing used a fresh cache |
| TI-3 | medium | No generic native-output test for per-form CSRF, script nonce, no inline style | Follow-up test |
| EA-4 | medium | Historical proofs live under `_temp/gui-functional-hub` | Not deleted; NAS archive before any sweep |
| TC-1 | low | Server `go 1.27.1` floor still admits an old local toolchain | Follow-up: version assertion in the unpinned gate scripts |
| TI-4/5/6, AFC-3, OSP-1/3, CRD-6/7, EA-7 | low/info | Missing roster negative tests; hollow opt-in roots; qualifier TMPDIR; strict safe-exit 500 on drift; group-detail scripts without nonce; graph tooltip innerHTML; dev compose SPA on; unwired FS renderer; landing volume | Follow-ups; safe-exit DB test passed in the 21-lane run |

Not covered: no browser re-run (desktop 78-state evidence stays the historical QA4 claim), no human review
(GOV-4 still gates first deployment), no production, provider, government or minor activation.
