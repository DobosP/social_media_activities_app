# Original-native public GUI fixtures

[ADR-0050](adr/0050-original-public-gui-fixtures.md) records the producer boundary.
[ADR-0051](adr/0051-public-original-template-load-tracing.md) records test-only
load tracing and unchanged-reference custody.
[ADR-0052](adr/0052-released-public-golden-tooling-bridge.md) records the optional
released public normalizer bridge and its focused managed evidence.
[ADR-0053](adr/0053-filesystem-original-renderer-boundary.md) records the additive
filesystem-backed original renderer and its focused partial evidence.
[ADR-0054](adr/0054-public-external-script-nonce-binding.md) records the two
existing external-script nonce additions and explicit original-fixture delta.
The source is `services/server/internal/web/gui_public_golden_test.go`; existing
native routing, translations and rendering are used; template edits are limited
to those two nonce attributes.

Run inside an owner-approved managed Go1.27.1 runtime with CGO enabled for race
checks, its task-owned cache and TMPDIR, from `services/server`:

```sh
go test -mod=readonly -race -count=1 -json ./internal/web -run '^TestGUIPublic'
```

To retain actual original HTML, append
`-gui-public-golden-output /absolute/private-parent/new-capture-directory`.
The parent must already exist with mode0700. The destination must not exist.
No command here authorizes host Go, a new image, DB, hosted workflow or service.

To bind the existing fourteen-body original capture, also append
`-gui-public-golden-reference /absolute/private-original-capture-directory`.
The original directory must remain mode0700 and contain exactly the fifteen
mode0600 files bound by the committed original checkpoint. This reads originals
before and after rendering without modifying or copying them. Omission is
recorded as `original_reference_verified: false`, never implied acceptance.

The matrix contains14 captures: privacy and terms in EN/RO with default and
contrast/larger/reduce settings; open-data in EN/RO with and without a synthetic
regular snapshot manifest; anonymous landing in EN/RO. All requests use the real
registered native mux. No account/session/PG/provider fixture is installed.

Outputs are14 raw HTML files and `manifest.json`, private mode0600. The manifest
records actual response/body hashes, current source/locale bindings before and
after rendering, actual original page/base loader streams, and scenario assertions.
These stream hashes cover the transformed bytes consumed by Pongo; the separate
source bindings retain the raw template hashes. A failed case keeps a failed manifest
and available bytes. Raw nonces are not printed or replaced; future canonical
golden comparisons must use the kit's approved attribute-only masks, not a local
body rewrite. Original public HTML is not a deterministic byte fixture until
that separate comparison contract is installed and qualified.
The successor explicitly records raw golden parity as unevaluated. Template-load
closure is not the plan's formal template-conditional coverage metric.

This is neither a full G0/G1 matrix nor template-conditional coverage. The
historical `_breadcrumbs` hold, unsigned84+7 ownership, protected paths, native
authcore/inline moderation separation and1651 retirement failures remain as in
[capture preparation](reviews/gui-capture-preparation/README.md). No owner
signature, Django parity, Native Live, templ/UI adoption or deployment is claimed.

[Focused checkpoint](reviews/gui-public-original/checkpoint.json): source75dc717
passed2top-level tests and14subcases with race/count1, no failures/skips/cache.
All14private body hashes/modes and18renderer input hashes were verified. The
latest raw setup remains task-owned; the superseded failed fixture run is compact
history. Full native qualification and independent landing review remain pending.
[Loader/reference checkpoint](reviews/gui-public-original/trace-checkpoint.json):
source5871df2 passed4top-level tests and19subcases (23unique actions, no
failures/skips/cache). All14native cases recorded28actual page/base loads;
20renderer/24selected-source bindings and the original15files stayed unchanged.
The overall command exited1 solely because GOROOTgofmt found one extra space.
The source correction is exactly that one-byte removal. Its
[formatter-only checkpoint](reviews/gui-public-original/format-checkpoint.json)
atbec6cf8 exited0 with empty stdout/stderr;24selected-source and15original corpus
files stayed byte/mode-equal. No test/capture/dependency action was repeated, and
the23tests remain bound to5871df2. The original checkpoint
and bodies remain unchanged. No full native/group/Live/parity/conditional result
follows from these focused checks.

