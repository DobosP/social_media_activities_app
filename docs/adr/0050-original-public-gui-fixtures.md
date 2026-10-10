# ADR-0050: Original-native public GUI fixtures

Date: 2026-10-10
Status: source implemented; managed targeted execution and independent landing review pending

## Decision

Implement an additive, test-only original-renderer fixture producer before any
templ or UI adoption. It invokes the current registered native HTML mux and
unchanged Pongo renderer for anonymous privacy, terms, open-data and landing
pages. Fourteen synthetic scenarios cover English/Romanian, the existing
contrast/text/motion presentation settings, and both public snapshot-availability
arms. No database, provider, session identity or real person supplies a fixture.

An explicit test flag writes unmodified HTML and a source/hash manifest into a
fresh private directory. Existing directories/files and symlinked ancestors
refuse; outputs are mode0600. Per-render nonces remain in private raw HTML, while
the manifest retains only a CSP-header hash. No app-local normalization or
template-conditional percentage is introduced. The producer's source bindings
are checked before and after rendering.

## Boundaries

These are original-native registered-handler fixtures, not historical Django
captures, a Native Live gate, signed group acceptance or proof of a complete G0/G1
port. The snapshot file is explicitly an availability-presentation fixture, not
an exported dataset/schema qualification. The supplied CSRF cookie is fictional
and carries no session or authorization.

The unsigned84-port/seven-frozen partition, historical/unreachable paths,
G2p/G5/G6 and privacy/minor/guardian/moderation applicability holds remain intact.
No original template, renderer, safety rule, dependency or frozen assertion is
changed. The1651 unresolved declarations remain red; no Python/oracle retirement,
kit adoption, deployment or milestone acceptance follows.

## Verification

The real capture cases and exclusive-output refusal run as targeted native Go
tests in an owner-approved managed Go1.27.1 runtime. Actual commands/counts are
recorded in STATUS/WORKLOG after execution; authored cases are not passing proof.
Landing remains subject to ADR-0040 and independent review.
