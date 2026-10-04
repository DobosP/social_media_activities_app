# ADR-0037 — PostgreSQL shared rate budgets

Date: 2026-10-04
Status: proposed for integration review

## Context

The native server's API, social, catalog and saved-search histories were process-local.
Replicas and restarts multiplied their abuse quotas. Python's atomic cache counters are the
offline policy oracle, while production PostgreSQL already owns identity, safety, ciphertext,
notifications and live publication. A paid Redis service is unnecessary for this pre-launch,
low-traffic fleet. [ADR-0032](0032-complete-native-go-backend.md) owns the native-runtime decision.

## Decision

`internal/budgets` admits these histories through atomic PostgreSQL sliding windows. The database
clock follows the per-subject lock; worker clocks never authorize a reset. Each admitted request
adds one timestamp, exhaustion supplies the oldest event's expiry as Retry-After, and denials add
nothing. Different limits/windows on replicas cannot reset a live bucket: they refuse until it
expires. Sliding windows tighten the old domain fixed-window boundary bursts; default caps and
scope-sharing remain intact. Account/safety/message fixed-window semantics remain intact.

`schema.Migrate` installs additive tables/functions/indexes and the migration version. Adoption
never resets existing identities or domain data. `App.New` checks the shared admission schema
before serving; missing schema, missing database, invalid policy or a database error fails closed.
There is no in-memory fallback. Admission and maintenance have a two-second deadline.

A dedicated per-subject advisory-lock namespace serializes missing and existing buckets. Short
AFTER STATEMENT transition-table triggers maintain exact capacity totals after affected rows have
been locked; admission has no table-wide count/sum scan. Capacity is at most 10,000 subject/scope
rows and 1,000,000 admitted events, with at most 10,000 events per row. Saturation refuses more work.
The capacity delta is atomic with the row mutation, including pruning and account-FK cascades.
These totals bound logical retained histories; they are not a disk-size or throughput guarantee.

An indexed expiry sweep locks at most 256 expired rows with SKIP LOCKED, then deletes them in a
separately committed transaction before admission. This keeps row-before-counter lock order and
avoids stale-window resurrection. `Store.Prune` exposes bounded off-request maintenance without
starting a scheduler. Expired timestamps in active histories are trimmed on admission. Idle expired
rows await the next sweep or explicit maintenance; deployment must qualify its maintenance tick.

PostgreSQL stores only action scope, timestamps, policy and an account FK or 32-byte peer digest.
Anonymous/token peers use scope-separated HMAC-SHA256 with the deployment's existing stable
`DJANGO_SECRET_KEY`; replicas must share that secret. No raw IP, credential, path, message, target
identity or browser-report content enters the budget table. User rows cascade on account erasure.
CSP reports remain sanitized, non-durable, process-local bounded buffers; their 120/min ingress
ceiling is shared globally and the browser endpoint always returns 204, including outages.

## Transactions and policy hooks

An independently committed reservation retains a debit if the later mutation fails, as in the
cache oracle. Seven previously in-transaction checks use bounded preflight/reservation/replay:
run the transaction to the exact admission point, propagate a private sentinel, roll back and
release its connection/locks, commit the reservation, then run the complete transaction once more.
Every visibility, membership, block and consent gate is evaluated again. All preflight effects
remain inside its rolled-back transaction. Early idempotent paths keep their no-debit behavior.
There is one debit and at most two domain passes; no nested pool acquisition occurs. Current actor
snapshot revocation must also be qualified by the coordinator's fresh participation gate.

`budgets.Policy{Limit, Window}` and `Resolve` expose caps of 1–10,000 and windows of 1 second–24
hours. Omitted actions retain reviewed defaults; explicit invalid overrides refuse admission.
`Service.RatePolicies` is exposed on social/catalog/recommendations/accounts/safety/messaging.
The config lane owns environment parsing, validation and application before serving. Existing
API anonymous/user/token integer fields retain their per-minute interface. PostgreSQL is always
the native shared store; `DJANGO_REQUIRE_SHARED_STATE=true` can therefore be supported by CLI
integration. `REDIS_URL` remains an explicit obsolete/unsupported dependency, rather than ignored.

## Coverage inventory

