# ADR-0023: Reconcile RO-EDU events against immutable source snapshots

Date: 2026-07-12
Status: accepted

## Decision

Retain RO-EDU's source category, confidence, lifecycle, venue identifier, source timestamps, and
immutable pack/release/snapshot identity on `Event`; map the stable category to an active local
`ActivityType` before falling back to title classification. Treat explicit cancelled/postponed/
removed states as source truth while keeping `EventReport` as a separate member-accuracy overlay.
Low-confidence imports remain reviewable in admin but are excluded from every public event read
surface. Apply body-less tombstones in product or delta mode, but infer absence only after an
unbounded, well-formed app-pack read whose every page carries one consistent `snapshot_id`,
`release_id`, `snapshot_generated_at`, `snapshot_mode=full`, and `snapshot_complete=true`.
Reconcile that complete snapshot and its checkpoint in one database transaction. Reject an older
snapshot timestamp or a different snapshot at the same timestamp unless the operator supplies the
explicit `--allow-snapshot-rollback` override. A scheduled run uses either the configured app pack
for both places and events or legacy products for both; it never mixes delivery modes.

## Context / why

The producer already knew event category and lifecycle, but the consumer discarded both and
always upserted an apparently scheduled, untyped event. It also discarded the upstream venue ID,
so an event-only delta could lose its stable place link. A disappeared or cancelled event could
therefore remain in discovery indefinitely. Inferring deletion from any fetched page was rejected:
license withholding, a limit, a malformed item, cursor drift, or a delta page would all create
false cancellations. Hash-only snapshot IDs were also insufficient for replay protection because
hashes have identity but no ordering; the promoted manifest's generation timestamp supplies the
minimum ordering evidence, while rollback remains an explicit operator act.

## Consequences

Imported cancellations, holds, and tombstones no longer appear as upcoming happenings; cancelled
detail pages remain honest and lose organise/report/share calls to action. A complete immutable
snapshot deterministically retracts only absent events from the same `(pack_id, city)` scope, while
partial, legacy, and delta runs never infer absence. The sync stores no copyrighted event prose and
does not change child-venue approval, cohort, consent, authentication, or privacy gates. A future
producer/consumer slice may project additional safe ticket, price, recurrence, and availability
facts; this decision does not authorize those fields without matching schema and rendering tests.
