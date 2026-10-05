# ADR-0034 — Native configuration and private error reporting

Date: 2026-10-04
Status: implemented; landing per ADR-0040; human review gates first deployment
Extends: ADR-0032 configuration boundaries

## Context

The complete native conversion accepted source defaults but refused more than 80
nondefault policy settings. Operational error reporting had no native implementation.
The remaining work must give overrides real effects while preserving child safety,
privacy, erasure, authentication and browser/API defaults. Source landing does not
activate providers, minors, ingestion, recurring work or production reporting.

## Decision

The executable decodes an explicit environment/presence interface, validates typed
configuration before connecting services and reports setting names without values.
Catalog, social, messaging, media, jobs and deferred-work policy have typed hooks.
The CLI inventory lists every accepted bound and intentionally refused control.
Unknown Python adapters remain refused until a reviewed native implementation exists.

Mandatory scanner/child-venue gates, private progression, adult-only file/video
cohorts and immutable RO-EDU facts contracts remain fixed. Reliability-report
thresholds can tighten (1..3), report lifetime cannot shorten below 14 days, and fact/
correction/edge consensus cannot fall below 3. Media limits retain hard ceilings;
profile duplicate-scan coverage cannot fall below 10000; frame capacity covers the
whole configured duration. Presence retention stays at most 6 hours. Mutable cohort
maps are copied before the shared services start serving.

A catalog policy value travels through a private context key across HTTP adapters,
HTML/forms, social share/venue gates, discovery, media and one-shot jobs/exports.
Service construction seeds the same typed policy. There is no mutable process-global
policy: two runtimes remain isolated, and invalid supplied visibility policy fails
closed. Context cancellation remains intact.

Ordinary non-file request data uses bounded request and memory caps. Multipart
readers stream files to private scratch and enforce an aggregate budget for non-file
fields as well as their smaller field caps. Native semantics are documented directly;
this is a hard data cap, not a claim of Django's buffering implementation parity.

PostgreSQL shared-rate hooks from ADR-0037 consume the decoded per-action caps/windows.
Required-shared mode additionally asserts coherent PostgreSQL pool/store wiring across
configurable admission services, while REDIS_URL and Python Redis
Channels classes are refused. The retired Python DB_POOL_TIMEOUT is rejected even
at its old source value: pgx acquires under caller deadlines, so accepting a separate
queue-timeout flag would promise behavior that is absent. Existing pgx pool/statement
bounds remain; no unqualified transaction-pool mode or arbitrary pool-off mode is added.
ASGI_THREADS and DJANGO_SETTINGS_MODULE are also refused as retired Python worker/profile
settings; native mode derives from explicit --dev / DJANGO_DEBUG and security settings.

Optional SENTRY_DSN selects an isolated sentry-go v0.49.0 client. Empty/unset leaves
reporting disabled without initializing the SDK or consulting its global fallback.
SENTRY_ENVIRONMENT is a bounded production/staging/development label. HTTPS DSNs
must exclude legacy secret passwords, query/fragment data and excessive length.
Tracing/profiling/session/log telemetry stays disabled; the sampling setting accepts
finite zero only. SDK globals, request middleware integrations and default integrations
are not used.

Error capture accepts only constant panic/5xx/startup/job-failure categories,
allowlisted methods and coarse fixed route families. BeforeSend reconstructs an
allowlisted event instead of deleting known sensitive fields. Raw error/panic text,
request bodies, user/identity data, cookies, headers, credentials, IP addresses, query,
stack frames, arbitrary contexts, attachments and breadcrumbs never reach transport.
The SDK timestamp/random event ID and fixed SDK metadata are permitted. Transport
endpoint/project/public DSN key are necessarily Sentry protocol routing metadata;
legacy secret-bearing DSNs are refused. Default HTTP transport rejects redirects,
has a 2 second timeout, ignores proxy/debug wrappers, and capture never waits for I/O.

The application reports its own recovered panic distinctly from generic 5xx responses,
even when request logging is disabled. Its committed-response abort behavior remains.
CLI initialization precedes service startup; failed startup/one-shot commands emit
fixed classes and defer a bounded 2 second shutdown. A fixed bounded worker queue and
bounded SDK queue drop overflow. Flush is best effort, not a delivery guarantee.

## Verification and consequences

Synthetic environment maps, typed policy/unit tests, mock Sentry transports and
assembled HTTP/startup tests qualify validation, disablement, overflow, privacy,
shutdown and real policy effects. PostgreSQL/codec qualification uses an isolated
fixture only; no actual Sentry outbound, provider calls or real ingestion occurs.
Current receipts are recorded in STATUS and WORKLOG; production delivery is unverified.

No shared authentication source is changed by this decision. Production alerting,
legal/provider/minor launch gates and human review remain separate from implementation.
New environment names are registered by the coordinating session in agent-ops.

## Primary SDK references

- [sentry-go v0.49.0 release](https://github.com/getsentry/sentry-go/releases/tag/v0.49.0)
- [Client options and hooks](https://github.com/getsentry/sentry-go/blob/v0.49.0/client.go)
- [Data-collection controls](https://github.com/getsentry/sentry-go/blob/v0.49.0/data_collection.go)
- [Bounded asynchronous transport/flush](https://github.com/getsentry/sentry-go/blob/v0.49.0/transport.go)
- [DSN protocol serialization](https://github.com/getsentry/sentry-go/blob/v0.49.0/internal/protocol/dsn.go)
