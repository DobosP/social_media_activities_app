# Native recommendations and opt-in alerts

Declared taxonomy interests and existing joined activity types feed the source-compatible
64-dimensional feature hash. Empty signal remains an honest soonest-first cold start. Warm ranks
use pgvector cosine distance, with bounded request-only proximity/access/topic nudges. Every
candidate first passes the cohort, hidden-state and mutual-block gates. Django's actual two-EXISTS
membership exclusion is preserved, including pending requests.

The host wires Social.AfterActivitySave to RecomputeEmbeddingTx so a new activity is ranked as
soon as its transaction commits. RecomputeEmbeddings backfills in fixed batches of five hundred,
reusing one vector per type and one grouped upsert per batch.

Owner-scoped saved searches retain the original route and serializer contracts. Matching uses a
durable user/object ledger before the shared preference-aware notification choke point: muting
and deleting/recreating a search cannot replay a notice. Current participation, pinned cohort,
blocking, secondary types, city, beginners/cost and local-time windows are rechecked. Gauges match
only predicates they actually carry. Synthetic database tests retain actual baseline foreign keys.

The host owns classic form bridges, scheduling, deployment policy overrides and the original
30/hour guardian-topic setting throttle. SetWardTopics locks an active guardian link and CHILD
ward before swapping known top-level topic preferences in one transaction.
