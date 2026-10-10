# Original-native public GUI fixtures

[ADR-0050](adr/0050-original-public-gui-fixtures.md) records the producer boundary.
[ADR-0051](adr/0051-public-original-template-load-tracing.md) records test-only
load tracing and unchanged-reference custody.
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
The source correction is exactly that one-byte removal; its formatter-only
recheck is pending, and the23tests remain bound to5871df2. The original checkpoint
and bodies remain unchanged. No full native/group/Live/parity/conditional result
follows from these focused checks.
