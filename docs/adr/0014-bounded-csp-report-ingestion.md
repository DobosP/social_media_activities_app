# ADR-0014: Bounded CSP report ingestion

Date: 2026-07-04
Status: accepted

## Decision
Keep CSP report collection unauthenticated, unthrottled, always-204, and report-only by default,
but bound accepted request bodies to 8 KiB before parsing and retain only sanitized
`directive`/`blocked`/`document` triples. The endpoint logs those sanitized triples under a global
per-minute budget and keeps a small process-local ring buffer for tests/debugging; durable operator
review continues through exported logs/files passed to `digest_csp_reports` or the
`digest_csp_reports` management command. Do not add a database model for browser CSP reports.

## Context / why
Browser CSP reports are unauthenticated and attacker-controlled, and browsers may send them with
session cookies even though the report body is not an authenticated user action. Persisting raw
report bodies, query strings, fragments, cookies, or headers would create unnecessary privacy and
retention risk for a report-only deployment. A database table would also make high-volume attacker
traffic durable product data. The useful operational signal is the bounded grouping key:
effective directive, blocked source, and document path.

## Consequences
The CSP collector can accept legacy `application/csp-report` payloads and modern Reporting API
batches without CSRF/auth coupling, while oversized payloads become byte-count-only log events. The
in-process buffer is intentionally non-durable and per-worker, so production review must use logs or
explicitly exported report files. `DJANGO_CSP_ENFORCE=True` remains the explicit enforcement switch
after deployed report-only violations have been reviewed and fixed.
