# ADR-0044 — Avatar uniqueness stays inside the cohort; fingerprints only for avatars

Date: 2026-10-05
Status: accepted (audit findings GO-MEDIA-01/02, reference parity, 2026-10-05); landing per ADR-0040; human privacy review gates first deployment
Extends: [ADR-0026](0026-private-thread-video-and-sota-image-compression.md), [ADR-0027](0027-avatar-styles-uniqueness-registry.md).

## Decision

- **Cohort-scoped uniqueness.** The profile-photo duplicate check (exact digest or perceptual distance)
  compares only against avatars of users in the uploader's committed cohort, read inside the upload
  transaction (unassigned compares with unassigned, as the reference does). An adult can no longer
  learn whether an image is a child's avatar, and a child is never refused an image an adult uses.
- **Fingerprints for avatars only (reference W8-0).** `media_photo.phash` and the stored processing
  manifest carry a perceptual hash only for profile photos. Thread photos, attachments and covers store
  none. Their manifests also drop the original-upload digest, except video (the worker reprocesses
  against it) and Wikimedia place covers (public licensing provenance for a Commons file).
- **Audit data.** Success-path upload/attach/cover audit events no longer record the original-upload
  digest; blocked-scan audits keep it for moderation, as in the reference.
- **Existing rows** are scrubbed idempotently at schema bootstrap. The hash-chained `safety_auditlog`
  is the documented exception: historical success rows keep their digest, because rewriting them would
  break the chain. No pre-launch deployment holds such rows; a chain-aware re-seal is the only way to
  remove them later.

## Consequences

Private thread media no longer carry a cross-thread correlation signal in application tables. Exact
re-encoded content hashes (`sha256` of the stored rendition) remain, as they did in the reference.
