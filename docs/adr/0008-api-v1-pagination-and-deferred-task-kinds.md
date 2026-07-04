# ADR-0008: API v1 pagination and task kinds

Date: 2026-07-04
Status: accepted

## Decision
Expose the canonical API contract under `/api/v1/` with DRF `URLPathVersioning`, keep `/api/` as a transitional compatibility alias, and make new high-traffic `/api/v1/` list-style APIViews return bounded cursor/limit envelopes. Register the first production `DeferredTask` kinds for bounded blob cleanup, activity notification fan-out, allowlisted cron-command splitting, and a fail-closed/audited media-scan placeholder.

## Context / why
Native clients need a versioned path before they bind to the API shape, and high-traffic APIViews bypass DRF viewset pagination. The unversioned alias already has callers and tests, so changing those response bodies would be a needless compatibility break. DeferredTask existed as a queue foundation, but no production kind exercised it, leaving blob deletion and fan-out work on request paths.

## Consequences
New clients should use `/api/v1/` and expect cursor envelopes on the hardened list endpoints. Existing `/api/` callers keep their legacy shapes during the transition. Blob cleanup now retries off-request through Postgres-backed tasks. Media upload admission still scans synchronously and fail-closed; `media.scan.dispatch` is intentionally a no-op audit placeholder until a withheld media state exists. Broader async conversion and prod-sized index tuning remain future work.
