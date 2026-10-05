# Engineering and supply-chain security

Native runtime: [ADR-0032](adr/0032-complete-native-go-backend.md). Child-safety invariants
remain in [SAFETY](SAFETY.md); product/legal launch gates remain separate from code landing.

## Dependencies and release artifacts

Go modules and checksums are pinned in `services/server`, `services/authcore` and the
optional `services/agentapi` module. The shared authentication snapshot has reviewed
MIT provenance and per-file hashes in `SOURCE.json`; `cmd/check-authcore` checks drift.
Node builds the hashed client once; compilers, Node, Python and test tooling stay outside
the native production image. Python requirement locks qualify only the offline oracle.

Dependabot opens review PRs for Go, image and action updates. There is no automatic merge,
and since [ADR-0033](adr/0033-manual-github-actions.md) those PRs get no automatic checks:
dispatch `native.yml`/`ci.yml` on the PR head (or run the local gates) before merging one.
The open Social Dependabot PRs stay unmerged (WORKLOG 2026-10-05, GOV-6): postgres-18 moves
`Dockerfile.db` off PostgreSQL16, node-26 leaves Node24 and django-6.0.8 shifts the pinned
oracle; closing them is the owner's action. `native.yml`, when dispatched, enforces
format/vet/race, actual isolated database/codec contracts, image construction,
source/imported-package govulncheck and linked-symbol analysis on a same-source
symbol-retaining audit binary. Stripping release symbols can make the analyser
fall back to conservative whole-module metadata; that output is recorded separately.

The fresh Trivy image gate checks fixable HIGH/CRITICAL findings (`ignore-unfixed`).
Current native receipts record zero such findings after compress1.18.7 and Debian
PCRE2 10.42-1+deb12u2 fixes. The unimported unmaintained x/crypto/openpgp module advisory
has no fix and remains disclosed; it is not imported/called in the serving programs.
This is not a claim that every future advisory or all unfixed findings are absent.

For a dependency change, update the exact owning Go module/checksum or image package,
refresh the canonical auth snapshot when applicable, then run affected native contracts
and the source/package/image gates. Offline Python updates still require their pinned
pip/Ruff/pytest/migration gates. Keep the two runtime boundaries clear.

## Application baseline

- Secrets come through fleet SOPS delivery or approved private operator stdin. The executable
  does not load `.env` or credential/service files automatically. Values never enter docs,
  source, commands, logs or auth-store dumps; errors identify setting names only.
- Canonical HTTPS origin/allowed hosts are explicit. XFF/XFP are accepted only from configured
  ingress CIDRs; untrusted headers cannot select peer/protocol or new rate-limit identities.
  Native redirects/HSTS/cookies/CSRF and privacy-safe operational headers follow CLI configuration.
- Sessions/tokens/provider flows are revocable and reload identity state. OAuth uses browser-bound
  single-use state/PKCE/nonce and stable provider subjects; email is never an account-link key.
  Login grants no age, cohort, guardian or consent assurance.
- EUDI/guardian, cohort, block, sanctions and ownership remain fresh native domain gates.
  Curated administration cannot edit raw identity/consent/ciphertext/scanner/payment state.
  Administrator bootstrap creates a fresh unknown/unverified account and logs no password.
- JSON/forms/uploads, query/path lengths, provider responses, buffers and codec concurrency
  have explicit bounds. Values are parameterized in SQL; identifiers use reviewed allowlists.
- Private images/video/documents require effective clean scanning, re-encoding/metadata stripping,
  native codec limits, owner-verified private EU storage and current serving authorization.
  Failed uploads/erasure reach durable blob cleanup; queued video stays withheld until ready.
- Structured logs contain route patterns/status/duration/request ID, without raw private paths,
  query strings, identities, peers, cookies, headers or bodies. CSP reports retain only aggregates.
- The release runs UID10001 with read-only root filesystem, dropped capabilities and private
  bounded writable media scratch. Database extensions are preinstalled by a privileged operator;
  the serving database role must be least-privilege.

## Release review

Landing follows [ADR-0040](adr/0040-landing-and-deployment-review-gates.md): local native gates
green on the exact head plus an independent reviewer; a dispatched hosted run is optional evidence.
Release additionally requires dependency/image scans, disposable real-FK PostgreSQL and actual
codec tests, backup/restore rehearsal and production configuration review. The owner authorized
the Go main landing on2026-10-04 only; human auth/erasure/privacy/safety code review is pending
and gates first deployment ([RELEASE_READINESS](RELEASE_READINESS.md)). Landing does not
activate providers/minors, ingest real data, procure infrastructure or satisfy the product GDPR/DPIA/parental-authority launch gates.
Exact commands and operational restrictions: [agent-testing](agent-testing.md),
[native CLI](../services/server/cmd/social-server/README.md) and [NATIVE_SERVER](NATIVE_SERVER.md).
