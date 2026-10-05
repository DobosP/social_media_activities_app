# Worklog — social_media_activities_app

Append-only, newest first. Current truth is `STATUS.md`; this file holds the dated detail
`STATUS.md` summarizes.

## 2026-10-05 — Group chats survive blocks; sanctions evict chats (GO-PRIV-02, GO-PRIV-03)

Valid until: `fix/go-group-messaging-blocks` is integrated or superseded — then treat as history.

[ADR-0043](docs/adr/0043-direct-only-block-veto.md) (owner decisions 2026-10-05; design by the G2 critic).
`CanView` and the `/keys/` roster veto only direct chats on a block with an active peer; `canAdminister`
lets an active admin remove any member regardless of blocks (adding still uses `pair()`); `Post` validates
only the sender, and its exact recipient set is active participants with active accounts (the roster's
predicate), with a batched key insert; `TakeAction` suspend/timed ban/ban calls `RemoveUser` in the same
transaction (fails closed if messaging is not wired); `BlockHTTP` has a shared 30/h `block` budget;
`Start` re-invites a direct peer who left or was removed (a removed starter re-enters only while the peer is
inactive too). SAFETY rule 2, MESSAGING and the messaging README say so. The cohort-change sub-case of
GO-PRIV-03 was refuted and is unchanged. With the messaging fix reverted the 9 new messaging tests fail as
intended (query bound n3=38 → n256=1303 before; ≤28 and membership-independent after) and the block-budget
test fails (404 instead of 429). WSL Go 1.27.1, social_g2, code 65f20ba on 778470d: lanes messaging 55,
safety 50, admin 19, jobs 64, web 92 top-level pass, 0 skip, 0 fail. <<G2-5D-EVIDENCE>> Reviewer: APPROVE.

## 2026-10-05 — Report eligibility independent of read gates (GO-PRIV-01, F4)

Valid until: `fix/go-report-eligibility` is integrated or superseded — then treat as history.

[ADR-0041](docs/adr/0041-report-eligibility-predicate.md); design by the G2 critic, owner decisions
2026-10-05. `safety.ReportTarget` gets its own predicate (activity same cohort ignoring blocks/hidden; post
by thread-owner cohort plus a seat; user via the new `Config.CanSeeUser` = active, pair-visible, unblocked);
labels are generic where the read gate would hide a title or name; every non-staff refusal is not-found;
`ReportHTTP` checks eligibility before the 20/h budget; report-page user lookups spend a 240/h
`report_lookup` budget (429 when spent). `UnsafeReport` checks the member seat inside its transaction.
Messaging `Report` needs only an active participant. `Social.Leave` and the web unsafe/leave actions no longer
use the block-aware read gate; a member blocked with the owner gets a safe-exit page
(`web/activity_safe_exit.html`, shared `_activity_safe_exit.html`). The reference's user-label leak is a
recorded reference bug. <<G2-5A-EVIDENCE>>

## 2026-10-05 — Messaging list/history query ceilings (preserved Codex candidate)

Valid until: `fix/go-messaging-query-ceilings` is integrated or superseded — then treat as history.

