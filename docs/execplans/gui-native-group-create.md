# Native group creation: bounded implementation plan

Status: completed and independently feature-qualified; [accepted keeper](../reviews/gui-native-group-create/README.md) joins PG1f8c974 with presentationd05343b.

- Approved contract: `75301bc5ddc302d605cde0f20922e76709d97f3507eaad0bdf8493daa2590728`.
- Independent contract review: `bafda78cf942e411330f19e08caca3b2ff246be9857f3474ef761dc3ce2fbbea`.
- Decision: [ADR-0062](../adr/0062-native-group-create.md).
- Reproduction: [committed native group methods](../../tools/gui-native-group/README.md), with separate actual PG producer and UI-only method identities.
- Source preparation base: `595d39c9d538c51bc18c684b801ac0ef32a1a1ef`.

1. Close the current native family feature and independently review this packet.
2. Rebind/rebase the packet onto the actual closed source; preserve all original
   templates/tests/assertions, family product bytes, and source-inverse history.
3. Apply only the selected form DTO/Go template/renderer dispatch and additive tests.
   Format the new source, then run focused pure/DB tests, current custody and vet.
4. Qualify the two new real App roots with actual cookies/CSRF/Origin, configured
   policy and current-account refusal; require real group/member/thread/audit
   success and mandatory-audit rollback. Preserve separate rate reservations.
5. Run the existing group creation/prefill/policy/service tests without assertion
   changes; run the full affected package lanes using committed methods and fresh
   synthetic fixtures. No skip/filter result substitutes for package qualification.
6. Export actual EN/RO native fixtures for keyboard/no-JS/Axe/assets/CSP/error help;
   repeat the existing eight family fixtures and require byte equality. Measure
   warmed original/native same-projection body render and allocations honestly.
7. Bind actual source/tool/result/cleanup hashes, obtain independent review, and
   update STATUS with only completed facts. Keep formal Social landing/milestone,
   production, providers and real-child activation outside this slice.

No formatter, compiler, Go parser, tests, browser, service or DB ran during packet
preparation. Source-only review is not functional qualification or a speed claim.

Completed: all seven steps, exact PG6 and later scoped presentation checks as separately
bound in the keeper. Initial source-guard/contrast failures and unqualified DSN skips
remain explicit; no later complete PG, main, milestone or deployment claim.
