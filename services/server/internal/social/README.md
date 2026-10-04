# Native social domain

This package replaces domain calls with SQL against the existing schema. It contains no Python
subprocess, Django request proxy or unrestricted table CRUD. The reference contracts are
[`apps/social/services.py`](../../../../apps/social/services.py),
[`apps/social/views.py`](../../../../apps/social/views.py),
[`apps/connections/profiles.py`](../../../../apps/connections/profiles.py) and their tests.

`New(db, platform.RecordAudit)` constructs the service. `Register(mux)` installs the canonical
`/api/v1/` routes and compatibility `/api/` aliases. The host supplies authenticated actors using
`platform.WithActor`, the signing key through `Service.Cursor`, and reviewed avatar/media callbacks.
`Notify` delegates to the one shared preference-aware notification service. No environment is
discovered by this package.

## Implemented contracts

| Surface | Native behavior |
| --- | --- |
| Activities | Cohort/block/hidden filtering; create, immutable-pin-safe edit, cancel, move, adult public listing; primary/secondary taxonomy and exact cost facts. |
| Membership | Request, leave/reset signals, two-thirds voting, self-vote refusal, organizer override, capacity serialization, current eligibility recheck, adult co-organizer grant/revoke/ownership transfer. |
| Guardians | Active-link proxy participation, child-approved public venues, strictest intersected weekday/hour/category/cap guardrails, registered adult supervisor seat, deferred vote settlement, immutable thread cohort. |
| Threads | Private membership/participation/block gates; first-person posting, optional native message policy, safe shares, depth-one replies, opted-in peer mentions, author edit/delete and standing appeal/provenance protection; atomic post/attachment publication, audit and live delivery. |
| Sentiment writes | Fixed appreciation catalog, guardian veto, child dissent/concern veto, shared bounded reaction budget, audit without per-reaction broadcasts or counts. |
| Sentiment jobs | Daily/weekly latching, retention graduation, author/lifetime caps independent of muting, teen human-relay queue, protective pile-on and coordinated-flagging sensors, raw-row hard purge. |
| Groups | Staff/opt-in creation, minor creation policy flag, open join/leave, no scalar roster counts, adult member-only block/eligibility-filtered roster, minor announcement-only threads, fixed-enum staff questions. |
| Series | Owner-scoped create/pause/resume/end/note, pinned identity, one future instance, locked/idempotent spawning, bounded stale-slot advance, local-clock DST and monthly anchor preservation. |
| Connections/profiles | Shared-peer-activity prerequisite, cohort/block/consent gates, reciprocal acceptance, self-scoped response/withdraw/remove, query-only discovery, veto-first tiered cards and minor richness clamp. |
| Communities | Same-cohort cards/graphs, coordinate-filtered activities, native real-activity/day/peer floor materialization, stable collision-safe slugs and deactivate-not-delete reconciliation. |
| Gauges/proposals | Ephemeral functional signal, no interested-person roster/count field, locked conversion without copying membership; Unicode/difflib-compatible venue dedup and independent confirmation quorum. |
| Operations | Self-scoped organizer console, completion, presence/gauge retention, one-shot organizer/RSVP/supervisor nudges. |

Current native constructor policy keeps user-created groups and minor onboarding off until the host
configures the existing reviewed switches. Tests exercise minor domain rules with synthetic records;
they do not authorize minors or mutate live data. Privacy withdrawal remains possible after assurance
or consent expires. Missing audit, avatar or required visual adapters fail closed.

## Verification

`social_test.go` contains offline Django serializer/dedup goldens plus isolated PostgreSQL scenarios
for private reads, cohort/block vetoes, supervision, votes, concurrent capacity, audit rollback,
replies, author controls, minor groups, gauges and venue quorum. The fixture database is opt-in through
the `-social-test-dsn` test flag; ordinary unit runs skip database cases when it is absent. Each case
clones its own schema namespace through internal/testdb, retaining the actual baseline foreign
keys, columns, checks and unique/index constraints. Deployment qualification remains the host's
responsibility. The attached-post cases exercise scan/no-op/audit failures and prove that no partial
post, attachment or audit survives rollback.

## Outstanding parity inventory

This inventory is intentionally distinct from registered REST-route coverage:

- The host must connect exported social batch methods to its native job runner. All computations
  and updates described above live in Go; registering a REST route does not schedule any job.
- The legacy web renderer still needs its domain context adapters for thread digest, safe inline
  markup/mention highlighting, plain meetup brief and draft prefill. These are not Python fallbacks.
- The host must wire batch media rendering and generated signature avatars, its existing deployment
  settings and public origin, the Activity post-save embedding hook, and the full application tests.
  Native identifiers-only PostgreSQL live publication is inside post/edit transactions.
- Guardian preview is capped at the source limit of fifty visible candidates and reuses CanJoin;
  it is currently an opportunity for further per-request query batching, without changing the gate.
