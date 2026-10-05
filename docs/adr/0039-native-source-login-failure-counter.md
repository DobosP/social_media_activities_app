# ADR-0039 — Native source login failure counter

Date: 2026-10-05
Status: proposed; source restoration qualified, landing per ADR-0040; human auth/privacy review gates first deployment
Extends: [ADR-0038](0038-native-verification-toolchain.md).

## Decision

Normal browser/API password login and session-free restricted-account credential proof use
one application-owned durable failure counter. Its key is purpose-separated HMAC over the
canonical lowercase username and normalized trusted client IP. Only the app's existing proxy
boundary interprets forwarded headers; source TCP ports and IPv4-mapped spelling cannot create
new pairs. Raw usernames/IPs are neither stored nor logged by this counter.

Restore the original source defaults: ten failed credentials, a fixed fifteen-minute window
seeded by the first failure, and successful credential verification clearing that pair. Successful
attempts do not spend the failure budget. Programmatic Config fields retain source test/operator
equivalence; this change introduces no unregistered environment variable names.

Short-lived durable reservations bound concurrent password work across service instances.
Pending slots count toward capacity even across epoch rotation. Each completion releases its
own slot; an old epoch cannot count into or clear a new failure window. Expired reservations or
database/finalization failures fail closed. Cancellation cannot suppress a known failure or leave
an unbounded live reservation. Storage and cleanup are bounded.

The application wrapper delegates password verification, current active-account checks and
session/cookie issuance to the unchanged hash-pinned authentication library. Only its private,
single-use, database-checked reservation context bypasses that library's unrelated total-attempt
limiter. Ordinary unreserved callers retain the existing guard. The browser retains HTML200
invalid/lockout responses and302 success; JSON login keeps its existing wire/cookie protocol.

Restricted proof is not a login session. It reads only the verified subject's current reasons
and permits only the bound account/action appeal. Opaque capability hashes expire after thirty
minutes and valid submission consumes them. Correctable empty/oversize statements retain the
same current owned remedy; invalid, replayed, expired or different-action proofs cannot broaden
scope. Exact bigint action IDs, decision dates/lifetime and appeal status survive projection.

## Context and consequences

The prior native store charged all attempts for sixty seconds; restricted proof also included
the ephemeral source port and a separate namespace. These did not implement the original
failed-only shared lockout. Preserving those differences as framework equivalence would hide
both missing behavior and an admission bypass.

Independent PostgreSQL/race tests cover exact first-failure expiry, replica state, normalized
pairs, success/cancellation/infrastructure outcomes, concurrent slot capacity, old epochs,
private context scope, expired/failing finalization, repeated real sessions, assembled trusted
proxy routing and the actual restricted remedy. Qualification does not activate authentication
providers/minors or production delivery. Current receipts and incomplete source-case coverage
remain in STATUS/WORKLOG; the complete migration is not claimed until retirement evidence passes.

## 2026-10-05 — Credential binding correction

Independent review found that the wrapper admitted a case-sensitive JSON map key while the
pinned verifier accepted struct-field case aliases. It also admitted an untrimmed browser
username while the verifier received its trimmed value. Both could split the failed-only
pair from the account actually checked. The API now decodes exact allowed keys once,
rejects repeated or case-aliased keys, and delegates canonical credential JSON from the same
normalized username used for admission. Ignored pinned email/name input fields are validated
but omitted from that credential body; password and username case remain unchanged.
Normal login, the failure-key helper and restricted proof share source whitespace cleaning.

Actual pinned-library/in-memory lookup proof, PostgreSQL counter/replica/session tests and
assembled trusted-proxy application tests qualify the correction. The original independent
API discovery overlay hardcodes the former parser and forwards raw JSON directly to the
unchanged library; it remains a negative control, not a test of the repaired wrapper.
The unchanged browser-padding overlay passes. Pinned authentication source hashes remain
verified. Human review and complete source-case retirement gates still precede landing.
