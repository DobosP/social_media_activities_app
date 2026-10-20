# ADR-0065: Native notification inbox presentation

Date: 2026-10-20
Status: independently feature-qualified — exact496 affectedPG7/desktop6+6; compact keeper and reviewed rolling retention, feature branch only.

## Scope

Rebuild existing `/notifications/` with cached escaped GoHTML and a scoped new
stylesheet. Keep current `/notifications/read-all/` POST action and native Go
services; no new endpoints, SQL, audits, schema, dependencies, provider or role
policy. Native preference pages and all 31 accepted previous bodies remain.

Typed projection consumes only authorized ordered notice display fields and the
current request Actor/default shell. It does not assume AccountView-only user
keys, serialize raw records/IDs, infer age/guardianship or add verification gates.
Original 100-row bound/order, read_at presence alias, optional reason/body, source
Bucharest timestamp and STRICT original safe_href policy are retained. Unsafe
URLs lose only their link. Native forms work without JavaScript. Explicit SPA
selection and its existing separate output stay unchanged.

The source single self-only read-all UPDATE, all-row effect beyond 100,
idempotence, CSRF/Origin, failure propagation and 303/fetch behavior stay exact.
Mandatory system/moderation delivery, optional mod_alert/arrival choices, presence
expiry and protected retention policies are unchanged. Opening a page never
marks a notice read. Existing authcore/current-active-account gates stay intact.

## Evidence and preservation

Accepted source contract review 670f9b38 binds runtime e900960. Clean published
base 05e5a522 has only 18 closing docs/keeper differences; rebind 07ef1b14 preserves
133 refs, all 287 old tests/6,878 assertion-call mentions and 31 actual old bodies
from keeper 29a9ff. Three narrow source-reader adapters restore complete former
bytes before the unchanged 32/31/27 controls. Current live readers require exact
new source, and new negative tests refuse former/missing/duplicate/modified/rehashed
bytes. Original HTML/assets/catalogue, backend/query/auth/audit/schema/dependencies
and all original assertions remain byte-exact. No frozen Pongo page rendering is
used to claim new native proof; the strict URL filter comparison is test-only.

New pure display/malformed/escape/header tests and a standalone automatically
registered App root/six finite leaves are applied after independent source review. App requests use
real login cookies through current App middleware; the final capture forwards the
same original API GET exactly once and whitelists display fields. No extra private
query, Actor/session/CSRF/nonce/raw ID is emitted. A registered direct-handler HEAD
is distinguished from net/http wire body suppression. Complete affected PG,
no-JS/desktop/Axe, all 31-body equality, measurements and keeper remain pending.

## Boundaries

Applied source and methods are independently source-reviewed (75a528c4/96601b3c).
This checkpoint is not successful runtime qualification, a
complete 21/current-image/main/Social milestone or deployment. Government/provider
connections, external delivery and real-child use remain separate unapproved
activation work. No privacy/clinical/content/retention policy ruling is made.
Optional warmed body measurements must report regressions and cannot establish
HTTP/SQL/throughput or a generalized efficiency improvement.

## Applied-source checkpoint

Reviewed 20-path product/method batch is applied. Focused web race checks pass
13 roots/72 named cases with zero failures/skips; App/web vet, five absent-overlay
compile checks, authcore snapshot, full native format, Python/Node syntax,
whitespace and all61-document guard checks pass. These are local checks, not
complete affected PostgreSQL, browser, previous-body or measurement acceptance.

## First actual PG attempt and assertion repair

Exact f087480 completed web199roots/863named without failures/skips, then the
complete App package failed only the new inbox date assertion and its parent:
48roots/96named RUN,94PASS. The other five new leaves and all existing App cases
passed; the remaining five packages did not run. All five created container IDs
and names were removed and the private PostgreSQL stopped. This is a failed
partial campaign, not complete affected qualification; original streams remain.

Actual native-render diagnostic confirmed that GoHTML safely emits the timezone
plus as an HTML entity; the decoded value is exactly the expected Bucharest ISO
timestamp. Independent5973e72e approves NEW test-only patch9aac0388: require one
actual marked time attribute and compare its decoded value with the same full
timestamp. Read-state/date/body assertions, product escaping/rendering, all old
tests and original resource/time limits remain unchanged. Actual failure review
adf73e82 and exact diagnostic are retained; fresh complete seven-package
qualification, UI, prior-body checks and measurements remain pending.

## Second actual PG attempt and isolated notifications bootstrap

Exact0e31da9 completed all original six affected packages:442 roots/1,327 named
passes with no failures/skips and six owned database retirements/checkpoints.
The notifications reference role then failed before its native package ran:
the frozen original helper accepts only the six earlier database suffixes and
returned `explicit isolated qualification DSN required`. All14 created CIDs and
names were removed and the private PostgreSQL stopped. This is another failed
partial campaign, not complete seven-package qualification. Originalf087 App
assertion failure and0e31 bootstrap rejection remain distinct immutable history.

An additive qualification-only notifications bootstrap accepts only
`host=/pgsocket port=55434 user=app dbname=social_family_<12-lowercase-hex>_notifications sslmode=disable`.
It uses the same canonical `catalog.New(db).Migrate`,30-second context and JSON
success contract. Pure grammar/flag and early invalid-run controls require no
live DB; separate unit/vet/build receipts precede the new reference role. Only
notifications selects the added binary. The original helper and six earlier
bootstrap/runtime/build/vet commands, all product/service/tests/schema/deps,
seven inventories/fifteen final CIDs/seven retirements and all caps/timeouts
remain unchanged. Extractor admission extends its exact method-input set only
with the two new helper files. No skip/filter, fixture reset or widened DSN.

