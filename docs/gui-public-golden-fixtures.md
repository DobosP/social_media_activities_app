# Original-native public GUI fixtures

[ADR-0050](adr/0050-original-public-gui-fixtures.md) records the producer boundary.
[ADR-0051](adr/0051-public-original-template-load-tracing.md) records test-only
load tracing and unchanged-reference custody.
[ADR-0052](adr/0052-released-public-golden-tooling-bridge.md) records the optional
released public normalizer bridge and its focused managed evidence.
The source is `services/server/internal/web/gui_public_golden_test.go`; existing
native routing, templates, translations and rendering are used without edits.

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
Raw original/current bytes are compared through the released API without writing
normalized baseline files. The manifest records byte equality, independent hard
findings/success and the combined diagnostic outcome separately. Binary/archive
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

## Continuation

The source lane is `feat/gui-app-migration-a`, separate from shared `main`.
Source764beb1 is the tested implementation; later checkpoint commits contain
docs/proof only. Existing original, loader and format receipts retain their
earlier source identities. Next source work needs parent review before starting;
each managed runtime needs a recorded fleet slot. Full native21, the unsigned
84+7 matrix, private applicability and1651retirement holds remain unresolved.

Task-private keepers are under workspace `_temp/feat__gui-app-migration-a/social_media_activities_app`:
the original `capture-75dc717`, latest `capture-764beb1`, reused `sdk-3b27357`,
and `evidence/normalizer-r7-764beb1` with the actual ELF/streams/build information.
No raw body or ELF is committed. The failedr6metadata/streams and superseded5871
capture are deterministic private archives, with every member's bytes/fullmode
roundtrip-verified before those two superseded raw copies were removed. Current
original/latest data, source packets and published branches remain preserved.