| Surface / scopes | Default caps and windows | State |
| --- | --- | --- |
| API anonymous / user / token | 60 / 240 / 10 per minute | Shared sliding; API aliases share scope; probes bypass. |
| Social `thread_post`, shared `thread_react` | 30 / 60 per minute | Shared sliding; appreciation/dissent/concern share reaction quota. |
| Social `connection_request`, `profile_card` | 20 / 240 per hour | Shared sliding; new-request and web/API/hover scopes retained. |
| Social `group_create`, `group_join`, `group_question` | 5 / 20 / 6 per hour | Shared sliding; idempotent joins do not debit. |
| Catalog `open_now_report`, `place_closure_report`, `event_report` | 10 each per hour | Shared sliding; duplicate attempt debit retained. |
| Catalog `place_fact_vote` | 40 per hour | Shared sliding. |
| Recommendations `saved_search_create` | 20 per hour, separate 20-search hard cap | Shared sliding; duplicate/failed mutation debit retained. |
| Ops `csp_report` | 120 per minute | Shared sliding ingress; 8 KiB body and 200 sanitized rows remain local resource bounds. |
| Accounts `age_start`, `avatar_style` | 30 each per hour | Existing PostgreSQL fixed windows; typed policy hooks and bounded denied counters. |
| Accounts `guardian_invite`, `guardian_guardrail`, `guardian_ward_topics` | 20 / 30 / 30 per hour | Existing PostgreSQL fixed windows; separate scopes; typed policy hooks. |
| Safety `report`, `appeal`, `unsafe_report` | 20/hour, 5/day, 12/hour | Existing PostgreSQL fixed windows; typed policy hooks and bounded counters. |
| Messaging `messaging_start`, `messaging_send` | 20 / 60 per minute | Existing PostgreSQL fixed windows; typed hooks, idempotent reuse and failed-send debit retained. |
| Authentication login attempts / session and API-token count bounds | Reviewed auth/store policy | Existing PostgreSQL shared state; canonical authcore unchanged. |
| Media `avatar_upload` | 20 per hour | Existing PostgreSQL attempt window plus audit-derived success cap; config lane hooks. |
| Jobs `saved_search_match` | 50 per day | Existing PostgreSQL shared action budget; config lane hooks. |
| Live chat | Domain post/message quotas above | No separate local user-abuse history; frame/queue/time limits protect each process. |
| Optional `services/agentapi` | 300/min refill, burst 60; 10,000 clients, ten-minute idle expiry | Intentionally DB-free local resource ceiling under ADR-0025; separate deployment contract. |

Cat's documented 7,200-second sliding in-memory game TTL and 1,000-session ceiling need game
affinity when adding instances, or a separately designed transactional store. That is an explicit
Cat architecture constraint (Cat ADR-0162); no Cat runtime or canonical authcore is changed here.

## Verification and limits

Synthetic race-enabled fixture tests cover two replicas/restarts, 64 concurrent same-subject
requests, rolling boundary expiry, scope separation, live-policy drift, keyed peer privacy,
10,000-key/1,000,000-event saturation, counter accuracy after trimming/expiry/FK erasure, concurrent
pruning+erasure, two-connection replay, changed gates, failed mutation debits, cancellation and
closed-store failure. CSP ingress remains shared while report content stays non-durable. Test
fixtures apply native function/trigger migrations because PostgreSQL LIKE does not copy them.
The qualification script includes the `budgets` package with explicit `-budgets-test-dsn`.

A laptop synthetic run admitted 100 sequential requests at 1/1,000/9,000 existing buckets in
2.97/2.10/1.91 ms mean (7.73/4.21/3.53 ms p95). An additional 2,000 distinct peers retained 26,488
bytes of Go heap after GC. These are local fixture measurements, not production capacity claims.
The short singleton counter update remains a possible high-throughput contention point; reassess
only with actual load evidence. No production database, deployment, real ingestion, provider,
scheduler or minor activation was used. Combined CLI/source qualification and human safety review
remain coordinator gates before landing.

## Worker qualification receipt

All 18 native fixture lanes pass: 194 tests, zero skips, including actual custom-policy domain
flows and legacy media/web fixtures. Legacy LIKE helpers apply migration locally so functions,
capacity census and triggers cannot fall through to the public namespace; the fixture public
budget rows/counters stay zero. The final synthetic cardinality means under concurrent host work
were 6.15/6.32/6.31 ms at 1/1,000/9,000 buckets (p95 9.35/9.83/10.24 ms); 2,000 additional peers
retained 9,992 bytes after GC. Race/vet, portable-auth hashes, formatting and docs pass.
Exact combined CLI/source qualification, maintenance binding and human review remain coordinator
gates. Detailed commands and fixture-only setup corrections are in WORKLOG.
