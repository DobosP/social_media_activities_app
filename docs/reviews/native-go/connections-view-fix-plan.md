Branch: fix/gui-social-connections-view
Session: /root/social_capture_go_review
Worktree: /home/dobo/work/_worktrees/social_media_activities_app/fix__gui-social-connections-view
Base: 5426e9154a67d5b3c3769a2a9f53c4197e690174

## 1. Purpose / Big Picture
Repair populated legacy Connections HTML in the real app view. Actual service
JSON rows become complete detached template maps with exact int64 pending IDs
and the source template's pk alias. SQL, services, safety and templates stay exact.

## 2. Progress
- [x] Root authorizes isolated real-app engineering correction, no oracle adoption.
- [x] Root creates exact5426 lane; read mandatory app instructions/status/guides.
- [x] Record exact seven-path ownership before product code.
- [x] Root approves bounded source/validation plan before product authoring.
- [x] Root widens the same seven paths to complete accepted-user avatars through
  the existing authorized-ID accounts.Avatars list helper before further code.
- [x] Author strict complete projection and exactint64 alias, bounded actual-avatar enrichment, four pure/native-renderer tops and two required-DSN taggedPG tops.
- [x] Source-only lexical/whitespace/docs checks and exact seven-path freeze prepared.
- [ ] Independent source review before native execution.

## 3. Surprises & Discoveries
All38 source inputs frozen in core still match this app HEAD. The real view passes
[]json.RawMessage directly to Pongo, which errors on named fields of byte slices.
Decoding alone is insufficient: pending templates use c.pk while SQL emits id.
Existing accountDecodeRows silently drops errors and float-converts IDs; it is
unsuitable. Global socialModel also changes dates/labels beyond this repair.

## 4. Decision Log
2026-10-16 UTC: Root approves preparing this source fix in an isolated app lane.
The fixed core v1 corpus/native30 and failed diagnostics remain immutable.
2026-10-16 UTC: Root authorizes source-owned accepted-user avatar enrichment.
Resolve only the exact public IDs returned by Service.Connections, then invoke
existing accounts.Avatars; zero queries for empty and three queries for nonempty.
Require complete unique ID/result joins and preserve order; expose no internal ID.
Actual accounts_user primary key and signatureavatar.user_id unique constraint
prevent duplicate helper rows; validate the returned map key set/cardinality.
Duplicate map keys already coalesced by the unchanged helper are not observable,
so no new claim of detecting them is made.
Do not alter the avatar algorithm, global filter, SQL service or safety resolver.
Adopting a separately corrected v2 core oracle remains a separate policy decision.
This is an existing view bug fix, not a new product/safety architecture decision.

## 5. Outcomes & Retrospective
The seven-path source batch is authored and awaits independent review. The actual
view checks both query errors, decodes every row, enriches accepted avatars, then
assigns all three context arrays. No service/SQL/template/auth/route/dependency or
old-test byte changes are present. Native tests are unexecuted; no test success,
commit, push, appmain landing or corrected-core-oracle adoption is claimed.

## 6. Context and Orientation
Read AGENTS, STATUS, agent-map/testing, SAFETY Core rules, ARCHITECTURE Working
conventions and FEATURES_BUILT Connections before source work. Domain service
Connections/PendingConnections own cohort/block/query behavior; the web view owns
ordinary rendering representation. Exact paths are in task scratch owned-paths.json.
Existing tests, templates, source packets, SQL, dependencies and auth stay immutable.

## 7. Plan of Work
Add private complete decoders for accepted-user and pending-connection rows.
The actual view checks both service errors, decodes all three lists completely,
then assigns data. Preserve order/empty lists/all projected SQL fields; add only
pending pk=int64(id). Reject malformed/duplicate/unknown/missing/null/wrong-type
rows, invalid string encodings, trailing values and noncanonical/overflow IDs.
No row skipping, float conversion or generic renderer decode. After all decoding,
enrich accepted users only with existing source-owned avatar_uri before assigning
any context. Fixed public-ID lookup selects only internalID/publicID for the
authorized users; those IDs never enter template maps. Check exact cardinality,
unique IDs, missing/extra matches and nonempty safe avatar URIs, propagating errors.

## 8. Concrete Steps
Author only the seven approved paths in this lane. Scratch lives under
/home/dobo/work/_temp/fix__gui-social-connections-view. Native Go parsing/build/
format/tests execute only through root's later pinned wrapper, never on the host.
Planned pure command: go test -race -count=1 -timeout=300s -run '^TestConnectionsContext' ./internal/web.
Planned PG command: go test -race -count=1 -timeout=300s -tags=connections_view_pg -run '^TestConnectionsPage' ./internal/web -args -web-domain-test-dsn=<explicit disposable fixture>.
Root supplies exact validated wrapper/image/SHA and fixtures; these are not run now.

## 9. Validation and Acceptance
Pure controls bind exact complete maps, detached bytes/order and typed IDs1,
9007199254740993 andMaxInt64; reject overflow/fractions/exponents/negative/zero,
strings-as-IDs, duplicates, nulls, wrong types, trailing JSON and malformed Unicode.
Actual native Renderer tests run unchanged source templates in en/ro for empty,
accepted/incoming/outgoing/mixed rows, escaped names and exact form actions.
Tagged PG tests require the explicit existing web fixture DSN with t.Fatal before
calling testdb when absent, adding no skip. Use real Service queries/view/HTTP
rendering and existing synthetic fixtures; verify source row/context joins,
PWA-independent CSP/CSRF, same-cohort/block/self exclusions, exact high-ID routes
and action parameter parsing. Accepted users must receive the exact existing
base avatar and a nonempty safe image URI; excluded users never enter avatar
lookup. Query-count controls require empty0/nonempty3 regardless of row count.
Preserve existing action/authorization tests.
No success is accepted without native proof and independent review; ordinary
hermetic skipped DB tests cannot qualify the new behavior.

## 10. Idempotence and Recovery
All fixture data is synthetic and disposable. Preserve production/main, original
core provenance and failed diagnostics. Source stage only; parent owns runtime
cleanup, commit/publication and any later adoption. No host product execution.

## 11. Artifacts and Notes
Architecture2384059548c78d2dd5564929edc8d18916228ecdc7c3043409f665aaf8b1380f
and diagnostic audit1685713d71b0496edf1ebdb7d137274ae6b4d926d98f6861c7f33e3848f9f0ba
are in the core Social capture task scratch. The original source diagnostics are
not copied or relabelled as successful app behavior. No artifacts are pruned here.

## 12. Interfaces and Dependencies
Keep Social Service signatures []json.RawMessage and routeURL unchanged. Maps
use existing lowercase keys consumed by Pongo, not exported struct field aliases.
Bound each list to its actual SQL LIMIT200 and each row to8KiB: the schema has
UUID36, display_name120/username150 Unicode characters, bigint19digits, status12
and one timestamp; two nested projected users fit below8KiB even fully JSON-escaped.
The helper is private and rejects unexpected shapes rather than truncating them.
No public codec, Raw capability, SQL/authorization change or coverage exception.
