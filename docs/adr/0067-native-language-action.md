# ADR-0067: Native interface language preference action

Date: 2026-10-20
Status: implementation slice; exact-source qualification pending.

## Decision

Implement the existing language forms' missing `POST /i18n/setlang/` action in
Go. Accept only English (`en`) and Romanian (`ro`) as a display preference.
Retain the original templates, language negotiation, catalogues, account
producer, identity services, SQL, schema and dependencies.

The exact canonical path receives a 4,096-byte request limit before the App's
existing session form parser and again at the standalone handler. Accept only
URL-encoded POST fields `language`, `next` and `csrfmiddlewaretoken`, with no
duplicate or unknown fields. Query parameters cannot supply the preference or
posted CSRF proof. Unsupported methods and aliases retain their source-defined
refusals; no refusal writes a preference cookie.

Every caller, including anonymous and API-token callers, must pass the existing
same-origin/double-submit CSRF check. Invalid supplied credentials still fail
at App admission. A missing CSRF authority fails closed. Saving a public display
preference cannot restore a revoked session or grant account access.

Success sets the existing host-only `django_language` convention: path `/`,
one-year MaxAge, SameSite Lax, Secure for TLS or configured HTTPS. It creates no
session, identity, consent or audit authority. Responses handled by the action
are no-store.

Return only to a local, source-registered HTML GET route. Reject external,
protocol-relative, malformed, control-character and backslash targets, plus
download/feed/worker/JSON transports and unknown or POST-only routes. Fall back
to `/`. The existing revoke-error form's action-path `next` maps explicitly to
`/settings/`; its rendering context and hidden field remain unchanged.

## Preservation and qualification

The account migration's temporary 404 assertion is superseded only by a
positive cookie/redirect assertion for that same request. Its original fragment
and previously qualified 404 outcome remain historical evidence. All original
306 test paths and assertions are preserved through exact source-reader
inverses and real mutation-target bridges. No original guard is removed.

Source proposal V2 addressed two pre-execution review findings: method-qualified
route registration avoids mux conflicts, and first-match return classification
excludes non-HTML dispatches. Four new Go images were mechanically formatted;
all six existing afterimages remained exact. Independent preliminary review
`287d08b0221209871bc35d91741b277983c85602acc99fc3603f0071698322c2`
accepts that source delta only. At that preliminary checkpoint, the final account
closing, operational bindings, application and actual tests were still pending.
Applied-source and actual qualification results must be recorded separately.

After final source review: run focused checks, commit the exact product, stream
the complete Git gzip archive through EOF under the unchanged 64 MiB limit,
then run the established complete seven-package affected qualification once.
Keep actual failed attempts distinct and require owned database/process cleanup.

The existing account desktop method remains unchanged. Preserve its 37 prior
body guards and add eight literal account-body identities from the accepted
account keeper. A separate reviewed companion checks actual language POSTs,
cookie changes and localized local returns using fixed synthetic GET pages and
the real registered handler. Those synthetic interactions do not replace
assembled App authority tests or qualify account presentation. No new body
performance claim follows; the earlier account measurements remain mixed.

No app-main landing, GUI milestone, production, provider, government,
legal-parent or real-child activation follows from this action.

## C-bound complete-source transport checkpoint

The account closingC7586d7c40fea262f22942c5242e7ec05716216c2 is published;
canonical accountkeepere31b/6a0c,keeper2799,feature2c38,publication2112,
retention502be/postchecke1cc and actualpublishd70b are bound in this sourcepacket.
The10formatted Go and companionV2c69/c65/348 source-only reviews287d/6502
are prerequisites,not application or actual language qualification.

Actual C capacityf492 reports default whole-SHA gzip67,152,204B,over the
unchanged67,108,864B cap by43,340B. Explicit -9 yields67,042,499B,under by66,365B.
Both streams completed EOF/CRC0. Independent6268 and actual6c64 prove both
decompressed tar streams are105,932,800B with identical SHA
ab24ced0ffb9aa6cf96ba8ee913d892a4c6548d41394d73de2a25b5feed66272;
source bytes,tar headers,modes and provenance do not change. No stream is retained.

Named LANGUAGE_COMPLETE_SHA_GZIP9_TRANSPORT_ONLY changes only the qualifier's
`git archive --format=tar.gz` call by adding `-9`; original696f is recovered by
closed inverse from3171. The existing pure archivecontrol expected-argv tuple
gains only that same option; all four named cases,assertEqual predicate,remaining
body bytes,caps/refusals,wholeSHA,seven profiles,timeouts and cleanup are preserved.
No oldcase/assertion is removed,no allowlist expands and no cap is raised.

This C diagnostic is not L capacity evidence. After whole-packet source review
and application/localchecks,commit exactL and repeat its complete -9 gzip stream
through EOF under the same cap BEFORE full7. ActualL,its size/full7/UI/operational
admission/results remain pending; no future size or PASS is predicted. Existing
account operational metadata already admits the actual in-L driver hash through
complete7_driver_sha256; its source/allowlist stays unchanged.
