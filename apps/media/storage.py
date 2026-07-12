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

    def save_fileobj(self, key: str, fileobj, *, content_type: str | None = None) -> None:
        """Store from a readable binary file object WITHOUT buffering the whole payload in
        memory where the backend can avoid it (ADR-0026: video uploads are tens of MB).
        Concrete fallback reads the object fully — correct for any backend, overridden where
        streaming is possible."""
        fileobj.seek(0)
        self.save(key, fileobj.read(), content_type=content_type)

    def download_to(self, key: str, path: str) -> None:
        """Fetch a stored object to a local file path (the transcode worker's scratch copy).
        Concrete fallback goes through bytes; overridden where the backend can stream."""
        with open(path, "wb") as fh:
            fh.write(self.open(key))

    def size(self, key: str) -> int:
        """Stored object size in bytes WITHOUT reading the body where the backend can avoid
        it (ADR-0026: HTTP Range serving of tens-of-MB videos must not load the whole clip
        per seek). Concrete fallback reads; both shipped backends override."""
        return len(self.open(key))

    def open_range(self, key: str, start: int, end: int) -> bytes:
        """Read bytes [start, end] inclusive. Concrete fallback slices a full read; both
        shipped backends fetch only the requested window."""
        return self.open(key)[start : end + 1]


class LocalStorageBackend(StorageBackend):
    """Filesystem-backed storage under MEDIA_ROOT/uploads. Not for production scale,
    but exercises the same interface as the S3 backend."""

    def __init__(self):
        self.root = Path(settings.MEDIA_ROOT) / "uploads"
        self.root.mkdir(parents=True, exist_ok=True)

    def _path(self, key: str) -> Path:
        # Keys are app-generated. Allow app-owned subdirectories (activity-covers/...) while
        # rejecting traversal and absolute paths defensively.
        path = Path(key)
        if path.is_absolute() or "\\" in key or ".." in path.parts:
            raise ValueError("Invalid storage key.")
        return self.root / path

    def save(self, key: str, data: bytes, *, content_type: str | None = None) -> None:
        # content_type is irrelevant on the filesystem (the serving view sets it from the model).
        path = self._path(key)
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)

    def open(self, key: str) -> bytes:
        return self._path(key).read_bytes()

    def exists(self, key: str) -> bool:
        return self._path(key).exists()

    def delete(self, key: str) -> None:
        path = self._path(key)
        if path.exists():
            path.unlink()

    def save_fileobj(self, key: str, fileobj, *, content_type: str | None = None) -> None:
        import shutil

        path = self._path(key)
        path.parent.mkdir(parents=True, exist_ok=True)
        fileobj.seek(0)
        with open(path, "wb") as fh:
            shutil.copyfileobj(fileobj, fh)

    def download_to(self, key: str, path: str) -> None:
        import shutil

        shutil.copyfile(self._path(key), path)

    def size(self, key: str) -> int:
        return self._path(key).stat().st_size

    def open_range(self, key: str, start: int, end: int) -> bytes:
        with open(self._path(key), "rb") as fh:
            fh.seek(start)
            return fh.read(end - start + 1)


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

    def save_fileobj(self, key: str, fileobj, *, content_type: str | None = None) -> None:
        extra = {}
        if content_type:
            extra["ContentType"] = content_type
        sse = getattr(settings, "MEDIA_S3_SSE", "")
        if sse:
            extra["ServerSideEncryption"] = sse
        fileobj.seek(0)
        # Multipart streaming upload — the object is never fully buffered in the app process.
        self._client.upload_fileobj(fileobj, self.bucket, key, ExtraArgs=extra or None)

    def download_to(self, key: str, path: str) -> None:
        self._client.download_file(self.bucket, key, path)

    def size(self, key: str) -> int:
        return self._client.head_object(Bucket=self.bucket, Key=key)["ContentLength"]

    def open_range(self, key: str, start: int, end: int) -> bytes:
        obj = self._client.get_object(Bucket=self.bucket, Key=key, Range=f"bytes={start}-{end}")
        return obj["Body"].read()

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
