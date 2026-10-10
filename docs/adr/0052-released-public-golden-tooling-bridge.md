# ADR-0052: Released public golden tooling bridge

Date: 2026-10-10
Status: focused managed checks passed at764beb1; independent actual review clear within original-native repeatability scope

## Decision

Use the already released core-v1.6 public `golden.Normalize` package for an
optional, test-only diagnostic bridge around the fourteen original-native public
fixtures. The consumer command supplies `golden.Options{}`: no asset aliases or
historical empty-URL oracle are needed or admitted by this bounded public scope.
The SDK owns normalization; no normalization algorithm is copied into Social.

The command builds only in a private copy of the exact released Go archive,
with the original module/sum and all41released regular files unchanged. Its two
Social-owned command/test sources are added in that scratch module. This is
tooling preparation, not installation into an application or a consumer-kit
fork. App Go modules, dependencies, installed SDKs and production are untouched.
`tools/gui-public-golden/release-bindings.json` records the archive hash, source
commit,41file hashes/full modes and the exact public normalizer source hash.

The existing producer admits the optional binary only alongside its actual
managed-build SHA256, that exact Go archive and the committed original corpus.
It bounds child output/time, verifies archive/binary hashes before and after,
and records canonical hashes, independent hard findings and diagnostic outcome.
Both original and current raw bodies are passed byte-for-byte on stdin. No raw
body, nonce or canonical HTML is printed to a public log or installed as a new
golden baseline. The existing fourteen scenario assertions remain in force.

## Boundaries

An equal result describes repeatability of two original-native synthetic renders
only. It is not templ parity, a Django oracle, signed G0/G1 coverage, Native Live,
formal template-conditional coverage or retirement acceptance. Equal canonical
bytes cannot override hard findings. Empty URLs and template leaks refuse; no
local masking exceptions or policy changes are introduced. All raw original
captures, unsigned ownership/private applicability holds and1651unresolved
declarations remain unchanged. No template, route, UI or safety code is edited.

## Verification

Authored public-SDK mutation controls cover nonce/JSON-CSRF and attribute-order
equivalence, nonce-like visible text, ordinary attributes, non-CSRF JSON,
character changes, warning-arm removal, URL changes, sibling order, preformatted
spacing and inline gap presence. Hard-finding controls include empty URLs,
failed sanitization, template leaks and invalid JSON, including identical
canonical output that still refuses. Raw input bounds are exercised separately.

Historical source checkpoint (2026-10-10, source3b27357): source and release-file
metadata had been reviewed locally; managed compilation,
public-SDK tests, fourteen-case integration and formatting have NOT RUN on this
successor. Existing tests and formatter receipts stay bound to their prior
sources. Parent owns fresh runtime reservation, evidence review and publication.

At764beb1, actual managed formatting and build passed;21helper and23app test
actions each ran/passed once with zero failures/skips/cache/race findings. All14
original/current diagnostics were equal and hard-free.28source files,41released
SDK files,2owned commands,15original corpus files and both archives remained
byte/fullmode-exact. The actual binary is SHA598515e7…/4,897,832B; its Go build
information correctly reports a private released-module copy as `(devel)`, not
a compiled tag version. Parent independently reviewed the bounded actual result.
The earlier4035Bformat failure and exact locked x/net acquisition remain compact
history. This adds no formal oracle/group/Live/templ/retirement acceptance.
