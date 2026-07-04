# ADR-0013: Graceful Readiness Drain

Date: 2026-07-04
Status: accepted

## Decision
Keep shutdown drain state process-local in `apps.ops.readiness`: SIGTERM/SIGINT and the test helper
mark the process as draining, `/readyz` returns HTTP 503 with only `{"status": "draining",
"draining": true}`, and `/healthz` remains a cheap liveness endpoint.

## Context / why
Scaled deploys need a node-local readiness bit that can shed new traffic during graceful shutdown
without requiring new infrastructure or a shared datastore. The signal hook must be conservative:
it sets readiness state first, then delegates to any existing server signal handler, and preserves
normal termination semantics when no callable handler existed. The response intentionally avoids
hostnames, signal names, process IDs, dependency exceptions, or other operational detail.

## Consequences
Load balancers should continue using `/readyz` for traffic eligibility and `/healthz` for process
liveness. Draining short-circuits dependency checks to avoid slow shutdown probes. Future
multi-process deploys still need each worker/process probed independently or coordinated by the
process manager, because this state is intentionally not shared across processes.
