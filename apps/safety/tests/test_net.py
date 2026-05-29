"""SSRF guard tests for :func:`apps.safety.net.safe_get`.

These never hit the network: DNS resolution is monkeypatched via ``socket.getaddrinfo``
and the transport via ``requests.request`` so we exercise the guards (scheme, private/
loopback/link-local/reserved IP rejection, byte cap) deterministically.
"""

import pytest

from apps.safety import net
from apps.safety.net import UnsafeURLError, safe_get


def _patch_resolve(monkeypatch, ip: str):
    """Make ``socket.getaddrinfo`` resolve any host to ``ip`` (one A/AAAA record)."""

    def fake_getaddrinfo(host, port, *args, **kwargs):
        return [(2, 1, 6, "", (ip, 0))]

    monkeypatch.setattr(net.socket, "getaddrinfo", fake_getaddrinfo)


class _FakeResponse:
    """Minimal stand-in for requests.Response with a chunked body for the cap test."""

    def __init__(self, body: bytes, *, chunk_size: int = 64 * 1024):
        self._body = body
        self._chunk_size = chunk_size
        self.status_code = 200
        self._content = b""
        self._content_consumed = False

    def iter_content(self, chunk_size=64 * 1024):
        for i in range(0, len(self._body), self._chunk_size):
            yield self._body[i : i + self._chunk_size]

    def close(self):
        pass


def _patch_transport(monkeypatch, response):
    """Replace ``requests.request`` so no socket is opened; assert redirects are off."""

    def fake_request(method, url, *, timeout, allow_redirects, stream, **kwargs):
        assert allow_redirects is False, "safe_get must disable redirects"
        assert stream is True, "safe_get must stream the body"
        return response

    import requests

    monkeypatch.setattr(requests, "request", fake_request)


def test_rejects_loopback_127(monkeypatch):
    _patch_resolve(monkeypatch, "127.0.0.1")
    with pytest.raises(UnsafeURLError):
        safe_get("http://localhost/whatever", max_bytes=1024)


def test_rejects_cloud_metadata_link_local(monkeypatch):
    # 169.254.169.254 is the canonical cloud metadata endpoint (link-local).
    _patch_resolve(monkeypatch, "169.254.169.254")
    with pytest.raises(UnsafeURLError):
        safe_get("http://metadata.internal/latest/meta-data/", max_bytes=1024)


def test_rejects_private_10_network(monkeypatch):
    _patch_resolve(monkeypatch, "10.1.2.3")
    with pytest.raises(UnsafeURLError):
        safe_get("http://internal.example/", max_bytes=1024)


def test_rejects_bare_private_ip_literal(monkeypatch):
    # A literal private IP is rejected even before DNS (and getaddrinfo is not relied on).
    _patch_resolve(monkeypatch, "192.168.0.5")
    with pytest.raises(UnsafeURLError):
        safe_get("http://192.168.0.5/", max_bytes=1024)


def test_rejects_non_http_scheme(monkeypatch):
    # No resolution should even be attempted for a bad scheme.
    def boom(*args, **kwargs):
        raise AssertionError("getaddrinfo must not be called for a non-http scheme")

    monkeypatch.setattr(net.socket, "getaddrinfo", boom)
    for url in ("file:///etc/passwd", "ftp://host/x", "gopher://host/"):
        with pytest.raises(UnsafeURLError):
            safe_get(url, max_bytes=1024)


def test_enforces_byte_cap(monkeypatch):
    _patch_resolve(monkeypatch, "93.184.216.34")  # public (example.com)
    big = b"x" * (5000)
    _patch_transport(monkeypatch, _FakeResponse(big, chunk_size=1000))
    with pytest.raises(UnsafeURLError):
        safe_get("http://example.com/big", max_bytes=4096)


def test_allows_public_host_and_reads_body(monkeypatch):
    _patch_resolve(monkeypatch, "93.184.216.34")  # public
    body = b"hello world"
    resp = _FakeResponse(body)
    _patch_transport(monkeypatch, resp)
    out = safe_get("http://example.com/ok", max_bytes=1024)
    assert out.status_code == 200
    assert out._content == body
    assert out._content_consumed is True
