# ADR-0051: Public original template-load tracing

Date: 2026-10-10
Status: targeted tests passed at5871df2; formatter-only recheck passed atbec6cf8; broader landing/group review pending

## Decision

Extend the test-only [original public producer](0050-original-public-gui-fixtures.md)
with a delegating Pongo loader. Each of the fourteen registered-handler cases
records the actual successful page/base loads and hashes of the transformed
streams consumed by Pongo. The delegate returns the same bytes and preserves
the native loader's path/error behavior. Each case creates its own original
renderer, so cached templates from another case cannot hide a missing load.

An optional reference flag reads the existing fourteen-body private capture.
Its manifest and exact body identities/hashes are bound to the committed
original checkpoint. Missing, extra, aliased, changed or nonprivate members
refuse; the reference is read again after rendering. No reference file is
rewritten, copied into the repository, normalized or accepted as candidate
golden parity. The checkpoint itself joins the producer's source bindings.

## Boundaries

This is actual loader/case wiring and custody of original output. It supplies
neither template-conditional percentages nor proof of a complete G0/G1 group.
Only the delivered core coverage/golden contract can provide those later.
Per-render nonces stay unchanged, and raw golden parity is explicitly unevaluated.
The native renderer, templates, policies, dependencies and existing assertions
remain unchanged; no templ/UI adoption or original-test retirement is involved.
Unsigned ownership and all privacy/live/minor/guardian holds remain in force.

## Verification

Authored pass-through/error and incomplete-load controls exercise the real native
loader. Synthetic corpus controls cover changed bytes, missing/extra members,
symlink aliases and wrong case identity; they are custody tests, not native HTML
captures. The original fourteen handler cases retain every existing assertion.
Source is authored only; managed compilation/tests/formatting have not run on
this successor. Exact-head landing remains subject to ADR-0040.

Managed source5871df2 subsequently passed23unique test actions with no failure,
skip or cache. All14native cases produced28actual loads, while20renderer inputs,
24selected source inputs and15original corpus files stayed unchanged. Overall
exit1 preserves the one-space GOROOTformat failure. Only that emitted ASCIIspace
is removed in the source successor; formatter-only recheck is pending, and the
23test results are not relabeled as execution of the whitespace successor.
