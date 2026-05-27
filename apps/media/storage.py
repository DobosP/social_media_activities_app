"""Pluggable blob storage. Postgres keeps relational/geo data; image bytes live in
object storage (S3-compatible R2/MinIO in prod). The default local backend keeps
dev and tests dependency-free."""

from abc import ABC, abstractmethod
from pathlib import Path

from django.conf import settings
from django.utils.module_loading import import_string


class StorageBackend(ABC):
    @abstractmethod
    def save(self, key: str, data: bytes) -> None: ...

    @abstractmethod
    def open(self, key: str) -> bytes: ...

    @abstractmethod
    def exists(self, key: str) -> bool: ...

    @abstractmethod
    def delete(self, key: str) -> None: ...


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

    def save(self, key: str, data: bytes) -> None:
        self._path(key).write_bytes(data)

    def open(self, key: str) -> bytes:
        return self._path(key).read_bytes()

    def exists(self, key: str) -> bool:
        return self._path(key).exists()

    def delete(self, key: str) -> None:
        path = self._path(key)
        if path.exists():
            path.unlink()


def get_storage() -> StorageBackend:
    return import_string(settings.MEDIA_STORAGE_BACKEND)()