The additive `tools/gui-public-golden/normalize.go` command imports the released
public `golden.Normalize` with strict empty options. It receives bounded raw HTML
on stdin and returns base64 canonical bytes plus separate hard findings in JSON;
it refuses on any hard finding. Synthetic controls live beside it. No standalone
application module/lock is introduced. An owner-approved managed preparation
must verify/extract canonical `web-kit-go-core-v1.6.tgz` against all41rows in
`tools/gui-public-golden/release-bindings.json`, retain the original module/sum,
and copy only the two command sources into `web-kit/cmd/social-gui-normalize/`.
That private tooling module can run its focused race/count1 tests and build the
command with `-mod=readonly`; preparation must retain actual command streams,
binary SHA256/build information and before/after source/archive bindings.
There is no host Go fallback or authorization to execute before a runtime slot.

After that genuine build, the original producer accepts the three flags together:
`-gui-public-normalizer-binary /private/absolute/tool-binary`,
`-gui-public-normalizer-sha256 <actual-managed-build-digest>`, and
`-gui-public-normalizer-archive /private/absolute/web-kit-go-core-v1.6.tgz`.
The existing original-reference flag is mandatory for this optional path.
Raw original/expected-delta/current bytes are compared through the released API
without writing normalized baseline files. The current manifest requires original
canonical inequality and expected-delta equality, independent hard findings/success
and all three hashes; the original attributes were absent. Binary/archive
hashes are checked again after all cases. Omission supplies no normalization
claim. [Normalization checkpoint](reviews/gui-public-original/normalization-checkpoint.json):
source764beb1 passed formatting/build,21helper actions and23app actions, each
once with no failures/skips/cache/race. Fourteen original/current diagnostics
were hard-free/equal.28source/41released-SDK/2owned-command/15original-file
guards and both archive copies remained byte/fullmode-exact. Parent independently
cleared the actual result within original-native public repeatability scope.
The compiled command's module is honestly `(devel)` in the verified private SDK
copy; archive/source/binary hashes bind it to the selected release. No normalized
baseline, formal golden/Live/group result, templ adoption or retirement follows.

The new `NewRendererFS(assetRoot, files)` boundary keeps original Pongo rendering,
transforms and catalog parsing while reading templates/catalog only from the
trusted supplied filesystem. The default disk constructor, production assembly
and routes stay unchanged. Arbitrary FS immutability is the caller's contract;
the test producer uses six exact source-hash-bound snapshots and checks them
after rendering. Root-based assets/private pages remain outside this subset.

`TestGUIPublicOriginalCapture` still runs every original scenario/assertion.
`TestGUIPublicFilesystemCapture` adds the same14registered-handler scenarios
using the new constructor and the existing released-normalizer flags. Optional
`-gui-public-golden-fs-output /absolute/private-parent/new-fs-capture-directory`
retains its own14raw bodies/manifest separately from the disk output and original
reference. Root-relative include/extends, bounds/overflow, read/close errors,
catalog parsing and genuine host-fallback refusal controls are additive.
[Filesystem checkpoint](reviews/gui-public-original/filesystem-checkpoint.json):
source8bdbb4e passed formatting and63app actions (12top/51subcases, each once,
zero fail/skip/cache/race), retaining all23prior identities. Both14-case sets
were hard-free/equal to originals through the released normalizer, and all14
disk/FS canonical hashes joined.56template loads, six FSsnapshot/source joins
and30source/41SDK/2owned-command/15original/archive/binary guards were verified.
Parent independently cleared the partial result. The normalizer helper/archive
and actual764binary were reused unchanged; no helper/download/build was repeated.
ArbitraryFS immutability remains the caller's contract. No fullM1 or production
switch, host-asset/private-page, formal oracle/group/Live/templ/retirement result
is implied.

The current source adds the existing `request.csp_nonce` only to `hovercard.js`
and `site.js` external tags. Native disk/FS tests cover fresh actual header/tag
binding, anonymous/authenticated presentation attributes, real response mutations
and the exact two-attribute template inverse. These20new actions bring focused
app discovery to83. Historical source7d stopped at a formatting-only failure;
its exact one-file correction became testedsourcef010. Paths/load attributes,
nonce provider and CSP mode stay unchanged.

