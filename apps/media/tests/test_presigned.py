"""P1 scale (opt-in): the media-serving views can redirect an AUTHORIZED viewer to a short-lived
presigned object-store URL instead of streaming the bytes through the app process. Default OFF
(secure streaming preserved); the local filesystem backend can never presign, so it always streams.
"""

import hashlib
from io import BytesIO

import pytest
from django.test import Client, override_settings
from PIL import Image

from apps.accounts.identity.base import AssuranceResult
from apps.accounts.models import AgeBand, User
from apps.accounts.services import apply_assurance
from apps.media import services as media
from apps.media.models import Photo

pytestmark = pytest.mark.django_db


def _user(name):
    u = User.objects.create_user(username=name, password="pw", display_name=name)
    apply_assurance(u, AssuranceResult(age_band=AgeBand.ADULT, provider="dev"))
    return u


def _png(color=(10, 120, 200), size=(8, 8)) -> bytes:
    img = Image.new("RGB", size, color)
    out = BytesIO()
    img.save(out, format="PNG")
    return out.getvalue()


def _client(user):
    c = Client()
    c.force_login(user)
    return c


class _FakePresignBackend:
    def __init__(self):
        self.calls = []

    def presigned_get_url(self, key, *, expires_in, content_type=None, content_disposition=None):
        self.calls.append((key, expires_in, content_type, content_disposition))
        cd = content_disposition or ""
        return f"https://cdn.example/{key}?ct={content_type}&cd={cd}&ttl={expires_in}"


def test_maybe_presigned_off_by_default(monkeypatch):
    backend = _FakePresignBackend()
    monkeypatch.setattr(media, "get_storage", lambda: backend)
    # Flag defaults False -> stream (return None) even though the backend COULD presign.
    assert media.maybe_presigned_url("k.webp", content_type="image/webp") is None
    assert backend.calls == []


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True)
def test_maybe_presigned_on_returns_backend_url(monkeypatch):
    backend = _FakePresignBackend()
    monkeypatch.setattr(media, "get_storage", lambda: backend)
    url = media.maybe_presigned_url("k.pdf", content_type="application/pdf", download_name="d.pdf")
    assert url.startswith("https://cdn.example/k.pdf")
    assert "ct=application/pdf" in url
    assert "attachment" in url  # the PDF forced-download disposition is passed through
    assert backend.calls == [("k.pdf", 60, "application/pdf", 'attachment; filename="d.pdf"')]


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True)
def test_local_backend_never_presigns_so_it_streams():
    # The default (test) backend is LocalStorageBackend, whose presigned_get_url returns None,
    # so maybe_presigned_url returns None even with the flag ON — the app keeps streaming. This is
    # the safety net: enabling the flag can never accidentally break local/filesystem serving.
    assert media.maybe_presigned_url("k.webp", content_type="image/webp") is None


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True, MEDIA_PRESIGNED_TTL=17)
def test_authorized_media_file_redirects_to_presigned_url(monkeypatch):
    owner = _user("pre_owner")
    photo = media.upload_photo(owner, Photo.Kind.PROFILE, _png())
    backend = _FakePresignBackend()
    monkeypatch.setattr(media, "get_storage", lambda: backend)

    resp = _client(owner).get(media.signed_url(photo, owner))

    assert resp.status_code == 307
    assert resp["Location"].startswith(f"https://cdn.example/{photo.storage_key}")
    assert backend.calls == [(photo.storage_key, 17, photo.content_type, None)]


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True)
def test_unauthorized_media_file_is_denied_before_presign(monkeypatch):
    owner = _user("pre_owner_denied")
    outsider = _user("pre_outsider_denied")
    photo = media.upload_photo(owner, Photo.Kind.PROFILE, _png())
    backend = _FakePresignBackend()
    monkeypatch.setattr(media, "get_storage", lambda: backend)

    resp = _client(outsider).get(media.signed_url(photo, owner))

    assert resp.status_code == 403
    assert "Location" not in resp
    assert backend.calls == []
    assert photo.storage_key.encode() not in resp.content


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True)
def test_local_media_file_falls_back_to_streaming():
    owner = _user("pre_local")
    photo = media.upload_photo(owner, Photo.Kind.PROFILE, _png())

    resp = _client(owner).get(media.signed_url(photo, owner))

    assert resp.status_code == 200
    assert resp["Content-Type"] == photo.content_type
    assert resp["X-Content-Type-Options"] == "nosniff"
    assert resp.content


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True, MEDIA_REQUIRE_SCANNER=True)
def test_presigned_redirect_flag_does_not_bypass_fail_closed_upload():
    owner = _user("pre_blocked")
    data = _png(color=(9, 9, 9))
    digest = hashlib.sha256(data).hexdigest()

    with override_settings(MEDIA_CSAM_HASH_BLOCKLIST=[digest]):
        with pytest.raises(media.MediaRejected):
            media.upload_photo(owner, Photo.Kind.PROFILE, data)

    assert not Photo.objects.filter(uploader=owner).exists()
