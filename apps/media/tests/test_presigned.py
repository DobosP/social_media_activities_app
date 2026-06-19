"""P1 scale (opt-in): the media-serving views can redirect an AUTHORIZED viewer to a short-lived
presigned object-store URL instead of streaming the bytes through the app process. Default OFF
(secure streaming preserved); the local filesystem backend can never presign, so it always streams.
"""

from django.test import override_settings

from apps.media import services as media


class _FakePresignBackend:
    def presigned_get_url(self, key, *, expires_in, content_type=None, content_disposition=None):
        cd = content_disposition or ""
        return f"https://cdn.example/{key}?ct={content_type}&cd={cd}&ttl={expires_in}"


def test_maybe_presigned_off_by_default(monkeypatch):
    monkeypatch.setattr(media, "get_storage", lambda: _FakePresignBackend())
    # Flag defaults False -> stream (return None) even though the backend COULD presign.
    assert media.maybe_presigned_url("k.webp", content_type="image/webp") is None


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True)
def test_maybe_presigned_on_returns_backend_url(monkeypatch):
    monkeypatch.setattr(media, "get_storage", lambda: _FakePresignBackend())
    url = media.maybe_presigned_url("k.pdf", content_type="application/pdf", download_name="d.pdf")
    assert url.startswith("https://cdn.example/k.pdf")
    assert "ct=application/pdf" in url
    assert "attachment" in url  # the PDF forced-download disposition is passed through


@override_settings(MEDIA_REDIRECT_TO_PRESIGNED=True)
def test_local_backend_never_presigns_so_it_streams():
    # The default (test) backend is LocalStorageBackend, whose presigned_get_url returns None,
    # so maybe_presigned_url returns None even with the flag ON — the app keeps streaming. This is
    # the safety net: enabling the flag can never accidentally break local/filesystem serving.
    assert media.maybe_presigned_url("k.webp", content_type="image/webp") is None
