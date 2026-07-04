# ADR-0009: CSP enforcement prep

Date: 2026-07-04
Status: superseded-by ADR-0010

## Decision
Keep django-csp report-only by default, but use one shared CSP policy that can be flipped to
enforcing with `DJANGO_CSP_ENFORCE=True` after deployed violation reports are reviewed. Extract
executable server-rendered inline JavaScript and inline event handlers from key web templates into
static files, and nonce the remaining JSON script islands.

## Context / why
The child-facing server-rendered UI needs CSP enforcement, but the previous report-only policy still
had template-level inline JavaScript, inline event handlers, JSON config blocks, Leaflet map scripts,
chat config, and offline-meetups service-worker wiring that would block a direct enforcement flip.
Allowing all inline scripts would erase the main XSS value of CSP, while forcing a hard enforcement
switch before collecting production reports risks breaking Leaflet, chat, and accessibility-related
live-status behavior.

## Consequences
The enforcement path is now operationally explicit: deploy in report-only, review
`/api/v1/ops/csp-report/`, then set `DJANGO_CSP_ENFORCE=True`. Leaflet, chat, request-only
geolocation, and offline-meetups behavior stay progressive enhancements over server-rendered
fallbacks. Inline style attributes remain a separate tightening step before the policy can remove
the current style inline allowance.
