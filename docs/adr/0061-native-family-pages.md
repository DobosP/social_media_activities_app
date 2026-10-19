# ADR-0061: Native family management and ward explanation

Date: 2026-10-19
Status: proposed — source reviewed; focused Go checks pass; PG/desktop qualification pending

## Context and decision

Programme ADR-0032 authorizes selected functional Go HTML rebuilds. Rebuild only
the existing `/wards/` and `/guardianship/` views around the present account,
guardrail, recommendation and messaging services. Preserve logical template names,
registered methods, current session/Actor reload, CSRF and Origin admission. This
is neither S3A acceptance nor authorization for real-child onboarding or deployment.

The selected names use cached embedded `html/template` output after the existing
Renderer defaults and data merge; its common cache, Vary, CSP and reporting headers
remain shared. Other names keep cached Pongo. A strict typed projector accepts only
the existing authorized row shapes, builds local named action URLs from positive
owned IDs, and passes ordinary escaped strings. Existing catalog msgids remain the
translation source. Two finite EN/RO unavailable-invitation explanations describe
an environment switch or a missing verified-adult condition without claiming legal
proof. Original templates, tests, route manifests and catalog bytes remain.

Guardian pages retain the four current ordinary forms. Repeated names are exactly
`allowed_weekdays`, `allowed_categories` and `topics`. Optional nil limits differ
from numeric zero. Stored selected categories outside the top-level menu remain
explicit checked plain-text options so a valid restriction cannot disappear on
save. Existing bounded custom relationship labels remain escaped text. The service
still validates every mutation; topic nudges retain known-topic filtering rather
than acquiring hard-permission semantics. Limits shown are each guardian's chosen
values, with the existing combined-block warning; no browser permission union or
unprojected effective bound is invented.

The ward page is read-only and has no family mutation URL or identifier. Child-only
meeting logistics, observer and arrival explanations follow existing projection
capabilities. Revocation uses a native disclosure with consequence text and the
existing CSRF POST; it does not depend on JavaScript. Invitations stay pending and
uniformly acknowledged under existing policy; acceptance stays in the existing
home workflow. ADR-0045 block, co-guardian and adulthood rules are unchanged.

## Verification and limits

New pure projection/template/header tests, registered-route PostgreSQL boundary
and mandatory-audit rollback cases, and assembled-App real-session/CSRF tests are
prepared. Focused family race/vet, header and current-custody checks pass; actual
PostgreSQL/desktop qualification is pending. Existing assertions remain. The exact
renderer custody inverse is independently reviewed; historical bytes, captures
and receipts remain unchanged. Qualify source/authcore hashes, actual
synthetic PG, desktop/no-JS/a11y and matched warmed rendering. No production cost,
legal compliance, security certification, formal milestone or performance result
is established by this source proposal. Full landing gates and human first-launch
review remain as ADR-0040; Teacher/Social milestone landing remains owner-only.
