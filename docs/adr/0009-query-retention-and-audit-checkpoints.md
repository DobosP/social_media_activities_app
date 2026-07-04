# ADR-0009: Query retention and audit checkpoints

Date: 2026-07-04
Status: accepted

## Decision
Keep hot inbox reads on a concurrent `(recipient, -created_at)` Notification index, run
notification retention through a bounded `notifications.retention_purge` DeferredTask, stream audit
chain verification, and expose a verified audit high-water checkpoint for incremental extension
checks. Add focused query-count guards for v1 messaging and social membership list surfaces.

## Context / why
Notification fan-out makes inbox reads and old convenience rows grow quickly, but MODERATION and
SYSTEM notifications carry DSA/safety duties and cannot be treated as disposable convenience data.
AuditLog is append-only and never purged, so verification must avoid materialising the table. The
repo has a concrete zero-downtime index pattern (`AddIndexConcurrently`, `atomic=False`) but no
migration-linter dependency or CI hook yet.

## Consequences
Retention work is retryable and bounded by settings, and it excludes unread plus non-mutable
notices. Audit checkpoints speed recurring extension verification, while periodic full verification
is still required to re-check old history. Query-count tests now cover more list-style endpoints.
Automated zero-downtime migration linting remains open until the project adds a linter seam.