Applied the Go files of the preserved candidate `_temp/go-native-toolchain/messaging-query-ceilings`
(patch sha256 `6cf918f0…`, equal to its manifest; Codex's STATUS/WORKLOG copies were not applied).
Conversation-list and message-history identity reads now join their owning queries
(`scanConversations`/`populateConversationParticipants`, `messageReadProjection`/`serializeMessageRows`);
serialized fields, filters and ordering are unchanged (independent reviewer traced each). Ports
`test_v1_conversation_list_query_count_is_constant` (≤5) and `test_v1_message_history_query_count_is_constant`
(≤7); with the fold reverted they measure 7>5 and 9>7. privacy-coverage entries now native evidence.
WSL Go 1.27.1, social_g2, code f9d7f68 on 778470d: lane messaging 49 top-level pass, 0 skip, 0 fail.
Reviewer: APPROVE.

## 2026-10-05 — Membership rows and logistics co-member scoped (GO-PRIV-07)

Valid until: `fix/go-membership-logistics-scope` is integrated or superseded — then treat as history.

[ADR-0046](docs/adr/0046-membership-logistics-scope.md) (owner decisions 2026-10-05). `social.Membership()`
and `membershipsList` (rows and count) add `membershipAudience`: the row's own user, or the activity owner
or a current non-guardian member (co-organizers included) with no block either way with the row's member.
Every other caller of these reads was checked (join/leave/presence return own rows; vote/admit voters are
members or organizers). The reference `MembershipViewSet` stays cohort-wide (recorded as a defect).
New TestPostgresMembershipReadsAreCoMemberScoped fails with the fix reverted (membership_scope_test.go:85,
a non-member listed another activity's row). WSL Go 1.27.1, social_g2, code caf0602 on 778470d: lane social
42 top-level pass, 0 skip, 0 fail; the blocked-pair rule (added after review on owner decision) is
qualified with the stacked run below. Reviewer: APPROVE.

## 2026-10-05 — Live chat frames metered; typing never evicts a socket (GO-PRIV-04)

Valid until: `fix/go-chat-typing-throttle` is integrated or superseded — then treat as history.

`internal/chat`: each socket meters inbound frames before any authorization or database work (5 frames/s,
burst 10; typing at most once per 2 s; excess typing is dropped silently and spends no token; an ordinary
frame on an empty bucket closes with 1008). Typing is coalesced per (room, actor) process-wide (2 s,
bounded 4096 entries, evicts expired then oldest, never refuses a new typer). `Broker.dispatch` drops
typing for a subscriber whose queue is at least half full and never closes on a typing overflow; durable
events keep the close-and-reload rule. One typing dispatch resolves the typer's identity once per local
fan-out (an unexported, never-marshalled field); each recipient's own read authorization still runs per
delivery, and NOTIFY payloads are unchanged. Not done (not required): a per-(actor, room) socket cap,
cross-replica coalescing. New TestPostgresPlainThreadTypingFloodStaysBounded fails with the chat package
reverted; the chat unit tests need the new types. WSL Go 1.27.1, social_g2, code 2428cce on 778470d:
hermetic gofmt/vet/-race chat+messaging ok; lane messaging 48 top-level pass, 0 skip, 0 fail. Reviewer:
APPROVE.

## 2026-10-05 — Avatar uniqueness in-cohort; fingerprints only for avatars (GO-MEDIA-01, GO-MEDIA-02)

Valid until: `fix/go-avatar-cohort-media-minimisation` is integrated or superseded — then treat as history.

[ADR-0044](docs/adr/0044-media-fingerprint-minimisation.md). The profile duplicate scan joins
`accounts_user` and compares only with avatars of the uploader's committed cohort (fresh actor inside the
upload transaction; unassigned compares with unassigned, as the reference). `media_photo.phash` is stored
for profile photos only; non-profile manifests drop `perceptual_hash` and `source_sha256` (video included:
the worker compares the attachment row's digest column), except Wikimedia place covers (licensing provenance
read by the operator proof). Success audits (`media.uploaded`,
`media.attached`, cover uploads, `media.video_ready`, `place.cover_resolved`) no longer carry the digest;
blocked-scan audits keep it. `EnsureSchema` scrubs stored non-profile fingerprints idempotently; the
hash-chained `safety_auditlog` keeps historical digests by design. Two reference cases are ported
(`test_same_image_allowed_across_cohorts`, `test_profile_near_duplicate_rejected_within_cohort_only`;
privacy-coverage entries now native evidence). Four new PostgreSQL tests fail with the production files
reverted (cross-cohort refusal ×2, thread fingerprint stored, scrub absent). WSL Go 1.27.1, social_g2:
fail-before and lanes media 46/contracts/jobs at 0cb93cd on 778470d; after review dropped the video digest,
<<G2-5B-COUNTS>>. Open (not this slice): an avatar attempt is counted before
image processing, so a busy refusal still spends it (noted by G3).

## 2026-10-05 — Unreachable web action cases removed; two descriptions corrected (IDP-6, GO-MEDIA-05, GO-EXPORT-01)

Valid until: `fix/go-low-dead-code` is integrated or superseded — then treat as history.

IDP-6: `web.action`'s generic switch held nine cases for names that `AccountAction` or `SocialAction` always
answer first, including an account-deletion confirm gate the live erasure path never had; they are removed and
the `actions` map entries stay (POST routing depends on them). Erasure is unchanged and matches the reference
(GET preview, POST erases, no confirm field); new TestNativeAccountDeleteFormErasesWithoutConfirmField pins it
(it passes before and after: a pin, not a regression test). GO-MEDIA-05: `AttachToPost` is called only by
tests; its comment says so; it is not rewritten because its gates differ from the live prepare/publish path
(pre-codec ownership check, kind gate, post row lock, post-commit URL signing). GO-EXPORT-01: the
`export_agent_snapshot` job runs `jobs/snapshot.go`; the reviewed `export.Service` producer is unwired; the
privacy gates are equivalent but emitted values differ (name fallback, timestamp fraction, slug length, credit
trimming, one repeatable-read generation); `internal/export/README.md` now says so, and choosing one producer
is open. Independent reviewer: APPROVE. WSL Go 1.27.1, fixture social_g3, code tree 6ea32d3 on base 82a2625:
`scripts/check-native.sh` exit 0; lanes web 93 and media 42 top-level pass, 0 skip, 0 fail; `git diff --check`
clean. Not run: other lanes (unaffected packages).

## 2026-10-05 — Group mentions and forward message history (GO-PRIV-05, GO-PRIV-08)

Valid until: `fix/go-privacy-low` is integrated or superseded — then treat as history.

GO-PRIV-05: `social.mentions` returns early for group threads, as the reference `_ping_mentioned` does, so
a standing group never turns names into pings; activity threads are unchanged. GO-PRIV-08: a v1
`?after=X` history page with more than `limit` unseen messages dropped the oldest one; forward pages now
keep the oldest `limit` rows and the client continues with `after=<last id>` (`next_cursor` stays empty in
forward mode). The reference has the same `msgs[1:]` trim (apps/messaging/views.py:342): recorded as a
reference bug, not changed there. GO-PRIV-06 does not reproduce on `feat/go-native-toolchain` (the
in-memory limiter became PostgreSQL budgets, ADR-0037); budget capacity is G3's `fix/go-budget-families`.
New PostgreSQL tests TestPostgresGroupThreadPingResolvesNoMentions and
TestPostgresV1AfterHistoryKeepsOldestPendingMessage fail with the fix reverted. WSL Go 1.27.1, fixture
social_g2, code head 3d8add6 on base 778470d: gofmt/vet/-race (hermetic) social+messaging ok; lanes social 42 and messaging 48
top-level pass, 0 skip, 0 fail; `git diff --check` clean. Not run: other lanes (unaffected packages).

## 2026-10-05 — Go landing governance record corrected (ADR-0040)

Valid until: `docs/go-governance-record` lands or is superseded — then treat as history.

Owner decisions asked 2026-10-05, recorded as [ADR-0040](docs/adr/0040-landing-and-deployment-review-gates.md):
the 2026-10-04 landing of cbf8cdc was authorized, not code-reviewed; while undeployed, landing needs local
gates green on the exact head plus an independent reviewer; human auth/erasure/privacy/safety review gates
first deployment. GOV-4: ADR-0032 gains a 2026-10-05 amendment withdrawing "satisfying the implementation
review gate" (and noting GOV-3: no test adopts a Django-migrated DB); its Status/README row, STATUS, SECURITY,
agent-map, NATIVE_SERVER, deploy/README and the 2026-10-04 entry below carry the correction. GOV-2: AGENTS
Safety/Commands, agent-testing "Before commit and landing" and SECURITY point "green" at ADR-0040; CI is
dispatch-only (ADR-0033); the fail-open `test -z "$(gofmt -l ...)"` row now names `check-native.sh`;
Dependabot PRs get no automatic checks. Before-first-deployment list (GOV-1/GO-RT-03 Render env, GO-RT-01
superuser baseline, GOV-3/GO-RT-04 adoption, GO-RT-08 floating images, GO-MEDIA-06 upload timeout, GOV-9
rollback, GOV-4 review) is in `docs/RELEASE_READINESS.md`; STATUS no longer lists Render/fresh cloud-init as
working. GOV-5: no live doc carried exact-head CI claims; the WORKLOG 810492a/e6742a1/3439a3b run IDs got a
bracketed note that they predate landed main cd006e3, which has no hosted run. GOV-8: STATUS is 115 lines,
cites no Django command files; agent-testing has no pytest-in-web-container step or dead anchors. GO-08: the
superseded STATUS open-work bullets collapse to one line, all integrated on `feat/go-native-toolchain`:
profile authority/media-group/fixed-window budget-erasure review fixes (profile viewer reloaded after rate
admission), the ADR-0035 guarded permissions/private API contracts (380 operations/318 paths/166 schemas),
the three completion lanes first integrated on `feat/go-migration-finish`, and the ADR-0038 toolchain (CSP/
private-EU backup operators, shell hooks, exporter-to-sidecar qualification, adversarial native matrices). GO-03: the 40 "reused" receipts in `restart-checkpoint.json` (numbers unchanged),
the TODO frozen bundle and STATUS now read "package directory unchanged; transitive dependencies changed after
the receipt — not qualified on this head". GO-04/GO-05 (docs): NATIVE_WINDOWS_TODO checks run on Linux or
WSL2 ext4 with sha256-verified go.dev Go1.27.1, `GOTOOLCHAIN=local`, `-mod=readonly` (owner-authorized download
and Docker base images 2026-10-05); a canonical `docker build --no-cache` plus Trivy scan is mandatory before
landing/release. GOV-6: no Social Dependabot PR is merged; closing postgres-18/node-26/django-6.0.8 is the
owner's action. Docs only; no code, script, workflow or Dockerfile changed.
Windows Git Bash: fleet docs files53/dead_links0/stale_terms0/retired_verbs0/orphans0, `git diff --check`
clean, budgets AGENTS80/STATUS115/agent-testing67/agent-map47/CLAUDE3. Not run (docs only): Go, qualify, frontend.

## 2026-10-05 — Native gates fail closed (GO-02/GOV-7, GO-06, F5)

Valid until: `fix/go-qualify-fail-closed` lands or is superseded — then treat as history.

GO-02/GOV-7: `scripts/qualify-native.sh` used `rg` for its zero-skip gate, so a host without ripgrep printed
"no skips" and exited0. It now refuses non-Linux hosts, scans with `grep` (exit0 skip fails, 1 passes, other
errors fail) and requires a positive `--- PASS:` count per lane (`no native tests ran in <pkg>` otherwise).
`scripts/check-native.sh` resolves Go, takes gofmt from `GOROOT/bin` (missing ⇒ exit2) and captures `gofmt -l`
by bare assignment so a failing gofmt is fatal; `native.yml` captures its gofmt output the same way (agentapi
stays format-checked in `go.yml`). `go.yml` agentapi format step now also captures `gofmt -l .` by bare
assignment and fails on a non-empty list (same fail-open `test -z "$(...)"` pattern). GO-06: the embedded
offline service-worker `node:test` suite now runs in `check-native.sh` (node required) and the `ci.yml`
frontend job. F5: `runtime_verification` is relabelled as a manifest claim in `contracts.Check`, STATUS, the
Windows TODO and agent-testing; no gate behaviour changed. New `scripts/test-native-gates.sh` drives both real
scripts with stub go/docker/gofmt/node/uname on a restricted PATH without rg. Review hardening: GOROOT comes from
`go -C services/server env GOROOT`, the worker lane uses `--test-reporter=tap` and needs `# pass N` with N>0,
`native.yml` runs the harness, and new cases cover grep -q/-c errors, failing `go env`, empty GOROOT, unknown Go
and a zero-pass worker run: 15/15 pass; against the 07bdf5a scripts 13/15 fail (all but e and n).
Windows Git Bash: `bash -n` on the three scripts, `node --test` worker suite 4/4, `git diff --check` and fleet docs
files53/dead_links0/stale_terms0/retired_verbs0/orphans0 pass. Not run here: Go, shellcheck, docker, Linux hosts.

## 2026-10-05 — Requested restart checkpoint

Valid until: Windows resumes this branch and supersedes the checkpoint — then treat as history.

Human restart direction ended the active campaign. The current branch preserves993 verified original cases,
1678 unresolved and0invalid of2671; strict retirement exits1 and every Python reference is retained.
The broader frozen source batch restores exact web/report/saved-search/offline/deletion feedback, int64 HTML,
venue/claim/access/brief/API, typed event query errors, operator/ICS/cover/export, privacy records/reports and
messaging recipient/cap/current-key contracts. Materialized footer content is actual60→60 queries8→24,
with exact article/footer/avatar positives; no query ceiling was raised. Guardian oversight block behavior
and remaining list/history query ceilings are explicitly unclaimed. Independent05febd5 credential review is green.

Fourteen affected race lanes pass586 top-level tests without skips: configuration59/accounts61/admin19/app28/
catalog47/commands27/contracts5/export5/jobs64/media42/messaging47/safety49/social41/web92. Forty unchanged
tests retain previous receipts (626/21 lanes). Source/hashes/three-module vet/race and source/package/linked
audits pass; one unused required-module advisory remains. Source549 hashes were frozen and rechecked.
`docs/reviews/native-go/restart-checkpoint.json` preserves aggregate exact named tests/log hashes and artifact route.

Canonical Dockerfile fresh build failed because cached install layers were unavailable without network. The
offline current Go1.27.1 CGO0/buildvcsfalse/trimpath/s-w executable was overlaid on exact retained runtime5ea84fca.
Fresh imagec44143fdc56f7cde24460124e1c037b33ead934ed72a75b03b6283e17164e44a contains only one added server
layer; runtime configuration/nonroot/profile/codecs/licenses/external frontend/templates payload remain.
Real packaged health/ready/worker/schema200 and cleanSIGTERM0/noOOM pass on synthetic private PG/no host ports.
The new image scan is unverified: three scanner setup attempts failed on temp/cache permissions before scan.
No vulnerability-clean claim is made for this executable; the old clean image audit remains history.

Remote main cd006e3abe028acb2ccf5be4552bc94aebc1d234 was fetched/verified, shared main remains clean.
The existing worker branch receives a local reviewable checkpoint only; coordinator owns publication/main review.
`docs/NATIVE_WINDOWS_TODO.md` carries exact queues/gaps/commands/Windows resume steps. Unique unpublished V1
prototype source/tests are preserved as exact-byte inert keepers, never wired into runtime. No session IDs/raw
transcripts/secrets/corpus/voice evidence enter Git. Owned smoke is stopped/exited0, synthetic DB/network retained
for explicit task stop; no unmerged work/shared-cache cleanup. Human auth/privacy/safety and retirement gates remain.
Final fleet doc gate: files53/dead_links0/stale_terms0/retired_verbs0/orphans0; budgets80/120/46/61 and
whitespace pass. Owned synthetic DB is stopped and retained; no application/qualifier/auditor job remains running.

## 2026-10-05 — Independent auth admission binding repair

Valid until: this repair is independently reviewed/requalified or superseded — then history.

Independent review on a693291 reproduced API username case-alias/duplicate-key admission
mismatch and browser whitespace variants reserving separate failure buckets for the same
trimmed credential. The wrapper now parses exact allowed JSON keys once, rejects ambiguous
or repeated keys and delegates canonical credential JSON from the admitted cleaned username.
Password and credential case remain intact. Validated unused email/name metadata cannot
expand the delegated credential body. Failure-key and restricted-proof seams share cleaning.
The hash-pinned authentication library remains unchanged.

Targeted Go1.27.1/race receipts in _temp/go-native-toolchain/tests: accounts-login-binding.log
has10 passing top-level login tests/no skips, including real PG replica/padding/alias refusal,
no sessions on lockout, fixed expiry and repeated successful session/reset behavior. The
actual pinned-library/in-memory recording store verifies the canonical admitted username.
app-login-binding.log has2 assembled application/proxy/replica tests passing/no skips;
safety-source-current-root.log qualifies actual restricted-proof/capability cases with the
same normalization. Final small canonical-metadata unit and targeted PG refresh are recorded
separately before this checkpoint. Auth snapshot hash checks and targeted vet pass.

The initial independent API overlay intentionally reconstructs the old map parser and
passes original ambiguous JSON straight to the unchanged library. It cannot test this
wrapper repair without invoking the production parser/canonical delegation; it is preserved
as the discovery negative control. The independent browser-padding overlay passes unchanged.
No original login source IDs are newly declared complete from this checkpoint alone.

Other bounded venue/web/operator/privacy queues continue. The working source gate currently
has693 verified/1978 unresolved/0invalid; the preceding committed640-case ledger is preserved
in a693291. This isolated repair does not include the unrelated uncommitted source-case batch,
and no fresh matching application image is claimed yet. All unlanded work and reference
source/tests remain. No push/main merge/provider/minor/ingestion/scheduler/deploy activation.

## 2026-10-05 — Source-case contracts and failed-only login checkpoint

Valid until: the candidate is integrated/requalified or superseded — then treat as history.

The frozen2671-case source ledger now verifies640 and retains2031 unresolved cases, with0
structurally invalid evidence. The Go retirement checker exits1 as required; all original
Python source/tests remain. Privacy, product/web/venue and ordinary operator/ingestion cases
are being ported in bounded parallel queues. Neither grouped native test counts nor matching
test names establish source equivalence. A semantic audit found17 earlier claims needing
stronger assertions; these were first downgraded, then qualified with independent exact
erasure/export/guardian/profile/holder-proof/media/interest assertions before being restored.

Actual contract gaps repaired: GeoJSON IDs above2^53 retain int64 precision; interest/topics
GET/POST pages restore source selection/save/redirect behavior; notification unread markers,
ward refusal redirects and the four source soft kid-needs facts are restored. A narrow
trusted static-translation tag preserves source literal text while dynamic/interpolated
data stays escaped. Deferred missing-handler diagnostics, immediate failed-sync return,
metrics HELP declarations and32hex request IDs now match their source contracts. Generated
RO-EDU client/pin and the producer acquisition/ML exception are unchanged.

ADR-0039 records shared failed-only login semantics: lowercase username plus normalized
trusted client IP, ten failures in a fixed fifteen-minute window starting at first failure,
success clearing, durable cross-replica reservations and bounded fail-closed completion.
Raw identity/IP values are not stored/logged. The unchanged hash-pinned auth library still
verifies credentials/current active state and issues sessions; only a private one-use DB-
validated reservation context bypasses its unrelated all-attempt limiter. Browser invalid/
lockoutHTML200 and success302 are restored. Restricted proof reuses the same counter without
creating a session and retains owned action-bound one-shot thirty-minute appeal capabilities,
exact bigint IDs and correctable statement errors. This introduces no environment variable
names. Human auth/privacy/safety review is required before landing.

Go1.27.1/GOWORK=off/GOMAXPROCS=2/GOFLAGS=-p=2 and task-owned caches/tmp; actual private fixture
network go-native-toolchain-test/database go-native-toolchain-db, read-only source, dropped
caps/no-new-privileges/UID1000 and explicit synthetic DSN/native codecs:

- Eleven affected race lanes pass419 top-level tests with zero skips/failures:
  configuration53/accounts55/app27/catalog27/commands14/jobs44/media40/messaging34/safety29/
  social40/web56. The65 unchanged qualified tests retain the prior checkpoint receipts:
  484 total across21 lanes. `qualification-caseports.json` binds exact names/logSHA256 and
  distinguishes refreshed from reused receipts. Fixture repairs and compile-only iterations
  were corrected before qualification; failed iterations were not counted as passes.
- `scripts/check-native.sh` passes three-module format/auth hashes/vet/race/whitespace.
  Authcore source hashes are unchanged. Source-case structural provenance remains strict.
- govulncheckv1.8 source/imported-package/same-source linked binary gates pass. One unused
  required-module advisory remains separately reported; imported packages/linked calls have0.
- Matching Go-only image social-native:go-source-caseports-20261005 is
  `sha256:5ea84fca75b13acdbc7d13a71e01a52954f3733e358efe29b1e4ba07b771ce5d`.
  Trivyv0.75/publicDB2026-10-04 reports0 findings at the required image severity gate in
  Debian and the native executable. Source changes were frozen before the image build.
- Fleet doc/link gate passes: files52/dead_links0/stale_terms0/retired_verbs0/orphans0;
  documentation budgets80/120/46/61 and whitespace pass.

Only the assigned Social worktree was mutated. Shared main remains clean/read-only. No
main merge/push/deploy, hosted workflow enable/dispatch, real ingestion/provider/minor/
scheduler activation, paid infrastructure, actual secrets/env/auth-store reads or retained
work deletion occurred. The fixture and all unlanded branches/worktrees/scratch remain.
Remaining source cases continue; this checkpoint does not claim full migration completion.

## 2026-10-05 — Native verification and operator toolchain candidate

Valid until: the candidate is integrated/requalified or superseded — then treat as history.

ADR-0038 extends the native port into ordinary tests/tools/operators/CI. `ci.yml` runs native
Go and Node; the original Python job gates remain unchanged in optional manual `reference.yml`,
with a distinct concurrency group. All workflows remain manual-only/read-only, with no hosted
enable/dispatch claim. The Python hook harness is replaced by opt-in native shell hooks. Docker
backend compilation defaults to GOMAXPROCS2/GOFLAGS=-p=2. Generic fleet governance helpers and
the producer acquisition/ML exception remain scoped; no generated RO-EDU Python copy was edited.

New database-free CSP CLI supports bounded file/stdin JSON/JSONL input and text/JSON aggregates.
Native private-EU S3 backup upload/download/probe reuse the reviewed SigV4/endpoint/SSE policy,
stream bounded private files, use conditional creation and verify persisted size/hash/encryption.
The backup pipeline keeps native pg_dump/gzip with private scratch/time/size bounds; cloud-init
no longer installs awscli. Synthetic TLS/files prove the adapter; no real upload/restore occurred.

Independent native matrices cover avatar/holder proof/guardian guardrails, organizer/profile/
sentiment/series, appeals/overlapping bans/group lifecycle, scanner/probe/evidence retention,
RO-EDU envelopes/HTTP/ICS/mapping, taxonomy/FK adoption, public snapshots, saved searches/notices,
and populated4-to28-row query growth. Organizer/thread/discovery/corrections/export, messaging/
guardian history, profile interests/inbox and moderation triage remain query-bounded. Actual
export.Service.Snapshot feeds an independently running Go agentapi binary; Unicode venue search,
publication/license/privacy, headers/gzip/ETag/cursors and IDs above2^53 are checked end to end.

Contract discoveries repaired with regressions: repeated organizer grant now has no duplicate
notice/audit; scanner absence/outage preserves pending attempts/source bytes; snapshot IDs and
foreign keys retain exact int64 values; venue search uses the original raw-name semantics while
public display retains approved corrections. Eight actual TCP header-only cases prove401/403/413
before sending upload bytes. Declared oversize rejects before auth; body-bearing refusals close
instead of waiting for net/http's unread-body drain. Authcore and Unicode decoder are unchanged.

The deterministic scanner-outage/audit-failure combination revealed rollback could leave an
exhausted processing lease later inferred as terminal. Failed finalization now returns an error;
an exhausted stale lease without a committed outcome holds source evidence for operator recovery.
Pending exhausted cleanup remains. This conservative hold is recorded as a policy difference,
not false reference parity, and requires human sensitive-code review before landing.

Go1.27.1, GOWORK=off/GOMAXPROCS=2/GOFLAGS=-p=2, task-owned caches/tmp, internal network
go-native-toolchain-test, disposable go-native-toolchain-db and native codecs:

- Final21 native lanes:369 top-level tests pass, zero skips. The first complete harness run
  passed368; final catalog17 and media37 reruns qualify the raw-name/corrected-display and
  compound audit-failure additions. `qualification-final.json` binds each package's exact test
  names and log SHA-256; configuration37/accounts28/admin18/app26/backup5/booking7/budgets9/
  catalog17/commands9/contracts3/discovery2/donations5/export4/jobs31/media37/messaging15/
  notifications4/recommendations8/safety24/social35/web45. No legacy test count equivalence.
- Native shell check passes: format, authcore hashes, three-module vet/race and whitespace.
  Contracts/media/catalog affected checks were refreshed after the final repairs.
- govulncheckv1.8.0 source/imported-package/linked-binary checks pass for server and sidecar;
  final server receipts refreshed after source changes. Trivyv0.75.0, public database refreshed
  2026-10-04, reports0 fixable HIGH/CRITICAL findings in Debian and the native Go executable.
- Final image social-native:go-native-toolchain-20261005 is
  `sha256:4e480d9592540c1341c9987f3217d39d01e14a79c25c4e0349d422a97f688770`.
  Image tools contain no Python/pip/awscli; CSP command runs without DB/env bootstrap and backup
  probe rejects missing verified storage by setting name before any network/DB startup.
- YAML validation passes manual triggers/read permissions and exact preservation of original
  reference job gates. Bash syntax, gofmt, whitespace and fleet doc/link gate pass:
  files=52 dead_links=0 stale_terms=0 retired_verbs=0 orphans=0.
- Bounded race-enabled app benchmark,20 iterations/GOMAXPROCS2, synthetic PostgreSQL fixture:
  health94.2us/9493B/61allocs; ready360.9us/10458B/75; events5.48ms/31656B/167;
  places7.80ms/53058B/188; schema114.01ms/11244482B/59705. These are fixture observations,
  not production latency, capacity or hosting-price guarantees.

The Go-only frozen inventory retains2671 original declarations with exact file/line/SHA provenance
against ce3d0e3ee9f180ce2e95140300b12582db9895d6. The reviewed ledger currently verifies283,
leaves2388 unresolved and has0 invalid entries. The retirement checker deliberately exits1;
this blocks retirement rather than claiming a completed full port. All original Python source/
tests remain. Named Go evidence and recorded runtime statuses require reviewer confirmation of
semantics/receipts; static linkage cannot prove equivalence. Next priorities are retained web/
venue/ingestion/operator/privacy source cases, not deleting the coverage gap.

Only social_media_activities_app's assigned feat/go-native-toolchain worktree was mutated.
Native sources and qualification containers were read-only during fixtures; no host DB ports,
actual env/auth stores, real ingestion/providers/minors, scheduler activation, paid infrastructure,
deployment, workflow enable/dispatch, push or main landing. Scratch/logs remain in
`_temp/go-native-toolchain`; unlanded prior branches/worktrees/stashes/scratch remain intact.

## 2026-10-04 — Review media policy controls

Valid until: the review fixes are integrated/requalified or superseded — then treat as history.

Fixed both confirmed P2 native projection gaps under ADR-0034. Thread disappearance choices apply
the service's current adult/minor floor, deduplicate collapsed choices and label the effective
seconds. The HTML submit path accepts those effective choices and the source baseline values;
actual media publication retains its existing clamp. Zero/blank Keep retain no expiry.

Composer image/PDF/video flags and the accepted MIME set use the same cohort/attachment/file/video
policy as upload admission. Attachment-disabled/missing-media views hide attachment/timer/help
controls while retaining text posting. Minor viewers see images only. Video still requires the
processor's runtime video switch. Private scan, membership/cohort, storage and serving gates are
unchanged; no source admission gate or codec implementation was relaxed.

Preact intentionally excludes thread detail/composition and group composition remains text-only.
The existing native HTML context/template is the required projection; no new SPA thread feature
or frontend bundle change was made. An explicit native-context marker preserves the shared template's
offline reference baseline only when the marker is absent. Native explicit false/missing media
never falls back. No Python runtime or authcore source is changed in this lane.

Go1.27.1 with task-owned caches, GOWORK=off/GOMAXPROCS=2/GOFLAGS=-p=2:
- Race-enabled media binary: `TestComposerCapabilitiesFollowCohortPolicyAndCodecSwitch`,
  `TestDisappearanceChoicesUseEffectiveFloorAndPreserveKeep`, existing policy/token TTL regression,
  nondefault image/attachment-cap codec regression and nondefault video/denser-scan codec regression:
  five top-level tests pass, zero skips, real codecs in the already qualified Go-only image.
- Race-enabled web binary with explicit disposable `-web-domain-test-dsn`: three new composer cases
  pass, covering 14 actual rendered policy/cohort modes plus 7200/legacy3600/0/blank multipart,
  persisted two-hour or nil expiry, member PDF download, signed viewer binding, nonmember privacy,
  scanner rejection with no orphan post/attachment, and old marker-absent template controls.
  Four existing activity-thread/private-read,
  adult PDF, minor/guardian and scanner-rollback regressions pass. Seven web tests total, zero skips.
- `go -C services/server vet ./internal/media ./internal/web`, gofmt and `git diff --check` pass.
- Doc gate passes: files=52 dead_links=0 stale_terms=0 retired_verbs=0 orphans=0.

All DB records, scanner verdicts and uploads were synthetic on internal network go-review-media-net
with private go-review-media-db, source read-only and no published ports. Test containers use the
scratch-owning UID; an initial default-image UID couldn't create TempDir and was corrected before
qualification. No full suite was rerun; root owns combined-source requalification/image rebuild.
No push, deployment, real provider/ingestion/scheduler/minor activation; original retained budget
worktree/stash/scratch are untouched. Logs remain in `_temp/go-review-media/tests/`.

## 2026-10-04 — Shared-budget fixture qualification

Valid until: combined integration/landing verification — then treat as history.

All 18 native PostgreSQL/codec lanes qualify: **194 tests pass, zero skips**. This includes nine
budget tests; actual custom-policy account/safety/message/social/catalog/saved-search flows;
idempotent connection/group/unsafe repeats; retained failed/duplicate attempt debits; mapped-IPv4
peer identity; and media/web authorization/lifecycle coverage. Every legacy LIKE-based fixture now
applies native additive migration in its own schema, including functions, counters and triggers.
The public fixture budget table and capacity counters remain zero, proving no fallback writes.

Commands used Go 1.27.1, `GOWORK=off`, `GOMAXPROCS=2`, `GOFLAGS=-p=2`, task-owned module/build/tmp
caches, the existing Go-only codec image `social-native:go-complete-20261004`, and the dedicated
internal-network PostgreSQL fixture `go-shared-budgets-db` / `go-shared-budgets-net`:

- `go -C services/server test -race ./...`, `go -C services/server vet ./...`, and
  `go -C services/server run ./cmd/check-authcore` pass. Ordinary PG tests skip without a DSN;
  the explicit fixture run below is the database evidence.
- `scripts/qualify-native.sh GO IMAGE NETWORK DISPOSABLE_DSN /home/dobo/work/_temp/go-shared-budgets`
  qualifies sixteen packages before the new social fixture violated an existing unique-group
  city/type/cohort rule. Changing only that synthetic test's second city, then the identical
  race/read-only harness for social + web, qualifies all eighteen packages. No test was weakened.
- Native fixture migration/reference seeding ran only on the disposable database. Initial fixture
  startup/reference omissions were corrected before qualification; no production state was used.
- Formatting, whitespace and doc gate pass: files=52 dead_links=0 stale_terms=0 retired_verbs=0
  orphans=0. Logs live in task scratch `tests/`; no secret stores or actual env files were read.

The final fixture run under concurrent host work measured 100 sequential admissions at
1/1,000/9,000 buckets in 6.15/6.32/6.31 ms mean, 9.35/9.83/10.24 ms p95. Additional 2,000 peer
identities retained 9,992 bytes of Go heap after GC. Together with the earlier lighter run,
this supports the removal of cardinality-dependent table scans; it does not establish production
capacity. The short capacity-counter update remains a possible high-throughput contention point.

CLI/environment binding, source-only maintenance tick wiring, fresh actor participation gate and
combined-source qualification remain coordinator/config-lane integration responsibilities. No
main merge, push, deployment, real ingestion, scheduler enablement or minor/provider activation.

## 2026-10-04 — Shared-budget worker (ADR-0033)

Valid until: combined integration/landing verification — then treat as history.

Replaced API/social/catalog/saved-search/CSP local admission with PostgreSQL sliding histories,
keyed anonymous peer digests, user-FK erasure and exact statement-trigger capacity totals. Domain
preflight/reservation/replay retains failed-attempt debits without nested pool/FK deadlocks. Added
typed caps/windows to existing account/safety/message PG budgets, preserving their fixed windows
and idempotent fast paths; denied counters cannot overflow. Config parsing and combined wiring
belong to the config lane. Cat affinity and optional DB-free agentapi remain documented contracts.

Go 1.27.1 native `test -race ./...` and `vet ./...` pass (ordinary PG tests skip without fixture).
Nine race-enabled budget tests pass against the dedicated internal-network synthetic PG16 fixture;
no skips, no real data. Low-pool tests use two connections. Storage bounds10kkeys/1mevents, expiry,
privacy, failed-attempt debits, counter erasure and concurrency pass. Synthetic cardinality means
2.97/2.10/1.91ms at1/1000/9000buckets; p957.73/4.21/3.53ms; +2000peers retainedGoheap+26488bytes.
Doc gate: files=52 dead_links=0 stale_terms=0 retired_verbs=0 orphans=0; whitespace clean.
These are local fixture measurements only. Full domain/codec fixture and combined CLI verification
remain pending at this checkpoint. No push, merge, deployment or activation performed.

## 2026-10-04 — Go public serving foundation (review branch)

Valid until: this branch is reviewed/landed or superseded — then treat as history.

- Owner selected Go after the Romanian Go rollout. `feat/go-server-foundation`
  implements ADR-0031's first serving slice; source is not landed/deployed here.
- Checked company-ops and canonical deployment/media policy read-only; no org
  source, governance, storage, provider or visibility changes. Shared private
  workflow sources returned unavailable, so local Go checks were added.
- Complete Go race/vet and native static build passed. Isolated PostGIS tests:
  exporter/native contract plus public discovery/listing **38 passed** (3.61s).
  All data is generated test data; native query contract ran, not skipped.
- Official govulncheck 1.1.4 source SSA panicked on Go 1.27 syntax. Conservative
  package and compiled-binary scans both passed: no vulnerabilities found.
- Static scratch OCI image builds with a verified Go builder digest; UID65534,
  read-only/no capabilities/no-new-privileges, 512MiB/1CPU/64PIDs and no-network
  local qualification passed native health. Tiny fixture idle memory ~1.9MiB is
  not a production sizing or total-bill estimate. Final Trivy0.75.0 scan: zero
  fixable HIGH/CRITICAL findings. Ruff621files, YAML/Compose, docs37files, budgets
  and whitespace pass. Receipt: `docs/reviews/go-foundation/verification.json`.
- GitHub Go quality/container/security run37156292496 passed; frontend,
  dependency audit and Django image job passed. Initial full Django CI stopped
  at archived Bullseye PGDG apt sources before tests. The pinned CI-only database
  now uses the signed official PGDG archive, retaining server hold and signature
  verification ([PGDG notice](https://wiki.postgresql.org/wiki/Apt)). Runtime
  database images and live databases were not changed. The existing unpinned
  Trivy installer also failed release discovery; the same verified action/tool
  pins as the Go job now replace it, preserving its report-only CVE policy.
  Full Django lint/test job111301416554 passed: **2791 tests +38subtests**,
  migration drift and deploy checks passed (existing warnings remain).
  The previously documented websocket failures did not recur in this CI run.
- Remaining review: human privacy/safety sign-off before landing, explicit
  withdrawal/erasure and staleness budget before rollout, then live API parity.
  Separate ManagedScanner malformed-verdict gap is recorded in ADR-0031; no
  scanner or media behavior was changed by this branch.

## 2026-08-22 — one nightly job, one RO-EDU credential

- `sync_roedu` runs the venues lane and the events lane back to back, but only the venues lane ever
  saw `ROEDU_API_KEY`. The events lane took its key from an argparse default of `social-app-dev` that
  nobody passed, so a single nightly job authenticated to the producer as two different clients —
  silently, because a dev credential that happens to be accepted looks exactly like success.
- Three sites, one rule: the credential comes from the environment, and its absence is an error rather
  than a fallback. `sync_roedu.py` forwards `os.environ["ROEDU_API_KEY"]` to the events lane (its
  existing fail-open guard already returns early when the variable is unset, so the subscript cannot
  raise). `sync_roedu_events.py` drops the hard-coded default, resolves `--api-key` or `ROEDU_API_KEY`,
  and raises `CommandError` when neither is present. `ro_scraper.py` drops the same `social-app-dev`
  fallback, so a missing key fails at the call instead of reaching production as dev.
- The test that pinned the broken argv asserted the exact list without `--api-key`; it now asserts the
  key is forwarded. Added: both lanes send the same credential, and neither the events command nor the
  ingestion adapter carries a dev fallback — the last two read the source for the fallback PATTERN, not
  the string. Eleven events-sync tests were resolving that default without saying so; they get an
  explicit credential fixture.
- Verified: 142 passed across the touched lanes; full suite 2773 passed with 15 failures, all in the
  chat/messaging websocket consumer tests, which fail identically (12 of 12, same tests) on pristine
  main in isolation. Both CI ruff commands clean, `makemigrations --check` clean.

## 2026-08-19 — ruff green, stamp beats formatter

- Cleared the ruff red that had sat on main since the vendored-client adoption: unused and mid-file
  imports in the app wrapper, an unsorted import block in the stamp test, and — the real one — the
  generated `_roedu_client_core.py`, which `ruff format` wants to rewrite and the `VENDORED_SHA256`
  stamp forbids.
- Regenerating was not the fix: the producer's own `--check` reports the copy already in sync, and its
  canonical file is not format-clean upstream either. The generated file is excluded from the FORMATTER
  ONLY; `ruff check` still lints it.
- Verified: `ruff check .` and `ruff format --check .` (the two commands CI runs) clean; suite 2785
  passed + 38 subtests on a fresh DB.

## 2026-08-18 — a refused RO-EDU product is loud (ADR-0030)

- **A refused RO-EDU product no longer reads as an empty city (ADR-0030, 2026-08-18).** The
  shared core ends its walk on `available: false` silently, so a policy-gate refusal
  and a city with no events produced the identical output: `places: created=0` /
  `applied 0 events`, exit 0 — while the page `note` naming the actual reason was
  dropped. Verified against a live server on 2026-08-18: every products page came back
  `available: false` ("schema not ready: … missing required policy column(s) …") and
  both commands reported a clean zero. `RoeduClient.iter_required` (this app's layer,
  not the stamped core) now raises `RoeduProductUnavailable` carrying that note, on the
  first page and mid-walk alike — the mid-walk case had been truncating a
  plausible-looking result set with no signal at all. `ingest_places --source=roedu`
  and `sync_roedu_events` exit non-zero with the note; the scheduled `sync_roedu` job
  catches it, logs it with a stack, reports it to Sentry when configured, writes it to
  stderr, and then still runs `resolve_place_covers` and completes the tick — the shared
  tick carries the GDPR/DSA duties and pings its heartbeat only on a fully clean run, so an
  opt-in external source must not red-line it, and cover resolution is city-scoped rather
  than RO-EDU-scoped. The
  app-pack lane refuses the same way (`read_app_pack` raises when a pack is empty because
  the producer withheld items or reported errors) — that is the lane a promoted release
  uses, so leaving it silent would have kept the defect where it matters most. Still quiet
  by design: the configuration skips, a genuinely empty product/pack, and items dropped by
  this app's own canonical checks (they make the read incomplete instead, so absence is
  never reconciled). Plain `iter` keeps the core's semantics.

## 2026-08-10 — Art.17 provenance follow-ups

- **Art.17 provenance follow-ups (2026-08-10).** Five surfaces that read the same
  provenance question now agree, via one helper —
  `safety.targets_with_unlifted_remove` — which is THE single implementation of "the
  platform's removal is still in force". Two independent reasons keep content hidden:
  the AUTHOR's own act (`is_author_deleted`, permanent, never cleared) and a standing
  REMOVE (the platform's act, liftable). (1) A granted appeal whose un-hide is
  declined no longer tells the user "any restriction has been removed" — the
  notification says the message stays deleted because they deleted it, and the F19
  record carries the same line BEFORE they decide whether to contest. (2) The
  self-delete path refuses while a contest of the REMOVE is pending, and its flash
  only claims a moderation decision exists when one actually does. (3) The GDPR
  export returns the author's OWN withdrawn words to the author — but NOT to a
  guardian on the ward path (`build_user_export(..., for_self=False)` keeps
  `[removed]`), because the guardian is a read-only observer and a child's
  affirmative withdrawal gets the most protective reading. **Owner-ratified
  2026-08-12**, together with two related calls: the self-delete refusal while a
  contest of the REMOVE is pending stands (accepting that no appeal-withdraw path
  exists, so it holds until a moderator decides), and `PostAdmin`'s editable
  `is_hidden` stays an operator escape hatch — with the consequence recorded at
  `apps/social/admin.py`, that an admin hide carries no provenance and so becomes
  indistinguishable from a self-delete once the author also deletes. (4) The
  export's own-post slice is
  newest-first with an explicit truncation marker. (5) An expired attachment whose
  post is hidden ONLY by the author's own deletion, with no standing REMOVE, is now
  reclaimed rather than exempted forever — it is nobody's evidence, and permanent
  exemption fails GDPR storage limitation (Art. 5(1)(e)). The REMOVE-then-self-delete
  order stays exempt. An admin manual hide is byte-identical in data to a plain
  self-delete once the author also deletes, so an admin hold that must survive the
  author's deletion needs a real REMOVE action.

## 2026-08-09 — DSA Art.17 redress correctness

- **DSA Art.17 redress correctness (2026-08-09).** Two defects on the statutory
  redress path are fixed. (1) An author self-delete and a moderator REMOVE both set
  `Post.is_hidden`, so granting an appeal republished content the author had
  withdrawn; `Post.is_author_deleted` now records provenance, `_reverse_action`
  declines the un-hide (auditing `moderation.reversal_left_hidden`) while still
  lifting the action, and migration `social/0039` backfills historical self-deletes
  from the `post.self_deleted` audit rows so the fix is retroactive. (2)
  `safety_record_for` prefiltered own content with `[:500]`/`[:1000]` id slices;
  because `Post.Meta.ordering` is `["created_at"]` those kept the OLDEST rows and
  dropped the NEWEST, hiding recent decisions from the Art.16/17 record and from the
  GDPR Art.20 export, and making them uncontestable from that surface (the contest
  form posts `action_id`). The activity slice was worse still — `Activity` declares
  no ordering, so its 500 were arbitrary and could differ between page loads. The
  three scopes are now queried separately (each `[:limit]`, merged newest-first)
  rather than OR-ed: PostgreSQL cannot BitmapOr across a SubPlan arm, so the
  single-filter form seq-scans the whole action table and its hashed SubPlan cannot
  spill. Content rows are locked with `select_for_update` on both the reversal and
  the self-delete path, so the two cannot interleave into a republish.

## 2026-07-26 — canonical /v1 client adopted (romania_scraper ADR-0069)

- **Canonical `/v1` client adopted (2026-07-26, romania_scraper ADR-0069), and it
  fixed a real defect.** This app's private `iter()` followed `next_cursor` until it
  was falsy with **no repeated-cursor guard**, and `max_records` defaults to `None`
  — so there was no bound of any kind. A server or bug echoing one cursor made it
  re-yield the same page forever. `iter_app_pack()` always had that guard; the
  product walk did not. Transport and pagination now come from the generated,
  stamped `apps/ingestion/sources/_roedu_client_core.py`, so product iteration fails
  closed with `RoeduContractError` on a repeated cursor. The app also **gains
  `pages()`**, which the private copy lacked entirely, making page-level
  snapshot/release metadata reachable. What stays local is this app's publication
  gate — redistributability, policy-attestation currency, venue/commerce/event shape
  validation, canonical pack naming, `iter_app_pack`/`read_app_pack` — because
  deciding what may be published is this app's decision, not `/v1` transport.
  `RoeduContractError` is imported from the core so the domain layer and shared
  paging raise one class. Hand-edits are caught by the `VENDORED_SHA256` stamp
  (`apps/ingestion/tests/test_roedu_client_vendored.py`, 10 tests). Because that stamp
  forbids local edits, the generated file is excluded from `ruff format` (and only from
  the formatter — `ruff check` still lints it); the canonical file is not format-clean
  under the producer's own ruff either, so a resync cannot settle it.

## 2026-07-16 — verification gates

- Fresh 2026-07-16 gates: Ruff 0.15.21 check/format and migration drift passed;
  the focused RO-EDU/lifecycle/public-projection suite passed 178 tests plus 27
  subtests; the full isolated PostGIS suite passed 2,672 tests with 30 skips and
  27 subtests; the producer→server→both-real-clients loopback passed 84 tests.
- No real network ingestion, deploy, or child-facing data mutation is part of
  these gates; consumer fixtures and the loopback serving projection are used.

## 2026-10-04 — Complete native Go backend candidate

Valid until: this native candidate, dependency or deployment contract changes — then requalify.

Owner scope is both Cat and Social, with Go as the serving language. This branch builds
on public foundation36cff2f, replaces the remaining serving paths with services/server,
and keeps Django as an offline contract/migration oracle. No live ingestion, provider
activation, real minors, Terraform, deployment or production data is involved.

The native assembly owns password/Google/Facebook identity, current session/token state,
signed EUDI/guardian/cohort verification, authoritative activity/voting/series/group/
connection/thread/sentiment/moderation transitions, E2EE transport, media, booking,
donations, notifications/discovery/recommendations, public catalog/SEO/feed/snapshots,
original HTML/locale/form rendering and twenty SPA hydration contracts. The27 due jobs
and17 manual handlers (plus one administrator alias) execute native services. Native
bootstrap adopts existing relational data; fresh empty and preinstalled spatial schemas
are qualified without resetting extension-owned rows. A secret-stdin administrator
bootstrap creates only fresh unknown/unassigned/unverified staff, with atomic audit and
no existing identity/consent overwrite.

Private media tests use actual AVIF/WebP/FFmpeg, real foreign keys and isolated PostgreSQL.
Prepared attachments are one-shot/atomic, failed or ambiguous blob writes first reach
durable cleanup, claimant permissions reload, and terminal video notifications contain
IDs only. Upload-triggered video work is single-flight/max2, follows commit, survives a
canceled request under application lifetime and stops before storage/database close.
Deferred erasure retains all excess blob keys across bounded batches (seven keys/batch2
regression). Open sockets recheck captured session/token authority and membership.

First full release-image fixture qualification:167 top-level tests across17 database/
codec packages, zero skips, `-race`, read-only sources and Python-free native image:
accounts11, admin5, app10, booking7, catalog6, commands8, discovery2, donations5,
export2, jobs18, media25, messaging6, notifications2, recommendations5, safety12,
social18, web25. Later affected app12, administrator bootstrap and command tests passed
with additional fixture assertions. Independent fixtures include42 markup goldens,
three timestamp/compressed-cursor vectors and20 Django SPA projections plus populated
card/privacy examples. No test count implies exhaustive automatic equivalence of all
source paths; native route inventory documents378 API operations, with public field
schemas complete and private DTO field documentation still incomplete.

Source and imported-package govulncheck1.8 scans pass both candidates after updating
Parquet's compress dependency1.17.9→1.18.7 (GO-2026-5841). x/crypto's unmaintained
openpgp module advisoryGO-2026-5932 has no fix and is neither imported nor called.
Stripped Go1.27 binary extraction reports module-wide wildcard symbols conservatively;
a same-source symbol-retaining static audit twin passes binary symbol analysis with
zero called/imported findings. The unused module advisory remains reported separately.

Default Docker/Compose/systemd/Render/cloud-init paths now invoke Go, with immutable
frontend/templates/locales, UID10001 and bounded codec tools. Native artifact export
requires exact SHA-256; compilation stays off the small host. TLS headers/logs preserve
source policy, forwarded identities/protocols require configured ingress CIDRs, local
liveness remains narrow and private readiness never bypasses HTTPS. Bounded structured
logs omit private raw paths, queries, IPs, identities, headers, cookies and bodies.

Policy boundaries are explicit: raw age/consent/cohort/membership/ciphertext/scanner/
payment CRUD and unrestricted generic deletion are unavailable; governed services own
those transitions. Domain/API/catalog rate histories remain process-local; source Redis-
required/Sentry profiles and unsupported nondefault policy settings fail by NAME, not
silently. Shared auth state/budgets, job queues and live fanout are PostgreSQL-backed.
These restrictions and privacy/auth/safety changes require human review before landing;
product/provider/legal launch gates remain. This record does not claim deployment.

Final frozen release-image fixture run:173 top-level tests across17 PostgreSQL/codec
packages, zero skips. Counts: accounts14/admin5/app12/booking7/catalog6/commands9/
discovery2/donations5/export2/jobs18/media25/messaging6/notifications2/recommendations5/
safety12/social18/web25. All native race/vet and source/package scans pass. The audit
binary keeps symbols (same source/static build); its linked-symbol scan passes with no
called/imported findings. Production artifact may strip symbols after that audit.
Portable auth snapshot pins canonical Cat commitaf85e7f and verifies all file hashes.

Native CI's initial database-image build exposed obsolete Bullseye repository URLs.
The local canonical database image now builds on official PostgreSQL16 Bookworm with
PostGIS/vector (PG16.15), preserving the major/volume contract. Native bootstrap of a
fresh private fixture passes; full contracts are being requalified on that actual image.
Official packaging sources: https://www.postgresql.org/download/linux/debian/ and
https://github.com/postgis/docker-postgis (the old16-3.5 tag documents Bullseye).
Aggregate release/HTTPS/resource receipts promoted to docs/reviews/native-go; no raw
transcripts, credentials or database contents were promoted. The tiny warm public-read
sample measured26,316KiB Go PID1 RSS vs157,700KiB exact-source Django; it cannot predict
capacity or hosting bills. No unapproved network expansion was performed.

Official checksum-verified Trivy0.75 image scan found CVE-2026-103111(HIGH) in existing
Debian libpcre2-8-0, fixed10.42-1+deb12u2. Runtime explicitly refreshes that package;
source https://security-tracker.debian.org/tracker/CVE-2026-103111. No Go behavior or
frontend assets changed. The rebuilt image is re-scanned before any release approval.

Final runtime images pass official checksum-verified Trivy0.75 with zero fixable HIGH/
CRITICAL findings: Socialb54fa1b3/Cat73be2310, PCRE12u2. Aggregate immutable scan receipt
is docs/reviews/native-go/image-security.json. CanonicalPG16.15 Bookworm freshfixture
runs all173 contracts/17packages with nofailures/skips. Exact Socialhead810492a has all
CI workflows green: Native37208738237, reference37208738213, publicGo37208738229.
Human review requested against PR101 under AGENTS61; no approval recorded, so no Social
landing or deployment. Cat codeabe9337 landed/deployed anonymous with151 public checks;
this does not activate Social providers/minors or alter the launch gates.

## 2026-10-04 — Owner-approved Go main landing and native documentation

Valid until: the runtime/launch profile changes — then reverify.

The owner explicitly authorized both conversions to origin/main and requested canonical
Go documentation. This satisfies Social's human auth/privacy/safety landing review; it
neither enables provider/minor/product launch nor authorizes infrastructure procurement.
[Corrected 2026-10-05: the owner authorized the landing only; no human code review happened.
Review pending, gates first deployment — ADR-0040.]
Go/native/ref/public CI on e6742a1 all pass (37211779358/395/372), including actual native
bootstrap/codecs/DB; the 17-package fixture is 173 top-level tests, not 193. The count was
independently recomputed from individual PASS logs. Runtime and account/adoption/scanning/
erasure configuration boundaries remain recorded in ADR-0032 and the native CLI guide.

Canonical README/agent/runtime/architecture/hosting/database/security/deferred-work
instructions now name the Go executable and packages; historical Django/reference checks
are explicitly separated. Python is retained as offline contract/content tooling, and
client-side TypeScript stays unchanged. No application code or live data changed in this
landing-doc slice. Product launch and the current process-local domain rate limits remain
explicit; unsupported profiles still fail by name.

The orchestrator fast-forwarded and pushed the qualified native implementation plus final
canonical documentation to Social origin/main3439a3b on2026-10-04. PR101 is merged; the
older public-only foundation is included by ancestry. Exact final documentation-head CI
also passes (native37219661498/reference37219661457/public37219661432). [Corrected 2026-10-05
(GOV-5): these and the 810492a/e6742a1 runs predate landed main cd006e3 (workflow/ADR-0033
change), which has no hosted run; they are not exact-head evidence for main.] Native startup,
auth/domain/media/live/jobs and deployment entry points execute Go; client TypeScript and
offline Python oracles remain explicit. Both repository main landing pads are clean.
No Social production deployment or provider/minor/source activation occurred.


## 2026-10-04 — native completion coordination and readiness guide

Valid until: the three completion lanes are integrated and qualified — then treat this coordination note as history.

The owner requested three separate sessions managed by the original chat. Separate task
worktrees cover shared PostgreSQL budgets, runtime configuration/error reporting, and guarded
permissions/private API schemas. Main remains clean at3ad5c3338b1ececcd770c47f63a990fd505d8ee1;
workers commit locally and the coordinator owns review/integration. No production activity was enabled.

The production-readiness guide now lists native release, data-adoption/rollback, ingress, provider,
private-storage/scanner, recovery, operations and product/legal gates. The complete earlier Python-era
plan is preserved in docs/archive/production-readiness-native-go-reference.md. This documentation
change does not claim qualification of the in-progress code. Doc gate: files=52 dead_links=0
stale_terms=0 retired_verbs=0 orphans=0; git diff --check passes.


## 2026-10-04 — fresh authority after rate preflight

Valid until: the combined completion candidate is qualified and reviewed — then treat as history.

ADR-0036 adds an authoritative account equality gate to platform.Participate. A captured
actor cannot retain active/identity/cohort/age/role/staff/superuser permission after those
fields change between separately committed transactions. Existing assurance/parental-consent
and privacy-withdrawal behavior is retained. Two isolated PostgreSQL race regressions
passed, including seven independently changed fields, expired assurance and withdrawn
child consent; no skips. Source is pending combined qualification and human safety review.
Doc gate files=52 dead_links=0 stale_terms=0 retired_verbs=0 orphans=0; whitespace passes.


## 2026-10-04 — baseline authority regression and native template cleanup

Valid until: combined completion qualification supersedes this baseline receipt — then treat as history.

All175 existing-plus-authority PostgreSQL/native-codec top-level tests pass with the
race detector and zero skips in the task-owned internal fixture. The first run exposed
a catalog fixture actor missing its database role, and the saved-search job exposed an
incomplete production actor projection. The catalog fixture, saved-search loader and
guardian capabilities loader now include actual current permission fields; no gate was
relaxed. Existing saved-search notice and guardian/browser contracts pass.

Native Render/cloud-init templates no longer assign retired DB_POOL_TIMEOUT,
ASGI_THREADS or DJANGO_SETTINGS_MODULE; the offline oracle templates stay separate.
Render YAML parses and name absence checks pass. The public API guide now distinguishes
session-cookie/auth CSRF from opaque-token authorization. This is source/template work,
with no deployed configuration or credential changes. Combined completion remains pending.

## 2026-10-04 — Guarded permissions and private API field contracts (ADR-0035)

Valid until: this review candidate is landed or superseded — then treat as history.

Only the feat/go-admin-api-parity worktree was mutated. The shared main landing pad,
accounts/app/main source, schema migrations and canonical shared auth were untouched.
ADR-0035 is proposed pending human auth/privacy review. Root coordinates source integration,
publishing, combined regression and landing; no push, deployment, real ingestion, scheduler,
provider/minor activation, production-data read or production verification occurred here.

Native account permissions are complete presets: user, role-only moderator, staff operator
and staff superuser administrator. A current active staff superuser with role admin and
current actual adult assurance (or audit-provenance-backed native bootstrap exception)
is the only permission manager. Actor/target reload and row locking, serialized concurrent
changes, current eligible remaining-manager checks and anti-self escalation protect the
transition. Grants require current adult proof; reductions can repair unsafe legacy state.
Only role/staff/superuser fields change. Existing sessions and API tokens are revoked on
both grants and reductions; audit failure rolls back permissions and credentials together.
Django group/individual grants do not enter native actor authority. Owned curated/event/
local action writes additionally lock/recheck current staff authority inside their transaction.
Safety sanctions and GDPR erasure can still restrict/delete any administrator. Existing
already-authorized delegated domain operations retain their transaction semantics.

The existing native admin account page exposes a CSRF-protected strict permission form
only to fresh eligible managers. Synthetic tests cover presets, stale/forged authority,
expired/missing/latest/mismatched proof, bootstrap provenance, last-admin self/mutual
concurrent demotion, credential revocation on grants/reductions, stable OAuth subject links
with fresh capabilities, audit rollback, legacy Django joins, private membership/contact
walls, unchanged child consent, no-ops and raw/duplicate form rejection. An added external
identity test initially used an unsupported provider name; its fixture was corrected to a
supported provider with a synthetic subject, with no external network call. Final suite passes.

Private OpenAPI describes all 378 native API operations, 316 registered paths and 145 component
schemas, correcting the earlier 330-path claim. Actual named DTOs supply reflected field
contracts; anonymous request structs and map/SQL/export projections have reviewed field
contracts. Coverage tests walk actual source registrations, match 19 anonymous request DTOs,
17 SQL projections plus 10 export sections, and validate 11 synthetic wire fixtures plus
nullability/JWK/recovery/negative listings/auth/media/status/security/privacy cases. Schema
construction supports nullable referenced values and nil slices, base64 JSON byte strings,
custom decimal inputs and explicitly domain-extensible JSON. No live person examples or
credentials are embedded. The schema contract tests caught and fixed nil-slice nullability.

Exact gates on the final source use Go 1.27.1 from
/mnt/data/decision-lab-runtime/kev-native/toolchain/go/bin/go with GOWORK=off,
GOMAXPROCS=2,GOFLAGS=-p=2,GOPROXY=off and GOCACHE/GOMODCACHE/TMPDIR entirely under
/home/dobo/work/_temp/go-admin-api-parity. Public pinned modules were downloaded there;
no system caches were written. `go -C services/server test -race ./...` and
`go -C services/server vet ./...` pass; the race suite needs approved loopback test sockets
(the default sandbox denied existing httptest listeners). Shared auth hash verification
`go -C services/server run ./cmd/check-authcore` passes. Targeted schema race tests also pass.

Task-owned go-admin-api-parity-test Docker network and go-admin-api-parity-db contain only
synthetic fixture rows, no published ports. The approved social-native-db:go-complete-20261004
was bootstrapped with the approved social-native:go-complete-20261004 native migrate-only
command. Race binaries compiled with `go test -race -c` for internal/admin,accounts,web ran
read-only/no-new-privileges/cap-drop=ALL as UID/GID1000 against that explicit disposable DSN,
with source read-only and TMPDIR under task scratch. All 62 top-level package tests pass:
admin 18, accounts 14, web 30; zero skips/failures. AVIF/WebP/FFmpeg codec image is Go-only.
Logs and test binaries remain in task scratch for coordinator review/cleanup after landing.

`python3 /home/dobo/work/agent-ops/scripts/check_docs.py .` passes:
files=52 dead_links=0 stale_terms=0 retired_verbs=0 orphans=0.
`git diff --check` and native gofmt checks pass. This local qualification is not production
verification and does not substitute for the human sensitive-code review/combined landing gates.

## 2026-10-04 — Native configuration and private error reporting worker

Valid until: integration/landing of feat/go-config-observability — then treat as history.

Implemented ADR-0034 policy/configuration and observability in the task worktree,
with rate foundation e0d937b cherry-picked as dependency c32d77f. The CLI now loads
validated policy controls into catalog/social/media/messaging/jobs and shared rate
maps. Required-shared mode asserts a coherent PostgreSQL service/store graph;
Redis and retired Python queue/worker/profile settings fail by setting name. Source
browser/API defaults, mandatory scanning/adult-only media, child venue gates,
consensus/report-retention floors and bounded presence privacy remain. Mutable
cohort/rate maps are copied before concurrent use. Catalog policy is request/job
context data, with no process-global mutation; HTML/forms/discovery/exports/media
and social shares receive the same typed visibility policy.

Optional sentry-go 0.49.0 error reporting uses one bounded producer worker and the
SDK's bounded asynchronous transport, with a two-second CLI shutdown budget.
The fixed event allowlist excludes request/body/identity/header/cookie/token/IP/query,
raw panic/error text, stack/attachments/breadcrumbs/contexts and telemetry integrations.
Recovered panics, generic 5xx and returned startup/one-shot failures are captured;
normal responses and disabled reporting remain inert. DSNs reject legacy secret
passwords and query/fragment data; environment labels are a fixed three-value enum.
SENTRY_ENVIRONMENT is the only newly implemented name absent from the fleet registry;
root registered deploy-host nonsecret metadata in agent-ops a7062e7. No values were
read or delivered. No actual Sentry outbound was used.

Native request/memory caps now bound ordinary non-file data and aggregate multipart
field bytes; file data streams under separate media caps. This documents native hard
cap semantics without claiming the Python buffering implementation. Existing token
maintenance also invokes shared Store.Prune(1000), preserving token-result shape and
starting no new job/scheduler. A synthetic test proves 1001 expired keys drain in two
bounded passes while a live key remains. Native qualification now includes the CLI
configuration binary with an explicit disposable DSN.

Verification uses Go 1.27.1 with GOWORK=off, GOMAXPROCS=2, GOFLAGS=-p=2 and task-only
GOMODCACHE/GOCACHE/GOTMPDIR/TMPDIR under _temp/go-config-observability. Native race/vet
and portable authentication hashes pass. Mock-only reporter/app/startup tests cover
privacy, default disablement, full queue drop, caller shutdown deadline, committed
response abort, and fail-closed API admission. Final configuration binary passes 33
synthetic/race-enabled tests, zero skips; actual VoteFact admits one configured vote,
refuses the second without mutation, and custom closure visibility stays isolated
from the default policy.

The full scripts/qualify-native.sh pass reached these zero-skip fixtures before a
known custom-clone blocker: configuration 32, accounts 16, admin 5, app 22, booking 7, budgets 9,
catalog 10, commands 9, discovery 2, donations 5, export 2, jobs 22 (141 tests; replacing the CLI
with its final 33-test receipt totals 142 completed tests). Media passed 27 tests but
its two old social-post fixtures fail closed because their bespoke LIKE clone lacks
schema.Migrate's local shared-rate functions/tables. Rate integration owns that fixture
repair; no permission/rate bypass was added, and complete final-head PG qualification
is not claimed. Earlier isolated non-rate policy suites passed 20 social / 29 media / 21 jobs
plus 19 catalog/messaging before rate integration. Root will qualify the complete merge.

Final read-only inventory review found browser/command readers still using source
constants. Those now use configured open-now/closure decay and thresholds, private
fact quorum, interest readiness, group/support cohort maps, presence windows and
thread-root pagination. Browser post models use bounded 1000-item query batches with
finite 1950 total input/order/deduplication. Targeted race fixtures prove two-page
configured-root cursors, 1000 roots plus an announcement, existing private read walls,
and custom report/fact/gauge/cohort/window projections. Their new tests pass with zero
skips. Demo venue selection uses the same context policy. Final targeted native race
checks pass for CLI/app/web/ops/jobs/commands; app recheck passes after restricting the
large request exemption to actual multipart uploads. Final full vet passes.

Docs: check_docs files=52, dead_links=0, stale_terms=0, retired_verbs=0, orphans=0. Source
format/whitespace checks pass. No production data, ingestion, scheduler, provider,
minor activation, deployment, shared authentication source, push or main merge occurred.


## 2026-10-04 — combined native completion qualification

Valid until: the runtime/policy/authorization contract changes — then treat as history.

The coordinator integrated all three lanes plus fresh authority/projection corrections.
All280 top-level tests across19 explicit native CLI/PG/codec lanes pass under-race with
zero skips; final broad race/vet/auth hashes/format pass. Release smoke exposed missing
schema/guide registration; explicit routes, protocol contracts and assembledApp regressions
now pass. Actual release emits380operations/318paths/166schemas, and schema/guide/readiness
all return200. Final image453f7048c53d1b9b65ea5fc498cf389d32b0a457ce37413467507e4ae57820d9
is Python-free, UID10001; graceful shutdown exits0 withoutOOM. Source/package/linked audits
and image fixableHIGH/CRITICAL gates pass. Unimported openpgp GO-2026-5932 is recorded
separately; nothing was suppressed. Exact aggregated evidence is
docs/reviews/native-go/completion-qualification.md. Hosted CI and human sensitive-code
review remain beforelanding; no production/provider/source/minor activation occurred.

agent-ops registered SENTRY_ENVIRONMENT as non-secret deploy-host metadata without values
or SOPS/repo_env delivery, commit a7062e70bf4a4f3cfb2be47f7862a9b5e32b1f64 verified onorigin/main.
Cat runtime/authcore needed no change. The anonymous Go game replica-affinity contract remains.


## 2026-10-04 — reconcile parallel governance landing

Valid until: completion PR landing — then treat as history.

Main advanced to cd006e3 with accepted owner-directed on-demand Actions policy ADR-0033.
The proposed shared-budget decision is renumbered toADR-0037 beforelanding; config/admin/
authority decisions retain0034/0035/0036. This prevents duplicate canonical ADR numbers
without changing runtime behavior. All completion code remains under sensitive-code review.


## 2026-10-04 — completion published for sensitive-code review

Valid until: PR108 is reviewed and landed — then treat as history.

PR108 contains the combined280-test qualified Go completion. Parallel accepted main
cd006e3 is integrated and ADR numbering is unique (0033manualActions;0034config;
0035permissions/schema;0036freshauthority;0037sharedbudgets; next0038). GitHub native
Actions visibly reports manually disabled; no hosted run/result is claimed and no
workflow or spending controls changed. Local race/vet/PG/codecs/audits/image/HTTP
qualification is complete. Human auth/privacy/safety review under AGENTS.md remains
before source landing. Worktrees/branches/scratch are retained because work is unlanded.


## 2026-10-04 — reuse existing sessions for independent review

Valid until: review fixes are integrated and qualified — then treat as history.

The owner requested inspection/reuse of existing sessions before creating more. The
three completed Social workers were reused for rotated independent review; Teacher and
raw-data-server sessions were already active on their separate projects and received
interface-coordination notes. No duplicate chat was created. Review confirmed a stale
profile-viewer disclosure path, media TTL/control policy mismatches, group creation UI
policy mismatch, and an inherited fixed-window budget/erasure deadlock. Scoped fixes
are underway in separate task worktrees using those same worker chats.

Coordinator group-creation policy now has one shared helper used by domain admission
and both web context paths. A real PostgreSQL/browser/SPA regression proves ordinary
adult creation is hidden and rejected when configured cohorts are empty, and is shown
and accepted when enabled. The targeted race fixture passed; no gate was relaxed.

## 2026-10-04 — Review fix for inherited fixed-window erasure deadlock

Valid until: integration/landing of fix/go-review-budgets — then treat as history.

The independent bounded review at f2424bed confirmed inherited accounts/safety
fixed-window expiry deletion held child budget locks before requesting the account
FK lock, opposing accounts.Erase's account-before-child order. A synthetic SQL-only
check returned erasure SQLSTATE 40P01, and a deterministic test calling the actual
Erase service also fails with 40P01 under a scratch-only old-source go-overlay.
This was not a defect introduced by the new shared sliding budget implementation.

Changed only accounts/service.go, safety/service.go and the matching inline unsafe
report path. Expiry sweeps commit independently, lock/skip at most 256 expired rows
under a 2 second budget, and release them before admission. Admission takes account
FOR KEY SHARE before its own budget row. Conditional UPSERT resets count and until
only for an expired actor row that may remain beyond the bounded sweep; live caps,
fixed expiry, independent account/safety debits and original clock behavior remain.
Unsafe repeats stay free and its report/budget/audit/guardian notices remain atomic
inside the original outer transaction. No standalone admission or nested pool acquire
is added there. Missing database/actor returns an error without admitting work.

Qualification uses Go 1.27.1 with GOWORK=off, GOMAXPROCS=2, GOFLAGS=-p=2 and all scratch/
Go caches under _temp/go-review-budgets. Full account fixture binary: 19 top-level
race tests pass; full safety fixture: 20 pass; zero skips/failures. The extra account
unavailable-state unit passes in the final affected-package race run. Focused tests
cover actual Erase with full foreign keys and two connections, bounded expiry with
locked-row skipping, expired saturated own-row reset, concurrent quotas/live expiry
and unsafe free repeats. The same actual-Erase test fails on exact old service source
from f2424bed via a scratch-only go-overlay, with safely logged SQLSTATE 40P01. The
repository source never switched back for that negative control.

Exact source checks: go -C services/server test -race ./internal/accounts ./internal/safety
-count=1; go -C services/server vet ./internal/accounts ./internal/safety; go -C services/server
run ./cmd/check-authcore (snapshot hashes verified); gofmt and git diff --check empty.
Fixture race binaries run only on isolated go-review-budgets-net/db using the existing
Python-free native codec image, read-only source/dropcaps/nnp/user 1000, explicit synthetic
DSN and task scratch. Doc gate files=52, dead_links=0, stale_terms=0, retired_verbs=0, orphans=0.

No shared sliding SQL, canonical authentication source, environment names, provider,
minor activation, real data/ingestion, scheduling, deployment, push or main merge changed.
Previous retained worktree/stash/scratch remain untouched. Fixture container is stopped
and retained after checks; root owns combined candidate qualification and human review.

## 2026-10-04 — Fresh profile authority after admission review fix

Valid until: fix/go-review-profile is integrated/reviewed/landed or superseded — then treat as history.

The independent review of PR108/f2424bed reproduced an inactive and changed-cohort viewer
receiving connected adult fields from a captured actor. This fix owns only the new profile
worktree and its _temp/go-review-profile scratch; original admin worktree/scratch remain
intact. Social.Profile now loads the full viewer row after the independently committed
shared profile-card debit and replaces captured flags before pair/tier/private projections.
No nested pool acquisition or new profile participation gate is introduced. Current target,
self, inactive/unassigned/cohort and mutual-block vetoes retain indistinguishable404s.

ADR-0028 distinguishes visibility and participation: withdrawn identity or expired/revoked
child consent retains minimal or liveSHARED same-cohort cards, while current canConnect
refuses connecting. Current minor pairs never receive adult interests/uploaded-photo
permission. Role/identity/cohort fields from the supplied actor cannot restore authority.
Credential revocation applies to later requests/live delivery; already authenticated reads
are not claimed to be retroactively canceled at every subsequent concurrent change.

Generic person templates had been given person instead of card/person_user. The new
populatePersonContext helper supplies aliases and the authorized active target ID for
report/block forms; only connected-adult full pages query a clean photo and use the EXISTING
native media metadata route, its current viewer/target/block/scanner gate and signer.
Hover/minor/stranger profiles keep generated avatars. A block introduced before signing
collapses the helper to404. The coordinator owns views.go and will apply the exact narrow
caller preserving person for SPA. Worker physical views.go is untouched; scratch
profile-views-overlay.json / profile_views_caller.go qualify that approved caller with
this implementation. This dependency is explicit, not a claimed standalone caller change.

Go1.27.1 targeted race binaries run in the qualified Python-free native codec image,
read-only source/cap-drop=ALL/no-new-privileges/UID1000, explicit task-only PostgreSQL16
PostGIS/vector fixture with actual foreign keys and shared admission migrations. Five
social tests and three web tests pass with zero skips/failures. Social tests use a real
profile-card admission trigger to change viewer state before fresh loading, cover forged
captured privileges/current minor clamps and identity/consent visibility, and compare
self/missing/inactive/blocked vetoes. Web tests exercise actual API/v1API/page/hover routes,
require positive rendered display/avatar/handle/context/interests (not empty200), and
check current minimal/shared consent/identity fields plus page-only photo authorization.

Exact commands use GOWORK=off,GOMAXPROCS=2,GOFLAGS=-p=2,GOPROXY=off and task-local
GOCACHE/GOMODCACHE/TMPDIR; SDK /mnt/data/decision-lab-runtime/kev-native/toolchain/go/bin/go:
`go -C services/server test -race -c -o SCRATCH/social.test ./internal/social`, then
`social.test -test.v -test.run '^TestPostgresProfile' -social-test-dsn SYNTHETIC_DSN`;
`go -C services/server test -race -overlay SCRATCH/profile-views-overlay.json -c
-o SCRATCH/web.test ./internal/web`, then
`web.test -test.v -test.run '^TestProfile' -web-domain-test-dsn SYNTHETIC_DSN`.
The schema/native release bootstrap uses the task-only go-review-profile-db on private
network go-review-profile-test; no host ports or real data are used. Targeted overlay
vet, native gofmt, whitespace and the52-file fleet doc/link gate pass. No whole280-test
rerun, hostedCI refresh, production verification, pushes/main merges, deployment,
real ingestion, scheduling, providers or minors were activated. ADR-0036 qualification
is appended; human auth/privacy/safety review and coordinator combined checks remain.


## 2026-10-05 — reviewed completion qualified

Valid until: runtime/policy/authorization changes — then treat as history.

The existing three completed sessions were reused and managed for cross-review and scoped
repairs; no duplicate session was created. Profile current-authority disclosure, person
context/photo wiring, effective media TTL/MIME controls, group creation affordances and
an inherited fixed-window/erasure lock inversion are fixed. All166 affected native fixture
tests pass together under-race with no skips. Combined303contracts across19lanes retain
passing qualification; unaffected137 were unchanged. Broad native race/vet/auth snapshots
and source/linked/image audits pass. Final image4a660c95cf61e035384969c62292d6598f82f3ba4fce3f2c6070e47810123a33
is Python-free/UID10001,0fixableHIGH/CRITICAL; real release HTTP/schema/readiness200,
380operations/318paths/166schemas, gracefulexit0/noOOM. Unimported openpgp advisory remains
separately recorded. Exact review receipt supersedes the earlier280/image453f7048 iteration.
Teacher/raw-data-server sessions were already active and received interface coordination
without duplicate consumer/pin changes. Hosted Actions remains manually disabled; no
workflow/spending/provider/production/minor/source activation. PR108 awaits required
human auth/privacy/safety review beforelanding; worktrees/stash are retained.
