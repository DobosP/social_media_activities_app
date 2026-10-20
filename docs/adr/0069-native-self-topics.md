# ADR-0069: Native self Topics presentation

Date: 2026-10-20
Status: implemented on the working feature branch;focused checks passed with19 explicit pre-existing no-DSN skips;completePG7/desktop pending.

## Decision

Rebuild only the existing self `/topics/` HTML view with a typed request-local
projection, cached html/template and scoped CSS. Existing Go handlers and
services remain the authority. This is a selected functional page migration,
not a new interests engine or a requirement to convert every template one-to-one.
Keep original templates, catalogues, routes, dependencies and common assets.

The source-preparation checkpoint is actual published display closing
`1d0fd84ea4faa17e4d2c6f77e49b1137bff8d0c2`, tree
`b9e4b7a25a9de8ae3db32e3a929c0cbd19c6369f`. Native display producer S e0065ca9,
method T2 d2cd4a88, actual UI review b3526ecf and bounded feature463bf0e1
remain accepted under [ADR-0068](0068-native-display-access-preferences.md).
Publication9b7b67aa records that real closing; it is not a future Topics result.
At this checkpoint Topics application and product tests/runtime are unrun.
Later Root transport-control observations below still require independent review.

## Preserve the existing producer and save contract

`topicPage` already enforces an active current actor and owns the current
recommendation/category/selection/socialNav reads. The native view adds no
query, service call or authority. Its user shell is the actual Renderer-default
public_id/username/display_name/is_authenticated context, with optional existing
avatar data. It does not impose richer AccountView id/pk/cohort/guardian fields.

Categories keep their exact producer order, names, descriptions and slugs.
Checkbox selection uses chosen-map key membership, including a false-valued key.
Feed-link availability remains nonempty chosen-map length, independently of
visible or unknown choices. Empty categories preserve the original empty copy.
Do not add a sort, deduplication, filter, cap or submitted-value interpretation
to the projection. Messages and projected text keep contextual escaping; the
avatar alone uses the existing restricted image decoder, not a new URL policy.

The form remains POST to its current path, with one existing CSRF field and
repeated `topics` slug inputs. Existing SetTopics keeps its1000-input bound,
known-top-level filtering, unknown-ignore, empty-clear, active-self admission,
transaction/audit and302 `/topics/` result. No save, validation, auth/CSRF,
Origin, session, cookie or redirect rule changes follow from this presentation.
Existing SPA dispatch and data behavior precede HTML rendering unchanged.
Guardian ward-topic routes, permissions and policy remain separate and exact.

The single renderer dispatcher selects the new page. Cached template parsing,
nonce/CSP, response buffering, headers and existing shell flags remain source
contracts. CSS stays page-scoped; no new script, translation catalogue, clinical,
identity or content-policy decision is included. Existing display footer and
Romanian fallback polish limits are not silently changed by this slice.

## Source preservation and qualification

Core ca377a47 contains five source images and five authored pure-test roots;
none is an executed result. Early90d5 reports no blocking core defect, while
whole application admission remains pending. Test-preservation bridges are a
separate pending input. Compose them against exact closing C before application:
retain every original assertion, case, pin and source relation through complete
inverses, strict current physical-source readers and real mutation controls.
No production legacy-reference bypass, skipped root or weakened guard is allowed.

Preserve53 existing body identities:45 prior literal bodies plus the eight
actual display/access fixtures in accepted manifest5ad6a57e. Use the actual
normalization/body proof, not a new normalization or exception. Existing canonical
account/date keepers and original failure histories remain unchanged references.

[ADR-0070](0070-bounded-xz-git-source-transport.md) supplies the bounded full-source
transport prerequisite. Independent whole-source review must cover core,
bridges, transport and these documents together. Apply one reviewed product
source commit, run focused iteration checks and one complete affectedPG7 on its
exact clean SHA after full-archive preflight. Actual source/raw/privacy/lifecycle
acceptance precedes separately reviewed finite desktop methods and actual
JS/no-JS, strict whole-document Axe, real keyboard/FormData and all53 body checks.
The [execution plan](../execplans/gui-native-topics-and-xz.md) records that order.

No benchmark or general efficiency claim follows from cached Go rendering.
No mobile/device testing, main push, GUI milestone, production, government,
legal-parent, provider, school or real-child activation is authorized here.

## Root transport-control observation only

Root receipt1cbcdb3c reports four source-selected XZ controls passed with zero
failures/skips, and one exact memlimit-removal mutant failed only the expected
fourth root. Driver8897d477/controlsd550616c stayed unchanged; the owned temporary
HOME and mutant copy were removed after evidence. These are tiny fixed-fixture,
fake-Git-stdout observations, not Topics/Go/browser/PG execution or exact-source
qualification. Independent raw/custody review remains pending. The author of
these documents ran no candidate/control; all five Topics pure roots remain
source-only, and future application/producer/fullPG/UI results remain unknown.

## Applied-source focused checkpoint

Earlier source-only checkpoints above remain historical. Applied exact whole-reviewed68fe231f Topics/core/test-preservation/XZ source. Focused race runf5f488cb passed32roots/117named,including all8newTopics roots;19pre-existing database-dependent roots were skipped without a DSN,including the registered topics/guardian and SPA roots,so those remain mandatory zero-skip checks in completePG7. Affected vet web+app,mechanical format10Go/AST2Python,docs69 and whitespace pass;no race warning. Source-selected4XZcontrols and exact fourth-root-only memlimit-removal sensitivity1cbc are independently accepted in68fe;these tiny fake-Git fixtures do not qualify a new producer. Exact whole-SHA XZ/Git blob-mode-PAX preflight,complete affectedPG7,53fresh literal prior bodies and finite desktop qualification remain pending. No benchmark,new policy/main/milestone/production claim.