The original14raw bodies remain the reference. Only an in-memory expected copy
adds the two nonce attributes with a synthetic marker; it is never a response or
new baseline. The released SDK and masks stay unchanged. Each diagnostic requires
all three successes/hard-free outputs, `original_normalized_bytes_equal: false`,
`expected_delta_normalized_bytes_equal: true`, three canonical hashes and exact
original/expected raw hashes. Existing63-action evidence retains source8b and
its historical equality-only schema; no historical receipt is rewritten.

After approved managed execution, the read-only capture DATA verifier is:

```sh
python3 tools/gui-public-golden/verify_nonce_capture.py \
  --checkpoint /absolute/repo/docs/reviews/gui-public-original/checkpoint.json \
  --original /absolute/private/original --disk /absolute/private/new-os \
  --filesystem /absolute/private/new-fs --binary-sha256 ACTUAL_MANAGED_BINARY_SHA256
```

It requires exact15-member private inputs,14ordered cases in each current mode,
the new strict flags/hash relations and anonymous native nonce-binding reports;
it refuses the old equality-only schema. It emits only a DATA summary and cannot
replace separate actual Go counts/streams, source/image/archive/binary proof.
Atf010, formatting,83app actions (16top/67subcases, each once, zero
fail/skip/cache/race), both14-case captures and this strict DATA command passed.
All63prior action identities remain. All28three-way comparisons are hard-free:
original canonical output differs and expected-delta output equals current;
84success fields from the executed three-call path are true. All14OS/FS hashes
join and28capture header nonce hashes are distinct, with two scripts bound each.
The actual separate disk/FS anonymous/fictional-adult controls render twice to
require freshness; they establish no account/session/eligibility/privacy admission.
All94input guards,56loads and sixFSsnapshot/source28 joins remained exact.
The actual764helper binary/SDK was reused without dependency/build/helper-test
repetition. Parent independently cleared this scoped result; the
[nonce checkpoint](reviews/gui-public-original/nonce-checkpoint.json) retains
exact streams, hashes, counts and cleanup records. Wider qualification remains
pending; each later runtime still needs a recorded parent slot.

## Continuation

The source lane is `feat/gui-app-migration-a`, separate from shared `main`.
Sourcef010e0d is the tested nonce delta over the earlier8b partial FS implementation.
The separate current source section adds `StaticHandlerFS(files)` for a trusted
filesystem rooted at the static directory. It shares the existing native request,
MIME/cache/ETag and `ServeContent` path with the default OS constructor. Application
assembly, `--site-root`, templates, nonce policy, SDK and dependencies stay unchanged;
no filesystem transport is activated by this additive API.

The supplied FS owns only relative release-asset names. Its confinement, stability
and lifetime remain the caller's contract. A regular seekable input must have a
nonnegative size no larger than the existing64MiB limit, correct end/start seeks
and exact bounded reads including one overflow byte. The underlying file closes
once before any asset header/body is published. A request-local memory snapshot
then uses the common serving path; the FS path buffers at most64MiB plus one byte
per request. No aggregate data cache or new HTTP cache policy is introduced.

Authored focused tests bind snapshots of actual `static/css/base.css`,
`static/js/site.js` and `static/js/hovercard.js`. They compare OS/FS bodies and
all headers for GET/HEAD/range/conditional requests, preserve the existing
immutable prefix and empty-file behavior, prove supplied/missing FS isolation,
and exercise method/path/regular-size/read/seek/one-close refusals. Short/overlong
streams and correct positions with seek errors are independent negative controls.
The historical e230 preflight returned1 with1075B of formatter diff and no stderr:
two alignment lines each needed two spaces. Compiler and62tests did not run;
all38source inputs and owned cleanup passed. The exact four-space successor is354.

At source35441ad, actual read-only GOROOT formatting returned0 with empty streams,
then the focused route passed:
`go test -mod=readonly -race -count=1 -json ./internal/web -run '^TestGUIPublicStatic'`
The [static FS checkpoint](reviews/gui-public-original/static-fs-checkpoint.json)
records62unique actions once (4top-level/58subcases), packagePASS1 and252JSON events,
with zero failures/skips/cache/race reports and all38source bytes/fullmodes unchanged.
The pinned2e040 runtime used4CPU/6GiB, network none, read-only source and existing
private caches. Its container exited0 without OOM; removal of the owned CID
returned0 and both that CID and exact name were absent. Parent and program reviewer
accepted only this focused opt-in transport result. This later docs checkpoint
does not create another test execution.

