# ADR-0054: Existing public external-script nonce binding

Date: 2026-10-10
Status: source authored; managed formatting/tests/delta diagnostics NOT RUN

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

Expected focused app discovery is83actions: all63previous actions plus20new
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
