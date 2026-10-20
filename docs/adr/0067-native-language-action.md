# ADR-0067: Native interface language preference action

Date: 2026-10-20
Status: exact L2 native/account presentation accepted; companion and bounded feature independently accepted40339c77/5d6004ce; source publishedM016c(d185); separate L1 retentionb617/postf8a60b8837f9e252fd33f0c9baf346ab90d5d3eccf11ef5e8b74e4cd8612ba6e.

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

## First exact-L qualification and bounded custody repair

Exact L `c438b3c51d10ebdc04dd937cf0c6bee67aee5a0b` passed focused race
checks (17 roots/53 named, zero failures/skips/race warnings), affected App/web
vet, formatting, four archive controls and documentation checks (local result
51766411). Its complete Git gzip stream reached EOF: 67,077,682 bytes, SHA
38ad639fed2d052ad8375cd4a5834e4eb05f35115d1dd41a26556d9e625b4075,
under the unchanged 67,108,864-byte bound (preflight685fa344).

The first full7 campaign is FAILED, result e802817e5f2ef23cb5794af0b2c3f8a55e0f2fc203aaf28ec7ce1feaee87d097,
Root terminal8756c4cc. Its web package ran 215 roots/879 named cases: 212/876
passed and three source-custody roots failed, with zero skips/race warnings.
Later six packages were not run. The native lane exited1 after112.22 seconds,
without OOM or timeout. The owned private PostgreSQL server and all three
created containers were stopped and removed. Preserve that failed outcome.

The three failures share the historical proof's separate preserved `app.go`
input, which does not occur in its 32-row repair inventory. The existing exact
language inverse had not been applied to that separate historical proof. Add
that inverse before comparing the preserved historical app pin; require the
complete newest physical app bytes and retain the physical input tuple, every
original row, assertion and mutation control. Recovered old bytes remain an
in-memory historical proof and never become live source admission.

Independently review the exact repair, run focused source/custody checks, commit
the clean repaired source and repeat complete-stream preflight before one full7
rerun. Actual repaired-SHA/full7 acceptance, account UI rebinding and the separate
language companion remain pending. No runtime, account-presentation or broader
performance acceptance follows from this repair checkpoint.

## Current repaired language qualification and closing checkpoint

Exact producer L2 `0dbcc6ba753e94b3dee2203f32546692f97f7894`/tree `6fc5c7ec24db0cd6f91eee21227e49dcf77ca6e6` is independently accepted by7449f313/result25485a22: seven complete affected packages,469roots/1,395named,zero failures/skips/race warnings,340commands695artifacts,all15ownedroles/sevenDB/privatePG cleanup. All1,368priorR case identities remain. Source-only and L1 failed history above stay historical. L2 actual gzip67,080,798B is under unchanged64MiB; no future source-size prediction.

Account method `016c5a69e3199da643ad755301e5c78fb8d2e0c7`/resultd868902b is independently acceptede9343e50 for unchanged account GET-only display:8JS8no-JS/24strictwhole-documentAxe531predicates/428keyboard62forms/37literalpriorbodies/85artifacts/cleanup. All32primary+audit PNGs equal originalM3. Sourcecode/native4/accountcode3/fivehelpers/assets/catalogues/dependencies remain unchanged; benchmark was not requested. Original48mixed BODY samples remain historical,no new efficiency claim.

Separate companion independently accepted40339c77/result426117d2:5real registeredPOST/localGET flows,5refusals,3strict synthetic-pageAxe/48passing observations,45literalbody identities,owned cleanup; syntheticGET/registered-handler scope only;actual result 426117d2dccb0db187841c223e6423ba329d3bbd91e414f24b214e5adca0394e,independent review 40339c773400ed0649c37bd2e30187f291e9672c6efb7be6671b8dbc3c89bd38. The actual synthetic real-handler interactions/body proof cannot substitute for assembled App authentication/limits or account presentation. Bounded feature acceptance 5d6004ce97a09b5e6ada39a609eba93e9ed523d505348bf896acf4bef3aba34a is copied byte-exact as canonical acceptance.json; no authored PASS is introduced.

[Language current record](../reviews/gui-native-language-action/README.md) is metadata/provenance only,with exact Git/source/manifest/result/review references. Existing e31/6a0 andbc10/2787 remain reference-only;no nested archive/workspace or standalone custody claim is added. Full latestL2PG695/currentUI85/accepted11-filecompanion raw remain physical at admitted paths;historicalR695/baselines/branches/unmergedpackets/keepers stay protected. Separate exact failed-L1 retentionb61733e9/postf8a60b8837f9e252fd33f0c9baf346ab90d5d3eccf11ef5e8b74e4cd8612ba6e removed only source/,binaries/ and command-006.stdout:2,290files248directories407,696,307logicalB. Deleted generated bytes are unavailable; original failure/result/source manifest/141non-target rows remain,with only2knownparent entry transitions. This metadata closing performs no further cleanup;past canonical pruning also states deleted paths unavailable. Actual feature publication published source epoch M016c exactly equals existing remote feat/gui-app-migration-a,receipt d1855cac;nativeL2 and bothL1/L2 code histories preserved remotely,main unchanged. The later closing metadata commit/publication is not asserted here. No main/milestone/production/provider/government/legal-parent/real-child activation.