This source checkpoint makes no new test or runtime PASS claim. Root must review,
apply, perform cheap checks and commit before one fresh complete seven-package
qualification and the actual UI/no-JS/Axe/31-body/capture/measurement work. Both
failed raw histories remain; no retention is eligible before accepted successor
custody. No full21/main/milestone/production/provider/real-child activation.

## Applied isolated-bootstrap checkpoint

Independent41e1774a approves the additive nine-path repair. Root applied exact
images, mechanically formatted only the NEW helper test, and ran the complete
new helper package with race:3roots/41named, zero failures/skips. Helper vet,
format/whitespace and Python syntax pass; no live database is used by these pure
controls. Original304tests/serving code/old helper/deps/assets remain unchanged.
Commit the reviewed applied images before the complete seven-package method;
no PG/UI/measurement or successor retention acceptance is claimed here.

## Reviewed inline-link distinction repair

Exact3e536 complete seven-package qualification passed446roots/1,338named cases,
zero failures/skips; all15containers/seven databases/privatePG cleaned. Independent
5dfcd accepts that source epoch. Its first primary UI runbd606 completed6JS/6no-JS
states and31 byte-exact prior bodies but retained12 Axe link-in-text-block
violations in four states across three menu modes. Benchmark was requested but
not reached. This remains failed UI evidence.

The owned inbox stylesheet adds a persistent underline to its inline View links.
It changes no palette, original asset, Go/template/service, test or qualification
method. The causal diagnostic0177 runs the same fixtures and18 unchanged strict
Axe audits with the candidate CSS: all audits are issue-free, while all12 exact
old-CSS guards deliberately fail. That diagnostic is not qualification. The
reviewed presentation fix requires a fresh complete seven-package run on its
committed CSS source followed by the second primary UI/measurement attempt. Both
failed PG histories and the failed first UI remain retained; successor acceptance,
keeper and rolling retention are pending. No broader gate or activation claim.


## Final exact-source bounded qualification

Exact496056a passes all seven complete affected packages:446 root passes/1,338
named cases, zero failures/skips;339 waited commands/693 artifact rows,15 owned
containers/seven databases/private PostgreSQL cleaned. Independentdb593 accepts
the source/command/case/cleanup record. Desktop6JS+6no-JS passes18 unchanged strict
Axe audits/375 predicates,31 byte-exact previous bodies,66 assets/78 local GETs
and owned cleanup; independent565b accepts the actual record. Actual same-GET
100-row EN/RO display capture retains only the reviewed presentation whitelist.

Independent9a04 accepts12 matched warmed body samples and their regressions:
median elapsed timing80.76%EN/113.10%RO higher,allocated bytes28.18% higher and
HTML46.49%/45.79% larger; allocation count5.16%/5.14% lower and gzip7.55%/10.02%
smaller. These are descriptive elapsed body measurements, not CPU profiling,
HTTP/SQL/throughput or a general efficiency improvement. Date-formatting source
analysis is an unqualified separate follow-up; it cannot recast these results.

[Canonical keepere007](../reviews/gui-native-notification-inbox/README.md) retains
2,695 members/8,921,346B with exact bytes/mode/UID/GID/nsmtime, all73 current UI
artifacts and distinct failed/partial/diagnostic/source-method histories. Independent
keeper205b and bounded feature38ab accept this source epoch. The outer retention
record separately documents removal of8,863 owned superseded generated files,
967 empty directories and1,278,664,437logicalB after accepted custody. Deleted
source/binary/duplicate Git stdout raw paths are unavailable; all historical
logs/PNG/HTML/proof/source packets/Git refs/canonical keepers and the latest496
source/binaries/UI remain. The first refused retention dryrun and causal observer
diagnostic remain failures; explicit host-view metadata rebinding preserved strict
ownership guards before the accepted dryrun and execution. No policy changed.

This is bounded feature acceptance on the feature branch. No all21/current-image,
main/milestone/production/provider/government or real-child activation follows.


## Page-local date resolution follow-up — source proposal

The current496 feature and compact retention are independently accepted and
published by closingcb68b6d. The original twelve warmed body measurements retain
all elapsed/allocation-byte/HTML regressions. This separate runtime-source change
resolves Europe/Bucharest lazily once per page, converts each timestamp once and
formats the exact fixed display/RFC3339 pair. Generic djangoDate, producer,
self/newest100/mark-all/permissions, timestamp validation, original tests, markup,
assets and qualification methods remain unchanged.

Equivalence requires stable resolver outcome/availability and tzdata during a page render. A transient first failure stays in per-row source offsets for the rest of that page; the old helper could recover on a later per-field call. The next page retries. This intentional page-local outcome is not unconditional transient-failure parity.

The new four pure test roots cover the frozen helper's output for winter/summer,
DST transitions, offsets/subseconds/rollover/zero, deterministic persistent load
failure, fresh-page retry and independent concurrent pages. Source formatting
and exact text inverses are recorded; compilation/tests/full affected7/UI/Axe,
all31 previous plus6 current inbox body identities and matched measurements
remain to be run on the committed source. Source-level reductions from200 to1
zone loads and500 to200 Format calls at100 rows are hypotheses, not measured
causal shares or performance results. No global cache, original assertion/gate
weakening, main/milestone/production/provider or real-child activation follows.


## Applied date-source checkpoint

Independent5379fe2a accepts the rebound six-path source over publishedcb68b6d,
including the explicit stable resolver-outcome/tzdata compatibility limit.
Root applied exact two Go images; all305oldtests/11helper-template-method witnesses
remain byte equal. Focused race9roots/56named has zero failures/skips; App/web vet,
format, whitespace and63document checks pass. These are local checks. Commit exact
source before unchanged full affected7, desktop/no-JS/Axe/37body verification and
matched body measurement; no new performance or complete qualification claim.
