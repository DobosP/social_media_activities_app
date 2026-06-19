"""Pluggable blob storage. Postgres keeps relational/geo data; image bytes live in
object storage (S3-compatible R2/MinIO in prod). The default local backend keeps
dev and tests dependency-free."""

from abc import ABC, abstractmethod
from pathlib import Path

from django.conf import settings
from django.core.exceptions import ImproperlyConfigured
from django.utils.module_loading import import_string


class StorageBackend(ABC):
    @abstractmethod
    def save(self, key: str, data: bytes, *, content_type: str | None = None) -> None: ...

    @abstractmethod
    def open(self, key: str) -> bytes: ...

    @abstractmethod
    def exists(self, key: str) -> bool: ...

    @abstractmethod
    def delete(self, key: str) -> None: ...

    def presigned_get_url(
        self, key: str, *, expires_in: int, content_type=None, content_disposition=None
    ) -> str | None:
        """A short-lived URL the client can GET DIRECTLY from the object store (offloading the bytes
        from the app process — the biggest single-process saturation fix). Returns None when the
        backend can't presign (the default; the local filesystem backend always streams). Concrete
        — not abstract — so existing backends keep working without change."""
        return None


class LocalStorageBackend(StorageBackend):
    """Filesystem-backed storage under MEDIA_ROOT/uploads. Not for production scale,
    but exercises the same interface as the S3 backend."""

    def __init__(self):
        self.root = Path(settings.MEDIA_ROOT) / "uploads"
        self.root.mkdir(parents=True, exist_ok=True)

    def _path(self, key: str) -> Path:
        # Keys are app-generated (uuid hex); reject traversal defensively.
        if "/" in key or "\\" in key or ".." in key:
            raise ValueError("Invalid storage key.")
        return self.root / key

    def save(self, key: str, data: bytes, *, content_type: str | None = None) -> None:
        # content_type is irrelevant on the filesystem (the serving view sets it from the model).
        self._path(key).write_bytes(data)

    def open(self, key: str) -> bytes:
        return self._path(key).read_bytes()

    def exists(self, key: str) -> bool:
        return self._path(key).exists()

    def delete(self, key: str) -> None:
        path = self._path(key)
        if path.exists():
            path.unlink()


class S3StorageBackend(StorageBackend):
    """S3-compatible object storage (AWS S3 / Cloudflare R2 / MinIO) for production.

    Credentials come from the environment via boto3's default chain
    (``AWS_ACCESS_KEY_ID`` / ``AWS_SECRET_ACCESS_KEY``); the bucket and optional
    endpoint/region come from settings. Activated by pointing ``MEDIA_STORAGE_BACKEND``
    at this class. Objects are private — they are only ever served through the
    membership-scoped, signed-URL view, never via a public bucket URL."""

    def __init__(self):
        import boto3  # lazy: dev/tests stay dependency-light unless S3 is selected
        from botocore.config import Config

        self.bucket = getattr(settings, "MEDIA_S3_BUCKET", "")
        if not self.bucket:
            raise ImproperlyConfigured("MEDIA_S3_BUCKET must be set to use S3 storage.")
        self._client = boto3.client(
            "s3",
            endpoint_url=getattr(settings, "MEDIA_S3_ENDPOINT_URL", "") or None,
            region_name=getattr(settings, "MEDIA_S3_REGION", "") or None,
            config=Config(
                signature_version="s3v4",
                s3={"addressing_style": getattr(settings, "MEDIA_S3_ADDRESSING_STYLE", "auto")},
            ),
        )

    def save(self, key: str, data: bytes, *, content_type: str | None = None) -> None:
        # Objects are PRIVATE: no ACL is set, so the bucket's (private) default applies — they are
        # only ever reachable through the signed, per-viewer, membership-scoped serving view. Set
        # the stored ContentType for correct object metadata and optional server-side encryption.
        extra = {}
        if content_type:
            extra["ContentType"] = content_type
        sse = getattr(settings, "MEDIA_S3_SSE", "")
        if sse:
            extra["ServerSideEncryption"] = sse
        self._client.put_object(Bucket=self.bucket, Key=key, Body=data, **extra)

    def open(self, key: str) -> bytes:
        return self._client.get_object(Bucket=self.bucket, Key=key)["Body"].read()

    def exists(self, key: str) -> bool:
        from botocore.exceptions import ClientError

        try:
            self._client.head_object(Bucket=self.bucket, Key=key)
            return True
        except ClientError:
            return False

    def delete(self, key: str) -> None:
        self._client.delete_object(Bucket=self.bucket, Key=key)

    def presigned_get_url(
        self, key: str, *, expires_in: int, content_type=None, content_disposition=None
    ) -> str:
        # The caller has ALREADY done the per-viewer membership/cohort access check; this mints a
        # short-lived (expires_in) direct URL. Response-header overrides pin the served content-type
        # and, for a PDF, force a download — so a direct fetch keeps the inline-execution guard.
        params = {"Bucket": self.bucket, "Key": key}
        if content_type:
            params["ResponseContentType"] = content_type
        if content_disposition:
            params["ResponseContentDisposition"] = content_disposition
        return self._client.generate_presigned_url(
            "get_object", Params=params, ExpiresIn=expires_in
        )


def get_storage() -> StorageBackend:
    return import_string(settings.MEDIA_STORAGE_BACKEND)()
