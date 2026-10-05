# Native booking contract

The adapter registry retains the deep-link baseline and demo REST provider, with
`NewConfigured` accepting native overrides and configured demo endpoint/auth.
REST creation/cancellation do not replay ambiguous responses, credentials cannot
follow redirects, and provider identifiers remain opaque without float rounding.

Creation reloads account eligibility within its transaction. An activity booking
requires current membership and its cohort wall; the existing CHILD supervisory
guardian exception also requires a current adult guardian, active relationship,
eligible CHILD ward membership, assurance and unexpired parental consent. A stale
guardian seat alone grants no access. Review gates for these auth/privacy rules:
[ADR-0040](../../../../docs/adr/0040-landing-and-deployment-review-gates.md) (independent review before landing; human review before first deployment).

Cancellation locks the owned receipt before provider work, so concurrent repeated
requests cannot cancel externally twice. A provider failure persists a failed
receipt and returns a clean error. Deep links remain pending without fabricated
provider confirmation. Lists retain the original DRF limit/offset shape (50 by
default, 200 maximum), total count, and next/previous links.

Integration tests require the explicitly supplied `-domain-test-dsn`; each test
clones and drops only its own fixture schema. The suite covers ownership, fresh
eligibility, guardian withdrawal, pagination, concurrent cancellation, unsafe
provider redirects, numeric references, and local-time/default serializer parity.
