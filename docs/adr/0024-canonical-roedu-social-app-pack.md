# ADR-0024: Consume one policy-bound RO-EDU social app pack

Date: 2026-07-12
Status: accepted

## Decision

Consume only `roedu:social_media_activities_app:events_places:v1` from the redistributable
serving layer. Reject short aliases, other apps, stale schemas, unknown fields, and product or
snapshot identity drift before they can affect application data. Require every served venue,
event, and body-less tombstone to match its exact facts-only schema and a path-free public policy
attestation for policy schema 4/ruleset 6 and acquisition schema 3. A read is complete only when
every page belongs to one immutable full snapshot, local pagination reaches its end without a
record cap, the producer reports no withholding/errors, item IDs are unique, and event-to-venue
relationships are intact. A live event must reference a served venue and that venue must resolve to
a local `Place`; otherwise no null-place event is created. Only a clean read may drive absence
reconciliation. `moved_online` is retained as source truth but excluded from in-person discovery.

Persist the safe event fields the product contract promises: category, lifecycle, stable venue,
first/last/update observations, recurrence, timezone, ticket URL, price range, currency, free/paid
flag, and availability. Use the existing event URL for the strictly validated ticket URL and new
bounded `source_*` columns for the remaining facts; expose those columns through the read-only
event API. Continue discarding scraped descriptions, organizers/performers, generic source URLs,
full policy/evidence/clearance/rights/acquisition payloads, internal paths, and checksums.

## Context / why

The former additive client accepted a caller-selected pack name and only repeated four generic
redistribution flags. That did not prove which product or schema had been read, whether policy and
acquisition evidence matched the content, or whether the generic serving sanitizer had silently
removed fields the app needed. It also allowed a syntactically complete snapshot to become unsafe
after client-side withholding without necessarily preventing absence inference. The national-data
architecture requires a real producer/server/consumer contract, not a permissive JSON dictionary.

## Consequences

Producer or server evolution is deliberately fail-closed until the consumer contract and tests are
updated together. Partial data may still upsert valid rows but can never retract prior events.
Strict relationship validation requires a normal event-sync read to include its referenced venues;
the separate venue stage may request only venues. The event model gains migration 0009 and the API
gains additive read-only fields. No imported venue becomes child-safe, no low-confidence event
becomes public, and no copyrighted event prose is admitted. Localized UI presentation and a staff
review workflow remain separate product work.
