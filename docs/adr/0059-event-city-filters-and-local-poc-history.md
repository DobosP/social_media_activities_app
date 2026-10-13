# ADR-0059 — Event city filters and local POC history

Date: 2026-10-13
Status: accepted; owner-requested continuation, independently reviewed before landing
Extends: [ADR-0057](0057-roedu-local-poc-consumer.md).

## Context

The public event list discarded `?city=` although its query already had a city
argument. POC compatibility mode (`include_past=1`) sorts upcoming before past;
100 upcoming rows can therefore consume the entire list before any history appears.

## Decision

- The native event list passes its trimmed city filter. One consumer-owned comparison
  normalizes NFC, case and surrounding Unicode whitespace, folds Romanian diacritics
  including legacy cedillas, and recognizes `Bucharest` as `Bucuresti`. It is exact
  after those operations: unknown names, counties and different places never become
  fuzzy matches. Stored city facts, source evidence, venue IDs and provenance remain
  unchanged; producer internals are not imported.
- Catalog event/place filters, public place/event/area comparisons and related-city
  links share that comparison. City composes by intersection with activity, area and
  free text; an unknown city returns no rows. Existing unknown-area behavior stays.
- Add `past_only=1` only behind the existing development/loopback local POC gate.
  It lists past event-level POC rows, newest first, at the existing limit of 100.
  Production rows and the existing held, tombstone, lifecycle and public-place vetoes
  remain enforced. A POC venue alone does not admit a past production event.
- Default upcoming behavior and `include_past=1` compatibility remain. Past-only
  takes precedence if both flags are supplied. Query flags never enable the POC gate;
  the existing CLI refusal of simultaneous React and POC configuration remains.
- Mode links preserve city, free text, activity and area. Forms preserve the active
  mode. Clear removes discovery filters while keeping that mode. RSS/Atom keep
  city/activity/area, ignore free text and history, and stay upcoming-only. The
  sitemap publication adapter continues to clear every request filter.
- City/history results use filtered `noindex, follow` metadata and JSON-LD over
  exactly the visible result set. SPA projections and snapshots retain POC/past
  labels; a keyed form refreshes uncontrolled inputs after soft navigation.
  Shared templates retain named-route defaults for the offline Python context.

## Verification and boundaries

Registered-route synthetic PostgreSQL cases cover NFC/NFD, cedilla/ASCII/English
aliases, whitespace, unknown/incompatible cities, combined filters, API/feed and
SSR/SPA/JSON-LD parity. More than 100 upcoming rows cannot hide a past POC event in
past-only mode; past production, held, cancelled, tombstoned and unpublished-venue
rows remain excluded. Controlled test-only overlays restore each bug and must fail.

Landing needs native source checks, relevant frontend tests/build, the full 21-lane
PostgreSQL/codec qualification and independent review under ADR-0040. Source landing
does not switch the running POC, deploy, activate ingestion or authorize production.

Exact source `f412dfc` completed all21 lanes with740 passes,0 skips/failures,
plus native/frontend/focused checks. The [checkpoint](../reviews/native-go/local-poc-city-filters-checkpoint-20261013.json)
records source-object equality for receipt-only closure and disposable cleanup.
