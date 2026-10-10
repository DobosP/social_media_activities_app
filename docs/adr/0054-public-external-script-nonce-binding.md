# ADR-0054: Existing public external-script nonce binding

Date: 2026-10-10
Status: focused managed checks passed atf010e0d; independent actual review clear within the bounded nonce section

## Decision

The two existing external scripts in `templates/base.html`, `hovercard.js` and
`site.js`, receive the existing `request.csp_nonce` attribute. The actual native
renderer already creates that request nonce and puts it in `script-src`.
The source change adds exactly these two attributes. Script paths, `defer`,
the `site-js` ID and authenticated-only `data-meetups-owner` condition stay exact.
Nonce generation, CSP report-only/enforced selection, routes, auth, JavaScript,
template engines, modules and SDK sources are unchanged. This is the bounded
planned CSP implementation section explicitly assigned by the GUI parent.

## Evidence contract

Add native renderer tests for disk and supplied-FS paths, anonymous and fictional
adult presentation contexts. Two responses from each renderer must have fresh,
nonempty header nonces and exactly bound external tags. The authenticated context
uses the existing native view mapping; it establishes no account/session,
eligibility, guardian or privacy admission. Real response mutations test missing
or mismatched nonces, changed load attributes/paths, unexpected anonymous owner
attributes, duplicate scripts and missing CSP. An exact template inverse requires
the original13594-byte base hash after removing only the two nonce additions.

The original fourteen raw bodies remain immutable. Their released-normalizer
canonical output must now differ from current output because the original tags
had no nonce attribute. A temporary in-memory expected copy adds only those two
attributes with a synthetic marker; the unchanged released `golden.Normalize`
nonce mask then permits comparison with the actual current nonce attributes.
All three normalizations must succeed with no hard findings. The report requires
`original_normalized_bytes_equal: false` and
`expected_delta_normalized_bytes_equal: true`, and binds all three canonical
hashes plus original/expected raw hashes. The expected copy is not a captured
response or a new baseline, and it is never installed or persisted as a golden.
No mask or normalization algorithm is added or copied into the application.

The additive read-only `tools/gui-public-golden/verify_nonce_capture.py` checks
both complete private capture manifests and original hashes, exact new flags and
hash relations, header/tag binding reports, and disk/FS canonical joins. It
refuses the historical equality-only schema or missing required fields. Its
DATA readback does not prove Go execution, container identity or qualification;
the owning managed runner must separately retain those actual controls.

## Verification and boundaries

Historical source checkpoint (2026-10-10, source7d8627e): expected focused app discovery was83actions: all63previous actions plus20new
nonce/delta actions (16top-level/67subcases). Both14-case native sets retain every
old functional assertion. Expected diagnostics are28three-way comparisons and
84normalizer calls. Compilation, GOROOT formatting, tests, DATA verification and
these new diagnostics have NOT RUN. Source/checkpoint commits do not relabel the
earlier63-action proof at8b or the reused normalizer build/helper proof at764.

Original75dc and latest8b disk/FS captures, original templates in historical
captures and earlier receipts remain unchanged. There is no fullM1, signed84+7
matrix, Django parity, Native Live, private/group, templ/UI, retirement, device
or deployment acceptance. Parent review and a fresh managed runtime reservation
precede execution; independent landing and deployment gates remain separate.

At7d, the managed formatter returned1 with4927B of alignment/line-break diff in
the new nonce test only; no tests/captures/DATA ran. Parent independently verified
that failure and exact whole-file afterimage before formatter successorf010e0d.

Atf010, actual managed formatting,83app actions (16top/67subcases, each once,
zero fail/skip/cache/race) and the strict capture DATA verifier passed. All63prior
action identities remain. Both14-case disk/FS captures have hard-free three-way
diagnostics: each untouched original differs canonically from current, and its
explicit two-attribute expected copy equals current. All84success fields from
the executed three-call path are true; all14disk/FS canonical hashes join.
The28anonymous capture responses have distinct nonempty header nonce hashes and
two bound external scripts. Separate actual disk/FS anonymous/fictional-adult
presentation tests each render twice and require fresh nonces. No account or
eligibility admission is established.

All94input guards (32source/41SDK/2owned-command/15original/twoarchives/binary
and buildinfo),56template loads and sixFSsnapshot/source28 joins remained exact.
The normalizer helper/SDK and actual764binary were reused without download,
helper-test or build repetition. All three owned containers exited0 and their
names/CIDs were absent after removal; no live-container inspect is claimed.
Parent independently cleared this actual boundary. The
[nonce checkpoint](../reviews/gui-public-original/nonce-checkpoint.json) binds
the compact proof; this documentation commit does not relabel its tested source.

After latestf010/original keepers were verified, only the superseded8b disk/FS
raw pair was deterministically archived. Every30file byte/fullmode and both
directory modes round-tripped before those two raw copies were removed. The
original75dc/latestf010pair, source packets and sole binary/SDK setup remain.
Earlier8b receipts keep their original identities. All wider gates above remain
unqualified; no baseline replacement or mask expansion follows from these checks.
