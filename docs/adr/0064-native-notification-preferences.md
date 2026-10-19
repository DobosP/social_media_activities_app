# ADR-0064 — Native notification preferences

Date: 2026-10-19
Status: independently feature-qualified e900960; feature branch only

## Decision

Rebuild the existing `/notifications/preferences/` classic view as a cached,
escaped Go template under ADR-0032. Existing AccountView, AccountAction and
accounts.Settings remain its only data and mutation paths. The original logical
template, routes, field parsing, SPA dispatcher, services and frozen inputs remain.

The typed page displays the existing 22 optional categories in source order.
Checked means muted. Ordinary repeated `muted` fields preserve the existing
absent-means-clear behavior. `system` and `moderation` remain mandatory through
unchanged platform.Notify; the existing arrival/child-supervision warning remains.
A category does not imply a role, guardian capability or permission to participate.
No age, cohort, verification or MFA requirement is added to current account access.

The page preserves existing language/theme/text/motion choices, navigation and
shared hovercard/site script order, generated nonce and cache-purge wiring. A new
stylesheet scopes only the already-qualified native-page opaque header and
transform-only button transition patterns. Original CSS, scripts, templates and
locale bytes remain exact. No provider, guardian activation or new policy ships.

A strict complete-source inverse for the new dispatch precedes the unchanged
32-row historical source relation. All existing 32/31/27 assertions, pins and
138 physical preference bindings remain; recovered historical bytes are never
reported as physical current tuples. New controls reject stale, mixed, rehashed,
unrelated, missing and duplicate current changes.

Retained notification layout and exact Pongo form-byte controls explicitly use
a test-only frozen-template alias and source-bound common-page adapter. Their
original assertions, fixtures, source pins, OS/FS traces and normalizer remain.
They verify the preserved reference view, not the rebuilt native markup. Current
native form, authority and transport checks live in the additive App/pure/browser
suites. No production selector, fallback, flag or short-row exception is added.

## Qualification and limits

Additive pure and real App tests cover self scope, form semantics, unchanged
CSRF/Origin/current-account admission, transactional audit rollback and mandatory
delivery. Actual registered capture excludes all credentials and unrelated account
fields. All new roots run in the existing complete affected six-package method.

Before desktop acceptance, committed export/browser methods must check all eight
family, ten creation and seven accepted normalized detail bodies byte-for-byte.
The existing render-nonce-only normalization remains explicit. Actual strict Axe,
assets, scripts/CSP, ordinary keyboard/no-JS and cleanup results remain pending.
If measured, body benchmarks must use one captured authorized projection for both
engines and count native projection cost; no full HTTP or general speedup claim.

This source proposal is not full21, image, main, milestone, production, mobile,
provider or real-child qualification. Root owns application, runtime and feature
publication after independent review; Teacher/Social milestone gates remain.

## Applied reviewed source checkpoint

Independent whole-source9d97b22c and fixture supplement3c04a0d5 approve15
product paths. The new unassigned fixture additionally covers contrast/larger/
full without changing six case IDs or actors/forms. Root applied those paths
and five separately reviewed methods574ce748. Only five new Go files required
gofmt; all six original source afterimages remain byte-exact. No service,
query,role,route,original template/asset/catalogue/schema/dependency or prior
assertion changed. Pure/static/current-source controls, real canonical affected
six-package PG, actual capture/export/strict desktop and matched measurements
remain required; source acceptance implies no executed runtime success.

## First cheap iteration and narrow repair

Actual initial focused web race12roots/68named had65 passes and three failing
nodes across two roots. One retained negative substitution became a no-op when
the new notification branch interrupted its old Group-detail literal; unchanged
valid source was accepted,not a modified invalid source. The new reference
probe also sent its native-only invalid-key privacy sentinel to Pongo. App
static GET/HEAD race passed independently. No PG/capture/browser/benchmark ran.

Reviewed repair7969c62b only moves the disjoint notification dispatch before
Group detail,updates its new exact inverse and removes the new test sentinel
only before the historical Pongo call. Original assertions/pins and all32/31/27
relations remain; the old literal now occurs once so deletion is a real probe.
The current header/authority/form/22-row view is unchanged. Actual successor
checks and complete affected qualification remain required.

The repaired cheap iteration passes all12 focused web roots/68 named cases with
zero skips/failures,including the original source mutation controls. App public
stylesheet GET/HEAD race1 passed; touched app/web vet,original authentication
snapshot hashes,full native formatting,Node/Python syntax and63-document checks
are clear. All four absent-path Go export overlays compile (no tests executed).
Commit this complete source/method batch,then run the unchanged complete affected
six-package qualifier before actual capture/export/desktop/measurement acceptance.

## Complete PG and first UI presentation repair

Exact7819 passes all six affected PostgreSQL packages:436 roots/1,279 named
passes,zero skips/failures;293 waited commands/599 artifacts,thirteen owned
container removals,six database retirements and stopped PG. Independent69e71436
binds resultcf4cbeb7. The first UI admits the real projection and all25 prior
bodies,completes6JS+6no-JS/cleanup,but remains failed: six save-focus method
failures and six repeated color-contrast incomplete checks,zero violations.
The requested benchmark was not reached; its original result remains unchanged.

Form serialization restores selections via pointer actions; a later programmatic
focus does not prove keyboard modality. The method now uses real Tab from the
last checkbox and retains its original assertion,adding actual focus-visible/
nonzero outline metrics. The RO account-overview label wraps in its narrow
menu; no actual text occlusion was found. Scoped intrinsic desktop panel width
clears the ambiguity,while bounded max-width preserves the viewport. Scoped
nowrap/input-flex/text-min-width rules keep each checkbox beside its label
without hiding or truncating text. Existing themes,hover,motion and base assets
remain unchanged. Only two new-App stylesheet literal expectations change.

Independent a046033d approves the three-file batch. Controlled jointdiagnostic
74b061e2 passes18 strict audits/408 predicates and all12 real-Tab focus states,
with all264 rows grouped; it remains failed at12 deliberate stylesheet asset
guards and is never qualification. Fresh complete-six and current-asset UI/
matched measurements on committed source remain required; no bridge exception.

Applied repair passes actual new-App stylesheet GET/HEAD race1 with the exact
521-byte served CSS; Node syntax and whitespace checks are clear. No native
renderer/service/query/authority code changed after7819. Commit the reviewed
presentation/method batch and qualify complete6/current assets on that new SHA.

Finale900960 passes unchanged complete6:436roots/1,279named/zero skips,
292waited commands/597artifacts/13CIDremovals/6DBretirements/stoppedPG. Independent
8dfaa7f2 andbc1f0079 accept actual6JS+6no-JS/18strictAxe408predicates/12visible
keyboard Save states/25oldbodies/66assets/78GET/cleanup. Actual22rowcapture and
12matchedbenchrows are admitted descriptively byb8abda54: elapsed body timing
+44.30%EN/+42.16%RO,allocations+6.82%;allocatedbytes/HTML/gzip lower. No CPU
profile or overall speedup claim. Keeper29a9ff retains1,484members/133historical
aliases; independent4ad6ab72/f3329041 accepts custody/feature. See
[canonical evidence](../reviews/gui-native-notification-preferences/README.md).
No all21/current-image/main/milestone/activation claim; retention is separate.
