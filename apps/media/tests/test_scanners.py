"""Unit coverage for the media safety scanners — the fail-CLOSED behaviour that protects a
children's platform. These are pure scanner-class tests (no DB):

  * docscan.ClamdScanner   — INSTREAM clean / infected / fail-closed on a dead daemon;
  * docscan.NoopDocumentScanner + get_document_scanner selection;
  * scanning.ManagedScanner — clean / match / fail-closed on network/HTTP/parse error / no endpoint.

The image HashBlocklistScanner + the MEDIA_REQUIRE_SCANNER gate are covered in test_attachments /
test_media; the PDF document-scan branch in attach_to_post is covered in test_attachments.
"""

import dataclasses

import pytest

from apps.media import docscan
from apps.media.docscan import ClamdScanner, NoopDocumentScanner, get_document_scanner
from apps.media.scanning import ManagedScanner, ScanResult

# --- a fake clamd socket (context manager mirroring socket.create_connection) --------------


class _FakeClamdSocket:
    def __init__(self, reply: bytes):
        self._reply = reply
        self.sent = bytearray()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def sendall(self, b):
        self.sent += b

    def recv(self, _n):
        return self._reply


def _patch_clamd(monkeypatch, *, reply=None, error=None):
    def _connect(*_a, **_k):
        if error is not None:
            raise error
        return _FakeClamdSocket(reply)

    monkeypatch.setattr(docscan.socket, "create_connection", _connect)


# --- ClamdScanner --------------------------------------------------------------------------


def test_clamd_scanner_passes_a_clean_stream(monkeypatch):
    _patch_clamd(monkeypatch, reply=b"stream: OK\0")
    result = ClamdScanner().scan(b"%PDF-1.4 harmless")
    assert result.clean is True


def test_clamd_scanner_blocks_an_infected_stream(monkeypatch):
    _patch_clamd(monkeypatch, reply=b"stream: Eicar-Test-Signature FOUND\0")
    result = ClamdScanner().scan(b"infected")
    assert result.clean is False
    assert "FOUND" in result.matched


def test_clamd_scanner_fails_closed_when_daemon_unreachable(monkeypatch):
    # The whole point: a dead/unreachable clamd must NOT pass content through.
    _patch_clamd(monkeypatch, error=OSError("connection refused"))
    result = ClamdScanner().scan(b"anything")
    assert result.clean is False
    assert result.matched == "clamd_error"


def test_clamd_scanner_blocks_any_non_ok_reply(monkeypatch):
    # The rule is "OK suffix -> clean, anything else -> blocked": an ERROR / size-limit reply
    # (not a FOUND signature, not an OSError) must still fail closed.
    _patch_clamd(monkeypatch, reply=b"INSTREAM size limit exceeded\0")
    assert ClamdScanner().scan(b"big").clean is False


def test_clamd_scanner_effectiveness_tracks_host(settings):
    assert ClamdScanner().is_effective() is True  # default host 127.0.0.1
    # A misconfigured empty host => not effective, so the require gate fails closed.
    settings.MEDIA_CLAMD_HOST = ""
    assert ClamdScanner().is_effective() is False


def test_clamd_scanner_streams_the_bytes_in_instream_framing(monkeypatch):
    # Guards the protocol: zINSTREAM header, then length-prefixed chunks, then a zero terminator.
    captured = {}

    class _Recorder(_FakeClamdSocket):
        def __exit__(self, *exc):
            captured["sent"] = bytes(self.sent)
            return False

    monkeypatch.setattr(
        docscan.socket, "create_connection", lambda *a, **k: _Recorder(b"stream: OK\0")
    )
    ClamdScanner().scan(b"hello")
    assert captured["sent"].startswith(b"zINSTREAM\0")
    assert captured["sent"].endswith(b"\x00\x00\x00\x00")  # final zero-length chunk (terminator)
    assert b"hello" in captured["sent"]


# --- NoopDocumentScanner + selection -------------------------------------------------------


def test_noop_document_scanner_is_not_effective():
    # An honest "I can't screen" so the MEDIA_REQUIRE_DOCUMENT_SCANNER gate fails closed.
    assert NoopDocumentScanner().is_effective() is False


def test_get_document_scanner_default_is_noop():
    assert isinstance(get_document_scanner(), NoopDocumentScanner)


