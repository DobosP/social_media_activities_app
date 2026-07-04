# ADR-0015: Browser security headers

Date: 2026-07-04
Status: accepted

## Decision
Pin browser security headers in shared Django settings: `SECURE_CONTENT_TYPE_NOSNIFF=True`,
`SECURE_REFERRER_POLICY="same-origin"`, and
`SECURE_CROSS_ORIGIN_OPENER_POLICY="same-origin"`. Keep the custom `Permissions-Policy` middleware
because Django has no built-in setting for that header, and set its shared policy to
`geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()`.

## Context / why
The app is privacy-first and child-safety-sensitive, so browser policy must not rely on implicit
framework defaults or production-only overrides. Same-origin referrers avoid leaking paths to
third-party origins, COOP isolates browsing contexts, and `nosniff` prevents content-type
confusion. The web UI can request geolocation only from same-origin pages for request-only proximity
flows; it does not need camera or microphone access. Payment, USB, and interest-cohort are also not
product requirements and should stay disabled.

## Consequences
Normal responses from every environment carry the same header posture, and tests assert exact
values on `/healthz`. Future features that need a disabled browser capability must change this ADR
with a new superseding decision and targeted privacy/safety review.
