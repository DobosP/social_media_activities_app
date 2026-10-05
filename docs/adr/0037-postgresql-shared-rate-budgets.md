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

## Review follow-up — fixed-window expiry and erasure lock order

The independent review reproduced an inherited accounts/safety fixed-window limiter
race. Global expiry DELETE retained budget-row locks while its later INSERT requested
an account FK lock; account erasure held the account first and cascaded to the same
budget rows. A synthetic two-transaction reproduction produced PostgreSQL 40P01,
and the actual-Erase regression also fails with the old source via a scratch-only
overlay. This was not introduced by the shared sliding store.

Fixed-window expiry now runs in a separately committed, two-second sweep of at most
256 rows, ordered by until/user/action and using FOR UPDATE SKIP LOCKED. Admission
then takes account FOR KEY SHARE before budget-row mutation. Expired actor buckets
that remain beyond the bounded sweep reset count and until atomically in UPSERT;
live increments/denials retain their original expiry and configured cap. Existing
Config.Now fixed-window clock behavior remains. Missing database/actor fails closed.

The inline unsafe-report path performs the same independent sweep, locks its reporter
account before domain/budget rows and resets only its expired bucket. Repeated taps
remain free, and report/budget/audit/guardian notifications retain the original atomic
outer transaction. It never calls independently committing admission from inside that
transaction. No new scheduler, provider or identity authority is introduced.

Focused race fixtures cover actual account erasure and admission with a two-connection
pool, bounded expiry and skipped locked rows, leftover expired actor reset, integer
saturation and concurrent quota admission. Commands/results are recorded in WORKLOG;
combined candidate qualification and required human review remain separate gates.

The targeted review-fix receipt has 19 account and 20 safety fixture tests passing
under race instrumentation, zero skips/failures. The additional account unavailable-
state unit check passes in the final affected-package race run. The old-source overlay
fails the same real-Erase regression with SQLSTATE 40P01; new source succeeds without
swallowing errors. Vet, portable auth hashes, gofmt/whitespace and doc gates pass.
No production or provider verification is claimed, and sliding admission SQL is unchanged.

2026-10-05: anonymous API budget keys are per IPv4 address or IPv6 /64 (`platform.PeerKey`;
[ADR-0039](0039-native-source-login-failure-counter.md) per-peer admission).

## 2026-10-05 — Capacity families, eviction, pre-filter and off-path sweep

Owner rule (Paul, 2026-10-05): "never refuse new users because a table is full." The single
10,000-key/1,000,000-event capacity let minted anonymous keys refuse every new key in every scope,
including logged-in users' domain actions and new users' first reports (GO-CATALOG-01).

Capacity is now per scope family (`go_rate_budget_family`, schema version
`go-shared-rate-budgets-v2`): rows with an account are `actor` (50,000 keys / 1,000,000 events);
`api.anonymous` and `api.token` peers are `anonymous` (10,000 / 200,000); `ops.*` is `ops`
(100 / 10,000); anything else is `other` (1,000 / 100,000). Statement-level transition-table
triggers keep the per-family counters exact, locking rows before counters and counters in family
order. A `TRUNCATE` zeroes every family counter. Each migration locks the histories, re-applies the
reviewed limits and rebuilds the counters by census; a family found above its limit when adopting
v1 keeps its latest-expiring keys. The v1 singleton row is kept but no longer maintained.

"Saturation refuses more work" is replaced by eviction: when a key's family is full, admission
deletes up to 64 of that family's soonest-expiring keys (expired first, never another family's,
never its own) and retries once. It refuses only when nothing is evictable because concurrent
admissions hold every other key. An evicted bucket loses its history: a bounded fail-open on the
oldest keys of the full family only. Changed-policy refusal (above) is unchanged.

A per-process, denial-only pre-filter (65,536 direct-mapped slots, 64 shards) answers a throttled
anonymous or token peer from memory until its Retry-After passes. Keys are hashed in memory with
a per-process seed and never stored or logged; collisions overwrite. Allowed requests always reach
PostgreSQL, which stays the authority: a replica can only repeat a denial the database made.

CSP ingress is a per-process 120/min fixed window again, so an unauthenticated report flood never
touches the database pool. The inventory's "Ops `csp_report`" shared-sliding row no longer applies;
the 8 KiB body and 200 sanitized rows remain local bounds. Expiry sweeping is periodic, not per
admission: the existing `expire_api_tokens` pass plus one live-process sweeper (one 1,000-row batch
per minute, stopped with the server context; one-shot jobs start none). Admission makes one
database round trip and never depends on the sweep, since an expired row reads as empty.
