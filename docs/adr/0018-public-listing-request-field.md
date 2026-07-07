# ADR-0018: Public Listing Request Field

Date: 2026-07-07
Status: accepted

## Decision
Use `listed` as the canonical request field for the activity and group `set_public_listing` API actions. Keep `is_publicly_listed` as a legacy alias only when `listed` is absent, and reject requests that send both fields.

## Context / why
The response serializer exposes `is_publicly_listed`, but mutation requests need a compact verb-shaped input that describes the requested action. The recovered view accepted both fields without a documented contract, leaving missing, malformed, or ambiguous payload behavior under-specified.

## Consequences
Existing legacy clients that send only `is_publicly_listed` keep working. New clients should send `listed`. Requests that send both fields must be corrected instead of relying on server-side precedence.
