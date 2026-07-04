# ADR-0010: Readiness and Structured Request Logs

Date: 2026-07-04
Status: accepted

## Decision
Keep `/healthz` as a cheap liveness endpoint that does not touch dependencies, and use `/readyz` as
the dependency readiness endpoint for DB plus configured shared cache and object storage. Emit
request correlation through `X-Request-ID`, log records, and Sentry scope; when request logging is
enabled, emit only allowlisted operational request fields and default production logs to JSON.

## Context / why
Load balancers need a process-up probe that does not flap during a DB/cache/storage incident, while
orchestrators also need a readiness signal that drains a node when configured dependencies are not
usable. Operators also need request correlation across HTTP, periodic jobs, logs, and Sentry, but
this project must not log PII-bearing bodies, query strings, headers, cookies, users, or IP
addresses.

## Consequences
Uptime checks can keep using `/healthz`; deploy readiness checks should use `/readyz`.
`REQUEST_LOGGING_ENABLED`, `LOG_FORMAT`, and `LOG_LEVEL` remain operational knobs, with dev/test
kept quiet/readable by default. Future logging additions must extend the formatter allowlist
deliberately and preserve the no-body/no-query/no-user/no-IP posture.
