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
- [x] Independent seven-path source review approves152e095d; root commitsd2e122d with all268 original Go test files unchanged.
- [x] Exactd2 source packet passes fresh roedu-ui@acc49e59 StageA23:81 pure names/30 actual Pongo cases,18 collector controls, two required-DSN refusals and vets (independent review61c643e4).
- [x] Same-SHA direct PG bridge passes native15/host35: full migration,7 positive lifecycles, labelled parent-view negative, source/packet checks and isolated resource cleanup (independent reviewab8eb3bb).
- [ ] Root reviews and publishes metadata successor; owning Go1.27.1/full, capture and corrected-core-oracle adoption remain separate.

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
The seven-path source batch atd2e122d is independently source-reviewed. The actual
view checks both query errors, decodes every row, enriches accepted avatars, then
assigns all three context arrays. No service/SQL/template/auth/route/dependency or
old-test byte changes are present. Its exact fixed575-file packet is qualified
through the pinned Go1.27.2 core bridge for pure/rendering and selected live PG.
This does not qualify the app's owning Go1.27.1/full gates, native capture, appmain
landing, production or corrected-core-oracle adoption. Historical receipts stay exact.

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
The shapes above describe the source-stage plan. Actual bridge selectors and
outcomes are retained in the exact coreacc49e59 proof; no app-owning run is claimed.

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

Source freeze152e095d is approved by source review8052f3f9001fe741341b13c5d82f8506ebf4dac8750719e2048b1d96370b20c0;
StageA actual review is61c643e4baec2a036680056fc3d95178e85ac17c59a4dd6f3fc970371503e341.
Selected PG actual review isab8eb3bb47503e348284358814b0a89246a231a3cea08f840da5f06af3d22077.
Its closed132-payload-file/9447582B proof has manifestc91d4cee911e207d0c2e4212b1b9bf21f6b258e8bd5ebbb2e4c6b72b53f02a18,
host result78ae348c55aa650f296acec54c5eda4378d1b491e12499b16d2194dbe3847bf3
and native result669188ae5cacf1b7ceabe62b8baac4f56849cd12effc01987bb50a6b9f398166.
Core task scratch retains these separate actual records. All are scoped to
appd2e122d/coreacc49e59, not this documentation successor. The original fixed packet
was not recollected. Native Go builds and testing.TempDir use a fresh held exec
temporary root, which is empty and removed; the disposable DB/native pair/network
are removed with independent exact-label absence confirmation393a3e96.

## 12. Interfaces and Dependencies
Keep Social Service signatures []json.RawMessage and routeURL unchanged. Maps
use existing lowercase keys consumed by Pongo, not exported struct field aliases.
Bound each list to its actual SQL LIMIT200 and each row to8KiB: the schema has
UUID36, display_name120/username150 Unicode characters, bigint19digits, status12
and one timestamp; two nested projected users fit below8KiB even fully JSON-escaped.
The helper is private and rejects unexpected shapes rather than truncating them.
No public codec, Raw capability, SQL/authorization change or coverage exception.