def test_get_document_scanner_honours_setting(settings):
    settings.MEDIA_DOCUMENT_SCANNER = "apps.media.docscan.ClamdScanner"
    assert isinstance(get_document_scanner(), ClamdScanner)


# --- ManagedScanner ------------------------------------------------------------------------


class _FakeResp:
    def __init__(self, payload=None, *, raise_for_status_exc=None):
        self._payload = payload if payload is not None else {}
        self._exc = raise_for_status_exc

    def raise_for_status(self):
        if self._exc is not None:
            raise self._exc

    def json(self):
        return self._payload


def _patch_safe_get(monkeypatch, fn):
    import apps.safety.net as net

    monkeypatch.setattr(net, "safe_get", fn)


def test_managed_scanner_unconfigured_is_not_effective_and_blocks():
    # No endpoint -> not effective (so the require gate fails closed) and scan blocks outright.
    scanner = ManagedScanner()
    assert scanner.is_effective() is False
    result = scanner.scan(b"img")
    assert result.clean is False
    assert result.matched == "scanner_unconfigured"


def test_managed_scanner_passes_a_clean_response(settings, monkeypatch):
    settings.MEDIA_SCANNER_ENDPOINT = "https://scanner.example/screen"
    sent = {}

    def _fake(endpoint, **kwargs):
        sent["endpoint"] = endpoint
        sent["json"] = kwargs.get("json")
        return _FakeResp({"match": False})

    _patch_safe_get(monkeypatch, _fake)
    scanner = ManagedScanner()
    assert scanner.is_effective() is True
    result = scanner.scan(b"clean image")
    assert result.clean is True
    # Privacy: only the SHA-256 is sent, never the bytes.
    assert sent["endpoint"] == "https://scanner.example/screen"
    assert set(sent["json"]) == {"sha256"} and len(sent["json"]["sha256"]) == 64


def test_managed_scanner_blocks_a_match(settings, monkeypatch):
    settings.MEDIA_SCANNER_ENDPOINT = "https://scanner.example/screen"
    _patch_safe_get(monkeypatch, lambda *a, **k: _FakeResp({"match": True}))
    result = ManagedScanner().scan(b"bad image")
    assert result.clean is False
    assert result.matched  # the digest, not empty


def test_managed_scanner_blocks_on_flagged_key(settings, monkeypatch):
    # The service may answer with {"flagged": true} instead of {"match": true}.
    settings.MEDIA_SCANNER_ENDPOINT = "https://scanner.example/screen"
    _patch_safe_get(monkeypatch, lambda *a, **k: _FakeResp({"flagged": True}))
    assert ManagedScanner().scan(b"bad").clean is False


def test_managed_scanner_fails_closed_on_network_error(settings, monkeypatch):
    settings.MEDIA_SCANNER_ENDPOINT = "https://scanner.example/screen"

    def _boom(*a, **k):
        raise RuntimeError("connection reset")

    _patch_safe_get(monkeypatch, _boom)
    result = ManagedScanner().scan(b"img")
    assert result.clean is False
    assert result.matched == "scanner_error"


def test_managed_scanner_fails_closed_on_http_error(settings, monkeypatch):
    settings.MEDIA_SCANNER_ENDPOINT = "https://scanner.example/screen"
    _patch_safe_get(
        monkeypatch, lambda *a, **k: _FakeResp(raise_for_status_exc=RuntimeError("503"))
    )
    result = ManagedScanner().scan(b"img")
    assert result.clean is False and result.matched == "scanner_error"


def test_managed_scanner_fails_closed_on_malformed_body(settings, monkeypatch):
    settings.MEDIA_SCANNER_ENDPOINT = "https://scanner.example/screen"

    class _BadJson(_FakeResp):
        def json(self):
            raise ValueError("not json")

    _patch_safe_get(monkeypatch, lambda *a, **k: _BadJson())
    result = ManagedScanner().scan(b"img")
    assert result.clean is False
    assert result.matched == "scanner_error"


def test_scanresult_is_immutable():
    # ScanResult is frozen so a verdict can't be mutated after the fact.
    with pytest.raises(dataclasses.FrozenInstanceError):
        ScanResult(clean=False).clean = True
