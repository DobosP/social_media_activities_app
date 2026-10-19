# ADR-0063: Native group detail and existing owner controls

Date: 2026-10-19
Status: independently reviewed source; complete affected qualification pending
Extends: ADR-0032/0061/0062; no previous decision is superseded.

## Decision

Rebuild only the existing `web/group_detail.html` view with cached embedded Go
HTML and typed group/post/feed projections. Keep its complete overview,
membership actions, owner archive/adult listing, eligible roster, announcements,
conversation and upcoming activities. Existing templates/catalogues/assets,
services, queries, routes, flags and authority decisions remain unchanged.
The new stylesheet scopes opaque theme-token header and button color stability
to this view. Global base CSS, press motion and reduced-motion overrides remain.

The projector consumes already-authorized semantic rows. Nil versus admitted
empty roster stays distinct; web/API roster projections are not conflated.
Post markup is regenerated only with the exact existing group BodyMarkup
arguments and compared to the current producer output. No widget/safe-string
HTML is trusted. Snippets, sentiment, shares and attachment states retain their
existing authorized projection and controls, including the current Report
member/other-author conditions. Original helpers and live scripts stay intact.

The existing staff-owner read-failure fallback supplies own static broadcasts
without live configuration. Its current template loads a client that dereferences
null; the native view instead requires valid current configuration for LiveThread.
It invents no subscription and preserves static own broadcasts/forms. This also
covers the same-cohort staff-owner fallback after read authority is withdrawn.
A malformed present configuration fails before output; presend remains separate.

## Boundaries and qualification

No new management route, editing/eviction/deletion, guardian role, backend policy,
media activation, query, schema, provider or real-child capability is introduced.
CSRF/Origin/current Actor, privacy withdrawal, audit/rollback, rate/idempotence,
cohort/consent/block and locked mutation checks stay with existing services.
The supplied CanAsk flag remains independent of thread-readable membership; a
retained staff membership can show the current fixed-question form while the
unchanged cross-cohort service submission still refuses. No authority is added.

Newest exact source inverses recover the entire previous31-row authority chain,
original27 mutation controls and all138 physical preference tuples. Original
registries, pins, templates and assertions remain; current readers receive only
physical current tuples. New pure/assembled HTTP/static/source-refusal test source
is additive. Formatting, native execution, complete affected PG, committed
fixture/browser methods, body equality and honest matched measurements remain
pending in the [plan](../execplans/gui-native-group-detail.md).
No completed runtime, speedup, main, milestone, release or activation is claimed.

## Applied source checkpoint

Independent whole-source review4bd70762 accepts the applied22-path product plus
eight committed-method candidates before runtime. Exact old source inverses and
all prior assertions remain; only gofmt and one unused local in a new test differ
from the reviewed product packets. The eight methods preserve the actual capture
projection, typed-nil footer, prior18 body hashes and native dialog-cancellation
witness. No capture or browser success is inferred from this source approval.

Actual focused web race14roots/61named/zero skips, touched app/web vet, full
native formatting, authentication snapshot hashes, Python/Node syntax and eight
unchanged qualifier controls pass. Complete affected six-package PostgreSQL,
actual captured projection, desktop7+7/strict Axe and matched warmed body
measurements are still required on committed source. The earlier source-only
preparation statements describe that earlier checkpoint; they are not runtime
claims for this applied checkpoint.

## First native execution and narrow correction

The first complete-six attempt on dc1c15ca ran the complete web lane only:
185 roots,183 pass/two fail, zero skips; two existing group-page leaf cases
returned invalid-snapshot/500. No later App/capture/five package lane or UI ran.
Recorded container exit was1 with no OOM/timeout; all three owned containers and
the synthetic PG process/socket were cleaned. Original rawbc9758 remains failed.

Unchanged socialThreadContext stores its optional reply target as a typed nil
map when no query target exists. The new renderer mistook that interface for a
present row. Independent source8a3775a8 approves only a decoded-row nil guard;
all original author/id/bound-POST logic, producers, policies and tests remain.
A new pure regression file covers typed nil/untyped absence, real GET target,
first bound POST value and nonnil malformed target refusal before any output.
Focused repaired race17roots/74named/zero skips and web vet pass. A clean-source
successor complete affected-six run is required; no repair pass is inferred yet.

## Complete PG and inline-link presentation correction

Qualified producer7fb35d0 passes complete affected PG6:428 roots/1,246 named,
zero skips/failures,291 commands, thirteen owned-container removals, six guarded
DB retirements and graceful PG cleanup. Independent6d6c91ec binds rawf56fb1e6.
UI-only executable-reader method0995583 preserves all native/PG inputs; its
actual export admits the successful App projection and matches eighteen earlier
native bodies. Seven JS/seven no-JS shapes and confirmations complete, but the
21 strict whole-document audits report nine repeated link-in-text incomplete
assessments and zero violations. This UI remains failed; no benchmark ran.

A controlled diagnostic records eighteen unchanged link rectangles/colors and
natural thread/header occlusion. Timestamp permalinks share adjacent muted text
color with no permanent non-color cue. A scoped underline for post metadata and
quote links clears all21 audits. The experiment remains failed at fourteen
unchanged exact-asset guards because its stylesheet was deliberately substituted;
no experiment is accepted as qualification or as removal of natural occlusion.

Independentda2f59c3 approves only the new page stylesheet cue, its exact existing
new-App served-sheet expectation and truthful benchmark requested/not-reached
metadata. All Go runtime, policy/query/HTML/original assets and test predicates
remain. Actual CSS GET/HEAD race and focused web17roots/74named/zero skips/vet
pass. Fresh complete-six/current-asset UI/captured-body benchmark must qualify
the committed stylesheet rather than add a CSS/test source-bridge exception.
