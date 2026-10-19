# ADR-0062: Native group creation form

Date: 2026-10-19
Status: implementation applied; focused race/custody/vet pass, complete PG/UI qualification pending

## Decision

Rebuild only the existing `/groups/new/` presentation with cached embedded Go
`html/template`, following ADR-0032 and the selected-view approach in ADR-0061.
Keep the logical `web/group_form.html` name and its existing GET/POST handlers.
The current form builder adds a GroupCreateForm-only typed snapshot from its
existing sorted field binding loop and already queried choices. The original
widgets, `as_p`, other form shapes, templates and catalogue remain intact. The
native projector consumes semantic values and options, never trusted widget HTML.

The view contains the same city, activity type, title and description inputs,
plus the existing staff-only cohort selection. Bound values preserve the source
last-value display while the unchanged cleaner consumes the first posted value.
Description keeps its original textarea without HTML `maxlength`: the service
counts Unicode runes, whereas the browser attribute counts UTF-16 code units.
All values and choices are escaped, action destinations are fixed, known form
errors have an accessible summary/field association, and credentials/owner fields
are absent. The new stylesheet only removes button color transitions within this
page; base CSS, press motion and reduced-motion overrides stay unchanged.

The shared Renderer retains cache, Vary, CSP and reporting headers. Family view
sources and their eight qualified fixture bodies must remain unchanged; this
packet does not claim that an unrun comparison passed. Template parsing is cached
without a shared private-data cache. Copy translation is memoized per request,
with no repeated markup-replacer construction.

## Boundaries and verification

No route, service, schema, flag, identity, age, group policy, owner/member/thread,
audit or API behavior changes. GET retains its narrower environment-flag check;
POST still uses the real session/Actor reload, CSRF/Origin, declared-field cleaner,
configured creation policy, rate reservation and audited participation transaction.
Reservations can persist separately when a later group transaction fails.
Minor flags are not enabled; no real account or child activation is authorized.

New source tests cover the typed seam, escaped forms/errors, supplementary-plane
description limits, real App cookie/CSRF/authority and audited rollback. Additive
exact source inverses recover the original form builder and prior renderer chain;
no historical pin or original assertion is retired. Formatting, focused projection,
copy and custody race tests plus touched web/app vet pass. Complete synthetic PG,
desktop/a11y and matched body-render measurements remain pending after application
to the qualified family feature.
This is not a Social milestone, main landing, deployment or performance acceptance.


The first complete web invocation on1dd7b0c failed three old source-custody
checks; all nine new group web roots/27named cases passed. The frozen preference
registry remains unchanged: exact in-memory inverses recover its three prior
inputs while live readers retain all138 physical current tuples. The old27-row,
ten-path mutation controls remain alongside new controls for all31 current rows
and new form drift. Every old assertion and pin is retained. Focused race checks
now pass, including the failed roots and added refusal controls; a complete fresh
six-lane successor and actual desktop qualification remain required. Committed
[UI methods](../../tools/gui-native-group/README.md) bind forms.json and an exact
tool-only Git delta; no failed-run projection is admitted for benchmarking.
