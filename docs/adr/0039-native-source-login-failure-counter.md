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

## 2026-10-05 — Per-peer admission and removal of table-size refusal

Independent review (findings F1/GO-01, IDP-1, IDP-2) found that the reserved-login context
left browser login, JSON login and the restricted proof without any per-IP limit, while a
10,000-row global cap on the failure table, the shared attempt table and the OAuth flow table
refused every new client once one source filled it. Owner decision (Paul, 2026-10-05), verbatim:
*Django's rule (failed logins only, per username + IP, 15 minutes, cleared on success) plus a
per-IP cap on total attempts. Never refuse new users because a table is full.*

Peer identity is one normalizer, `platform.PeerKey`: an IPv4 address (/32) or an IPv6 /64;
ports, IPv4-mapped spelling and zones are ignored; unparsable input shares one bucket. The
failure-counter pair key, the new per-peer caps and anonymous API budgets all use it. Per-peer
rows are keyed by HMAC-SHA256 under the identity binding secret (purpose- and scope-separated;
plain SHA-256 only in fixtures without a secret). No raw IP or username is stored or logged.

Each scope is a fixed window per prefix that counts every attempt, successes included; the
failed-only username+IP counter above is unchanged and remains the per-account brake. Defaults,
confirmed by the owner on 2026-10-05 and overridable only through the existing programmatic
rate-policy hook (no new environment variable):

| Scope | Default | Applies to |
|---|---|---|
| `auth.login` | 100 / 15 min | `POST /login/` and `POST /api/auth/login`, shared |
| `auth.restricted` | 30 / 15 min | restricted-account credential proof, its own scope |
| `auth.signup` | 30 / hour | `POST /api/auth/signup` and `POST /register/` |
| `auth.oauth_start` | 30 / 15 min | `GET /api/auth/oauth/{provider}/start` |
| `auth.legacy` | 10 / min | pinned library used without an application marker |

This goes beyond Django parity: Django had no per-IP cap on browser login or signup. Login and
the restricted proof are admitted per prefix before the failure reservation and any password
work; an oversize password (over 1024 bytes) is rejected before any row exists, with the same
visible response the pinned verifier gave. At the cap, browser login shows a network-specific
message, JSON login returns 429 with `Retry-After`, and the restricted proof shows its existing
"too many attempts" text.

No admission is refused because of table size. Storage is bounded by the caps times the
prefixes active in each window, plus in-flight reservations. A failure row is still created at
reservation, now only after the cheap checks and per-peer admission; completion deletes it when
it holds no failures and no other live reservation, so successes, infrastructure errors and
aborted attempts leave no row. Reservation and completion serialize on a per-pair advisory lock
instead of one global lock and touch only their own pair. Cross-key expiry runs outside those
transactions in bounded, separately committed `SKIP LOCKED` sweeps, from the existing
`expire_api_tokens` job (no new job name) and opportunistically after one in sixteen admissions.
Correctness never depends on the sweep: expired windows reset inline.

The pinned library's attempt store receives only a digest of the raw host, so the application
marks the request context with the normalized prefix and scope (memory only) for signup, OAuth
start and API logout. API logout is exempt and never throttled; HTML logout was already
unthrottled. A call without the marker keeps the library's previous ten attempts per minute per
host digest, without any table-wide refusal. Pending OAuth flows are capped at ten live flows
per prefix through a new nullable `peer_hash` column on the flow table (rows live five minutes),
replacing the global cap. The token endpoint's duplicate charge, an unkeyed SHA-256 of the IP,
is removed; its `api.token` budget of ten per minute per peer is unchanged.

Known limits: signup at its cap returns the pinned library's fixed `Retry-After: 60`, which
understates a one-hour window, and the OAuth flow cap surfaces as its 503 "identity provider
unavailable"; neither can change without editing the hash-pinned library. Users behind one
CGNAT or school address share one bucket per scope. A misconfigured `TRUSTED_PROXY_CIDRS`
collapses every client into the proxy's bucket. PostgreSQL fixture tests cover full-table
admission, per-prefix and IPv6 /64 caps, scope separation, the logout exemption, row lifecycle,
the sweep and the pinned-route marker coverage. Review gates:
[ADR-0040](0040-landing-and-deployment-review-gates.md).
