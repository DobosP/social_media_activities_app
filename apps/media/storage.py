import hashlib
import hmac
import time
from pathlib import Path

from django.conf import settings
from django.urls import reverse
from django.utils.module_loading import import_string


class StorageBackend:
    """Blob storage seam. Postgres keeps relational/geo data; image bytes live in
    cheap object storage. Production swaps in an S3-compatible backend (Cloudflare
    R2 / MinIO) via settings.MEDIA_STORAGE_BACKEND, returning presigned, expiring
    URLs. The default below stores on the local filesystem for dev/CI."""

    def save(self, key: str, data: bytes, content_type: str) -> None:
        raise NotImplementedError

    def delete(self, key: str) -> None:
        raise NotImplementedError

    def signed_url(self, key: str, *, expires_in: int = 300) -> str:
        raise NotImplementedError


class LocalStorageBackend(StorageBackend):
    def _root(self) -> Path:
        root = Path(getattr(settings, "MEDIA_ROOT", settings.BASE_DIR / "media_store"))
        root.mkdir(parents=True, exist_ok=True)
        return root

    def _path(self, key: str) -> Path:
        # Keys are app-generated (uuid-based); guard against traversal anyway.
        safe = key.replace("..", "").lstrip("/")
        path = self._root() / safe
        path.parent.mkdir(parents=True, exist_ok=True)
        return path

    def save(self, key: str, data: bytes, content_type: str) -> None:
        self._path(key).write_bytes(data)

    def delete(self, key: str) -> None:
        self._path(key).unlink(missing_ok=True)

    def read(self, key: str) -> bytes:
        return self._path(key).read_bytes()

    def signed_url(self, key: str, *, expires_in: int = 300) -> str:
        expires = int(time.time()) + expires_in
        token = sign_key(key, expires)
        return f"{reverse('media-serve', args=[key])}?expires={expires}&token={token}"


def sign_key(key: str, expires: int) -> str:
    message = f"{key}:{expires}".encode()
    return hmac.new(settings.SECRET_KEY.encode(), message, hashlib.sha256).hexdigest()


def verify_signature(key: str, expires: int, token: str) -> bool:
    if expires < time.time():
        return False
    return hmac.compare_digest(sign_key(key, expires), token)


def get_storage_backend() -> StorageBackend:
    path = getattr(settings, "MEDIA_STORAGE_BACKEND", "apps.media.storage.LocalStorageBackend")
    return import_string(path)()
