# ADR ledger — claimed numbers

Claim the next free number **here, in the same commit as the ADR file**, so two parallel worktrees
never mint the same number. Next free number: **0050** (0041–0049 reserved 2026-10-05 for the audit fix sessions: G2 0041/0043–0046, G3 0042/0047–0049). Template: [`0000-template.md`](0000-template.md).
ADRs are append-only: a reversal is a new ADR that flips the old one's `Status:` to `superseded-by ADR-NNNN`.
On conflict: `STATUS.md` > newest-dated ADR > every other doc.

| # | Slug | Status | Date |
|---|---|---|---|
| 0001 | [launch-hosting-hetzner-single-box](0001-launch-hosting-hetzner-single-box.md) | accepted | 2026-06-19 |
| 0002 | [cohort-connections-policy](0002-cohort-connections-policy.md) | accepted | 2026-05-30 |
| 0003 | [no-celery-postgres-deferredtask](0003-no-celery-postgres-deferredtask.md) | accepted | 2026-06-23 |
| 0004 | [media-screening-hash-blocklist-first](0004-media-screening-hash-blocklist-first.md) | accepted (its "no video" line superseded by ADR-0026) | 2026-06-13 |
| 0005 | [dependency-pinning-policy](0005-dependency-pinning-policy.md) | accepted | 2026-05-27 |
| 0006 | [e2ee-messaging-access-control-over-scanning](0006-e2ee-messaging-access-control-over-scanning.md) | accepted | 2026-05-29 |
| 0007 | [mobile-photo-activity-cards](0007-mobile-photo-activity-cards.md) | accepted | 2026-07-03 |
| 0008 | [api-v1-pagination-and-deferred-task-kinds](0008-api-v1-pagination-and-deferred-task-kinds.md) | accepted | 2026-07-04 |
| 0009 ⚠ | [csp-enforcement-prep](0009-csp-enforcement-prep.md) | superseded-by ADR-0010 | 2026-07-04 |
| 0009 ⚠ | [query-retention-and-audit-checkpoints](0009-query-retention-and-audit-checkpoints.md) | accepted | 2026-07-04 |
| 0010 | [csp-style-hardening-and-report-digest](0010-csp-style-hardening-and-report-digest.md) | superseded-by ADR-0014 | 2026-07-04 |
| 0011 | [readiness-and-structured-request-logs](0011-readiness-and-structured-request-logs.md) | accepted | 2026-07-04 |
| 0012 | [media-presigned-egress](0012-media-presigned-egress.md) | accepted | 2026-07-04 |
| 0013 | [graceful-readiness-drain](0013-graceful-readiness-drain.md) | accepted | 2026-07-04 |
| 0014 | [bounded-csp-report-ingestion](0014-bounded-csp-report-ingestion.md) | accepted | 2026-07-04 |
| 0015 | [browser-security-headers](0015-browser-security-headers.md) | accepted | 2026-07-04 |
| 0016 | [react-spa-frontend-migration](0016-react-spa-frontend-migration.md) | accepted | 2026-07-06 |
| 0017 | [roedu-seed-after-migrate](0017-roedu-seed-after-migrate.md) | accepted | 2026-07-07 |
| 0018 | [public-listing-request-field](0018-public-listing-request-field.md) | accepted | 2026-07-07 |
| 0019 | [places-v2-and-ia-redesign](0019-places-v2-and-ia-redesign.md) | accepted (owner direction) | 2026-07-07 |
| 0020 | [multi-type-activities-wizard-live-filters](0020-multi-type-activities-wizard-live-filters.md) | accepted (owner feedback on ADR-0019) | 2026-07-07 |
| 0021 | [map-quality-filters-labels-aggregation](0021-map-quality-filters-labels-aggregation.md) | accepted | 2026-07-07 |
| 0022 | [resource-bounded-asgi-browser-runtime](0022-resource-bounded-asgi-browser-runtime.md) | accepted | 2026-07-11 |
| 0023 | [roedu-event-lifecycle-snapshot-reconciliation](0023-roedu-event-lifecycle-snapshot-reconciliation.md) | accepted | 2026-07-12 |
| 0024 | [canonical-roedu-social-app-pack](0024-canonical-roedu-social-app-pack.md) | accepted | 2026-07-12 |
| 0025 | [agent-and-search-engine-access-surface](0025-agent-and-search-engine-access-surface.md) | accepted | 2026-07-12 |
| 0026 | [private-thread-video-and-sota-image-compression](0026-private-thread-video-and-sota-image-compression.md) | accepted (supersedes the "no video" line of ADR-0004) | 2026-07-13 |
| 0027 | [avatar-styles-uniqueness-registry](0027-avatar-styles-uniqueness-registry.md) | accepted (owner-approved) | 2026-07-13 |
| 0028 | [tiered-profile-visibility](0028-tiered-profile-visibility.md) | accepted (owner-approved tier matrix) | 2026-07-13 |
| 0029 | [plural-sentiment-reactions](0029-plural-sentiment-reactions.md) | accepted (owner decisions) | 2026-07-14 |
| 0030 | [refusal-visibility-and-tick-isolation](0030-refusal-visibility-and-tick-isolation.md) | accepted; one owner decision open (§4 refused-`venues` narrowing) | 2026-08-18 |
| 0031 | [go-public-serving-foundation](0031-go-public-serving-foundation.md) | scope superseded by 0032 | 2026-10-04 |
| 0032 | [complete-native-go-backend](0032-complete-native-go-backend.md) | accepted; landing authorized 2026-10-04, code review pending (ADR-0040) | 2026-10-04 |
| 0033 | [manual-github-actions](0033-manual-github-actions.md) | accepted; explicit owner request | 2026-10-04 |
| 0034 | [native-config-error-observability](0034-native-config-error-observability.md) | implemented; landing per ADR-0040; human review gates first deployment | 2026-10-04 |
| 0035 | [guarded-permissions-private-schema](0035-guarded-permissions-private-schema.md) | proposed; landing per ADR-0040; human auth/privacy review gates first deployment | 2026-10-04 |
| 0036 | [fresh-participation-authority](0036-fresh-participation-authority.md) | proposed; locally qualified, landing per ADR-0040; human review gates first deployment | 2026-10-04 |
| 0037 | [postgresql-shared-rate-budgets](0037-postgresql-shared-rate-budgets.md) | proposed for integration review | 2026-10-04 |
| 0038 | [native-verification-toolchain](0038-native-verification-toolchain.md) | proposed; native qualified, reference retirement blocked; landing per ADR-0040; human review gates first deployment | 2026-10-05 |
| 0039 | [native-source-login-failure-counter](0039-native-source-login-failure-counter.md) | proposed; source restoration qualified, landing per ADR-0040; human auth/privacy review gates first deployment | 2026-10-05 |
| 0040 | [landing-and-deployment-review-gates](0040-landing-and-deployment-review-gates.md) | accepted; owner decision 2026-10-05 | 2026-10-05 |
| 0041 | [report-eligibility-predicate](0041-report-eligibility-predicate.md) | accepted; owner decisions 2026-10-05; human safety review gates first deployment | 2026-10-05 |
| 0043 | [direct-only-block-veto](0043-direct-only-block-veto.md) | accepted; owner decision 2026-10-05 (narrows SAFETY rule 2 to direct chats); human safety review gates first deployment | 2026-10-05 |
| 0044 | [media-fingerprint-minimisation](0044-media-fingerprint-minimisation.md) | accepted; reference parity (audit GO-MEDIA-01/02); human privacy review gates first deployment | 2026-10-05 |
| 0045 | [guardian-authority](0045-guardian-authority.md) | accepted; owner decisions 2026-10-05; human safety review gates first deployment | 2026-10-05 |
| 0046 | [membership-logistics-scope](0046-membership-logistics-scope.md) | accepted; owner decisions 2026-10-05; human privacy review gates first deployment | 2026-10-05 |

⚠ **0009 is claimed twice** — this ledger exists so it does not happen again. Both files stay:
`0009-csp-enforcement-prep.md` (superseded by ADR-0010) and
`0009-query-retention-and-audit-checkpoints.md` (accepted). Cite the slug, not the bare number.
