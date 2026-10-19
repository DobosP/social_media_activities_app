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
