# Worklog — social_media_activities_app

Append-only, newest first. Current truth is `STATUS.md`; this file holds the dated detail
`STATUS.md` summarizes.

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
also passes (native37219661498/reference37219661457/public37219661432). Native startup,
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
