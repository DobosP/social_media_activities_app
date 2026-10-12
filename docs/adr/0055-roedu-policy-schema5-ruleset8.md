# ADR-0055 — The RO-EDU social consumer pins producer policy schema 5 / ruleset 8

Date: 2026-10-11
Status: accepted (owner directive 2026-10-11, "move everything to policy version 8"); landing per ADR-0040
Supersedes: the policy-tuple clause of [ADR-0024](0024-canonical-roedu-social-app-pack.md) (schema 4/ruleset 6).

## Decision

Accept exactly one producer policy tuple on every canonical social app-pack item: attestation
`schema_version` 5, `ruleset_version` 8 and `ruleset_hash`
`140290d4bc6374ebb5b7922dee98c79bee5501a6cf76e1ef18923a8647068fd1`. That hash is the producer's
own value, the SHA-256 of the canonical JSON of `_RULESET_MANIFEST` in
`romania_scraper/policy/models.py`. Every other tuple fails closed: the previous social pin 4/6, the
previous serving and Teacher pin 5/7, and any half-moved combination. The native Go verifier
(`services/server/internal/jobs/roedu.go`) and the legacy Python oracle
(`apps/ingestion/sources/roedu_client.py`) both carry the pin.

Nothing else in the contract changes. The rules below stay exactly as ADR-0024 set them:
- the exact 15-key path-free attestation;
- the exact venue, event and tombstone field sets;
- `effect == "allow"` with reasons exactly `["explicitly_cleared"]`;
- the closed obligation set;
- `privacy_classification == "no_personal_data"` and `gdpr_relevant == false`;
- capture and acquisition schema 3.

## Context / why

Ruleset 8 (romania_scraper `bdadeb7b`, its ADR-0366) changes what the producer clears. Publication
now asks for a lawful basis, not the absence of personal data. The rights tag gains `lawful_basis`,
`lawful_basis_reason` and `lawful_basis_ref`. `personal_data_release_permitted` clears none or
incidental personal data on its own. It clears PERSONAL data only against a recorded Art. 6/89
basis. It never clears Art. 9/10 data, a CNP or contact PII. The rights classifier moves to
version 8.

None of this reaches this consumer as data it re-checks. The serving layer strips the rights tag
and the full decision, and the social projection has no lawful-basis field. The consumer replays
no rights evaluation. It checks the attestation's shape, tuple and bindings, plus the item's own
privacy fields. The document-level evaluator still denies `privacy_personal_data` at ruleset 8, so
a 5/8 allow still arrives with `privacy_classification == "no_personal_data"`. The consumer's
existing gates are therefore at least as strict as ruleset 8 for this pack. The only semantics to
port is the tuple itself.

This was verified, not assumed. The fixtures were regenerated through the real chain by
`services/server/internal/jobs/testdata/generate_roedu_fixtures.py`:
1. producer classifier 8 and the 5/8 evaluator;
2. producer `materialize_social_items`;
3. ro_data_server `_public_pack_view`;
4. this repo's legacy oracle for each golden row's `valid`.

The generated attestation has the same 15 keys as `exactKeys`. The item key sets equal
`commonFields` plus the per-kind fields, with reasons `["explicitly_cleared"]` and no obligations.

The real chain differs from the old hand-written fixtures in two consumer-facing values. The test
expectations now follow what is actually served:
- **License.** The producer emits `license = rights.license_id or legal_basis`. A statutory
  `public_document` has no licence id, so the item says `Law 8/1996 art. 9`, not
  `RO-LAW-8-1996-ART-9`.
- **Ticket host.** ro_data_server's public-URL gate withholds a reserved `.test` host. The ticket
  URL host is now `tickets.example.org`.

Rejected: accepting 5/7 and 5/8 side by side during the rollout. ADR-0024 makes tuple drift the
signal that the consumer contract needs re-review. A second accepted tuple would admit items cleared
under the superseded rules and leave no point at which the old tuple stops being trusted.

## Consequences

Against a server still serving 5/7 items, every item is refused. The read is reported as a refused
product (ADR-0030), never as an empty city. This change must therefore land together with the
ro_data_server and ro_teacher moves to 5/8 and a re-promoted 5/8 release. Scheduled sync stays off,
as before.

The next producer policy move repeats this ADR's steps:
1. update both pins;
2. re-run the generator;
3. re-check the 15 attestation keys and the item key sets against the generated items.

Never edit the policy-bound fixture fields by hand.