Existing83nonce actions, original75dc/latestf010captures and actual764normalizer
retain their separate proof sources. No83/14recapture, helper21/dependency
acquisition or normalizer build was repeated. Each later runtime needs a recorded slot.
This section does not install an embedded release, switch defaults, qualify
whole delivery/M1, or alter private/auth/group/Live/device/retirement gates.

The normalizer binary and
its21helper action proof retain actualsource764beb1. Subsequent checkpoint
commits contain docs/proof only; earlier receipts keep their source identities. New
semantic work needs parent review before starting;
each managed runtime needs a recorded fleet slot. Full native21, the unsigned
84+7 matrix, private applicability and1651retirement holds remain unresolved.

Task-private keepers are under workspace `_temp/feat__gui-app-migration-a/social_media_activities_app`:
the original `capture-75dc717`, latest `capture-f010e0d-os`/`capture-f010e0d-fs`,
reused `sdk-3b27357`, `evidence/nonce-r11-f010e0d` and the one actual ELF/build
information in `evidence/normalizer-r7-764beb1`.
No raw body or ELF is committed. The failedr6metadata/streams and superseded5871
capture are deterministic private archives, with every member's bytes/fullmode
roundtrip-verified before those two superseded raw copies were removed. Current
original/latest data, source packets and published branches remain preserved.
The accepted failedr8formatter evidence/proposed afterimage and superseded764
raw experiment are also deterministic private archives, with every regular
member's bytes/fullmode and directory mode verified before removing only those
two raw copies. Archive identities are in the filesystem checkpoint. The one
binary/SDK/cache setup, original corpus and latest disk/FS pair remain present.

After parent actual nonce review and verification of the latest/original keepers,
only the superseded8b disk/FS raw pair was archived together (37,369B). All30files
and both directory modes round-tripped before removing those two raw copies;
archive/index hashes are in the nonce checkpoint. Historical8b proof and all
unmerged source packets remain. The7d formatter failure remains sealed separately;
its overall1 is not relabeled as the successfulf010 execution.

The latest static transport keeper is
`evidence/static-fs-35441ad-5f7f858c-782a-4d33-8568-cfdd9880c0a6`.
The earlier small e230 formatter failure and all source/method packets remain.
Only compact execution records are promoted, with no workspace/dependency/SDK/ELF
duplicate. Result/closure control filenames retain their original private-keeper
names; the checkpoint states which exact bytes enter Git. No raw HTML or context
is promoted.

## Community graph fallback checkpoint

At source `a8b9dc5`, the planned M1 externalization replaces only the fallback's
inline `padding:1rem` with `graph-fallback` and one graph-scoped `1rem` CSS rule.
The source inverse preserves every other widget byte and all existing CSS bytes
and order. Fallback copy/link, capability guards, fetch and graph/cohort logic
are unchanged; the vendored library's separate style injection remains unresolved.

The [focused checkpoint](reviews/gui-public-original/community-fallback-checkpoint.json)
records actual pinned, network-none, source-read-only Node execution: all14named
VM/source-CSS cases passed once, zero fail/cancel/skip/todo, all25source byte/fullmode
guards equal. The owned4CPU/6GiB container exited0 without OOM; removal returned0
and exact CID/name absence passed. Parent and program reviewer accepted this scope.
The compact result, raw TAP, exits, ownership/cleanup controls and closure retain
their exact bytes/fullmodes. No workspace, dependency, cache, SDK, binary or rawHTML
is copied. This later docs commit is distinct from tested `a8b9dc5`.

A prior wrong local dispatch path exited2 before the reviewed method, tests or
container started. Its exact erratum is retained as dispatch history; its later
record timestamp is not an action timestamp. Correct method `f11115ae` then ran
invocation `f220a42b` once. The wrong command is neither a failed Node run nor a retry.

These tests model DOM-like inputs in VM and assert the literal CSS mapping; they
provide no computed-style/browser-CSP, cohort/privacy, app/full or adoption proof.
Original14/latestf010captures, nonce83/staticFS62 receipts and the sole764normalizer/
SDK retain their separate sources. No recapture, dependency install, prior test
rerun or new normalizer build occurred. Wider ownership/Live/device gates remain.
