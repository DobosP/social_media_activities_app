# ADR-0038 — Native verification and operator toolchain

Date: 2026-10-05
Status: proposed; native qualified, reference retirement blocked; landing per ADR-0040; human review gates first deployment
Extends: [ADR-0032](0032-complete-native-go-backend.md).

## Decision

The owner extended native migration to ordinary server build, tests, fixtures, clients,
content/schema/seed/export/operator tools, benchmarks, audits and CI. Go supplies backend
verification and operators; TypeScript/Node browser tests, shell/Compose/HCL orchestration,
PostgreSQL tools and native codecs/libraries retain their appropriate boundaries. No Go
HTTP proxy or required Django oracle can be advertised as a native serving/toolchain port.

Freeze every original reference test declaration with qualified IDs and source hashes before
retirement. Each contract needs named native tests/scenarios, assertion rationale and actual
qualification, or a concrete unresolved gap. The2671 original declarations and2791 expanded
legacy cases are not equated with303 grouped native tests. A Go-only inventory/evidence
checker fails on missing, partial, unrun or invalid mappings. Keep all unresolved reference
behavior available; removing code or tests cannot manufacture a completed port.

Ordinary manual CI runs native Go and Node, shared-source hashes, actual PostgreSQL/codec
contracts, real exporter-to-sidecar processes and source/linked/image audits. Optional offline
reference comparison keeps its existing lint/schema/deploy/security checks in a separate,
explicitly manual workflow. Native shell Git hooks replace the Python hook harness. No
remote workflow enable/dispatch or hosted success is implied by these source edits.

Operators use bounded native CSP digest input and typed output, and native private EU S3
backup upload/recovery/probe with the existing endpoint/SigV4/SSE policy. Keep pg_dump,
gzip and pg_restore as native platform tools. Backup lifecycle and real recovery acceptance
remain operator obligations; qualification uses synthetic files/TLS only, never real uploads.
The shared producer HTTP/data contract remains separate from acquisition/ML internals;
romania_scraper's authorized Python producer exception is unchanged. A language-neutral
consumer pin/target retirement must be coordinated at the producer metadata boundary.

Generic offline fleet governance helpers (worktree creation, check_docs, release_readiness)
are a scoped owner-approved exception, not ordinary web project runtime/tools. Credentials,
raw data and actual env/auth stores are never migration evidence.

## Context and consequences

The Go release already served natively, but ci.yml/pre-commit/reference requirements,
Python source tests/operators and an awscli backup path still required Python for normal
verification or operation.783 Python files included276 actual collector-pattern test files
with2671 declared cases. Existing native goldens and303 qualified contracts provide strong
independent baselines, not a blanket source-case equivalence claim.

New matrices exercise transport/envelope drift, native source/codec/evidence retention,
identity/cohort/consent authority, current relationship semantics, schema/seed/FK adoption,
bounded query growth and actual process/operator boundaries. Contract mismatches discovered
by porting are repaired with their own regressions; safety or behavior is never deleted to
make the ledger green. Benchmark timings describe the measured fixture/machine only.

An exhausted stale processing lease has no committed failure outcome. Failed claim-finalization
transactions now surface an error; source bytes/keys remain held for operator recovery, including
scanner-outage plus failed-audit combinations. A pending exhausted attachment retains normal
terminal cleanup. This conservative unknown-outcome hold is not claimed as equivalent to every
reference crash-exhaustion case, and needs sensitive-code review before landing.

Current gaps and exact receipts belong in STATUS/WORKLOG and the case manifests. Python
reference retirement remains blocked until its independent native coverage is qualified.
Source privacy/auth/safety changes need human review before landing. There is no production
verification, ingestion, scheduler/provider/minor activation, procurement or deployment here.

## 2026-10-05 — Exhausted video lease no longer blocks the queue

The oldest-first video claim selected an exhausted stale lease on every drain, so one held row
blocked every later video. The claim now fences stale processing rows at or above the attempt
budget. Such a row stays held with its evidence: status, source key, attempt count and source
bytes are unchanged, nothing is queued for deletion and no terminal audit is written. Later
videos keep processing, and a drain that finds only held rows completes without error. The
in-transaction exhaustion re-check stays as a defensive guard; pending exhausted attachments
keep their normal terminal cleanup.

A held row no longer makes every drain fail, so nothing signals it by itself. The
administrator console's attachment summary now shows status, attempt count and claim time,
which identifies a held row (processing, attempts at the budget, an old claim time); there is
no held-row count or filter. The expiry purge skips processing rows, so a held source also
outlives its attachment's time-to-live until an operator acts; owner deletion and account
erasure still remove it. Releasing or finalizing a held row stays a manual operator action
until a reviewed tool exists. These are known gaps.
