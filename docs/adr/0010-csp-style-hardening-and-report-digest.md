# ADR-0010: CSP style hardening and report digest

Date: 2026-07-04
Status: superseded-by ADR-0014

## Decision
Keep django-csp report-only by default and keep `DJANGO_CSP_ENFORCE=True` as the explicit
enforcement switch, but remove the temporary `style-src 'unsafe-inline'` allowance from the shared
policy. Key server-rendered CSP smoke pages must render without inline executable scripts, inline
event handlers, inline style attributes, or inline style blocks. Browser CSP reports remain accepted
at `/api/v1/ops/csp-report/`, and operators can group exported report payloads with
`digest_csp_reports`.

## Context / why
ADR-0009 removed executable inline JavaScript and event handlers from the high-risk SSR flows but
left inline styles as the remaining practical enforcement blocker. Flipping enforcement without
first collecting report-only data can still break less-travelled pages, Leaflet, chat, or offline
meetups behavior. Persisting every CSP report in the product database would add storage and
retention questions for attacker-controlled browser telemetry, so this slice keeps collection
log-based and adds an offline digest helper for operator review.

## Consequences
The report-only policy now emits style violations for remaining legacy inline styles instead of
allowing them silently. The tested key SSR flows are nonce/static-safe for scripts and style-safe
for server-rendered markup, including Leaflet place picking, E2EE messaging config, activity thread
chat config, and offline my-meetups wiring. Before setting `DJANGO_CSP_ENFORCE=True` in production,
operators still need to review deployed CSP reports for pages outside the smoke set and fix any
remaining violations.
