# ADR-0010: Authorized Presigned Media Egress

Date: 2026-07-04
Status: accepted

## Decision
Media-serving views may redirect an already-authorized viewer to a short-lived object-storage
presigned GET URL when `MEDIA_REDIRECT_TO_PRESIGNED=True` and the configured storage backend
implements `presigned_get_url`; local filesystem storage continues to return no presign URL and
streams through Django.

## Context / why
Private media reads currently enforce authorization in Django and then stream every blob through the
app process, which is the largest avoidable byte-egress pressure point for a single ASGI deployment.
The redirect cannot be public media: the signed app token is viewer-bound, the view resolves it and
re-checks current membership/cohort/visibility before calling the backend presign seam, and the
object URL lifetime is intentionally shorter than the app token lifetime. Provisioning a bucket,
credentials, or CDN remains an operational task outside this code decision.

## Consequences
S3-compatible storage can serve authorized media bytes directly without weakening the existing local
development path. While a presigned URL is live, a later block, consent change, moderation hide, or
ephemeral expiry is not enforced until the short presign TTL expires, so deployments should keep
`MEDIA_PRESIGNED_TTL` small and leave `MEDIA_REDIRECT_TO_PRESIGNED` off unless object storage is
private and correctly configured.
