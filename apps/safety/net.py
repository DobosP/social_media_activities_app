"""SSRF-hardened outbound HTTP for server-side fetchers.

Every place/event/media integration that pulls a URL from outside (an iCal feed URL,
the Overpass/Wikidata/Google Places endpoints, a managed scanner callback) is a
server-side request the attacker may influence. Left raw, ``requests.get(url)`` will
happily fetch ``http://169.254.169.254/`` (cloud metadata), ``http://127.0.0.1:…``
(internal admin), or ``file://``-adjacent schemes via redirects — classic SSRF.

:func:`safe_get` is the single hardened entry point the fetchers use instead of
``requests.get``/``requests.post``:

* only ``http``/``https`` schemes are allowed;
* the host is resolved with :func:`socket.getaddrinfo` and **every** resolved
  address is checked — private, loopback, link-local, reserved, multicast and
  unspecified ranges are rejected (blocks the metadata IP and internal hosts);
  NOTE: this is a pre-flight check, not pinning — ``requests`` re-resolves the host
  when connecting, so a TOCTOU **DNS-rebinding** attacker who flips the record between
  validation and connect is NOT fully defeated. Acceptable here because these URLs are
  operator/feed-configured, not arbitrary per-request user input; pinning the validated
  IP (connect-by-IP + Host/SNI) is a hardening follow-up before exposing this to
  user-supplied URLs;
* redirects are disabled by default (a redirect to an internal host would bypass
  the pre-flight check); callers that must follow redirects re-enter ``safe_get``
  per hop;
* the body is streamed with a hard byte cap so a hostile/huge response cannot
  exhaust memory; and
* a short timeout is enforced.

The returned object is the underlying :class:`requests.Response` (already fully read
and capped), so existing call sites keep using ``.text`` / ``.json()`` /
``.status_code`` / ``.raise_for_status()`` unchanged.
"""

from __future__ import annotations

import ipaddress
import socket
from urllib.parse import urlsplit

DEFAULT_TIMEOUT = 15
# A generous default cap; callers pass an explicit ``max_bytes`` for their payload.
DEFAULT_MAX_BYTES = 10 * 1024 * 1024


class UnsafeURLError(ValueError):
    """Raised when a URL is rejected before any network connection is made."""


def _ip_is_blocked(ip: ipaddress.IPv4Address | ipaddress.IPv6Address) -> bool:
    """Reject anything that is not a normal, routable public address.

    ``is_global`` is the inverse we want, but we check the specific categories
    explicitly so the rejection reason is auditable and so IPv4-mapped IPv6
    (``::ffff:169.254.169.254``) is unwrapped first.
    """
    if getattr(ip, "ipv4_mapped", None) is not None:
        ip = ip.ipv4_mapped
    return (
        ip.is_private
        or ip.is_loopback
        or ip.is_link_local
        or ip.is_reserved
        or ip.is_multicast
        or ip.is_unspecified
    )


def _validate_host(host: str) -> None:
    """Resolve ``host`` and reject if ANY resolved address is non-public.

    Resolving here (rather than trusting the literal host) catches both hostnames
    that point at internal IPs and bare-IP URLs. We require every address to be
    safe: a name that resolves to one public and one internal address is rejected.
    """
    if not host:
        raise UnsafeURLError("URL has no host.")

    # A bracketed/bare IP literal still goes through getaddrinfo, but guard the
    # obvious literal case first for a clearer message.
    literal = host.strip("[]")
    try:
        ip = ipaddress.ip_address(literal)
    except ValueError:
        ip = None
    if ip is not None and _ip_is_blocked(ip):
        raise UnsafeURLError(f"URL host resolves to a non-public address: {ip}")

    try:
        infos = socket.getaddrinfo(host, None)
    except OSError as exc:
        raise UnsafeURLError(f"Could not resolve host {host!r}: {exc}") from exc

    if not infos:
        raise UnsafeURLError(f"Host {host!r} did not resolve to any address.")

    for info in infos:
        sockaddr = info[4]
        addr = sockaddr[0]
        try:
            resolved = ipaddress.ip_address(addr)
        except ValueError as exc:
            raise UnsafeURLError(
                f"Host {host!r} resolved to an unparseable address: {addr!r}"
            ) from exc
        if _ip_is_blocked(resolved):
            raise UnsafeURLError(f"Host {host!r} resolves to a non-public address: {resolved}")


def safe_get(
    url: str,
    *,
    max_bytes: int = DEFAULT_MAX_BYTES,
    timeout: float = DEFAULT_TIMEOUT,
    method: str = "GET",
    **kwargs,
):
    """Make an SSRF-safe outbound HTTP request and return the (capped) response.

    Args:
        url: Absolute http(s) URL to fetch.
        max_bytes: Hard cap on the response body; exceeding it raises ``UnsafeURLError``.
        timeout: Per-request socket timeout (seconds).
        method: HTTP method ("GET"/"POST"/…). Defaults to GET.
        **kwargs: Forwarded to ``requests.request`` (``params``, ``data``, ``json``,
            ``headers``, …). ``allow_redirects`` and ``stream`` are forced safe and
            cannot be overridden.

    Returns:
        A ``requests.Response`` whose body has already been read (so ``.content`` /
        ``.text`` / ``.json()`` work) but never exceeds ``max_bytes``.
    """
    # Lazy import to match the surrounding fetcher style and keep import-time light.
    import requests

    parts = urlsplit(url)
    if parts.scheme not in ("http", "https"):
        raise UnsafeURLError(f"Refusing non-http(s) URL scheme: {parts.scheme!r}")

    _validate_host(parts.hostname or "")

    # Force the safe transport options regardless of what the caller passed.
    kwargs.pop("allow_redirects", None)
    kwargs.pop("stream", None)

    resp = requests.request(
        method,
        url,
        timeout=timeout,
        allow_redirects=False,
        stream=True,
        **kwargs,
    )

    # Stream the body with a hard cap so an attacker-controlled endpoint can't
    # exhaust memory. We read one byte past the cap to detect an over-limit body.
    chunks: list[bytes] = []
    total = 0
    try:
        for chunk in resp.iter_content(chunk_size=64 * 1024):
            if not chunk:
                continue
            total += len(chunk)
            if total > max_bytes:
                resp.close()
                raise UnsafeURLError(f"Response exceeded the {max_bytes}-byte cap.")
            chunks.append(chunk)
    finally:
        resp.close()

    # Re-seat the fully-read body so downstream ``.text`` / ``.json()`` behave like a
    # normal (non-streamed) response.
    resp._content = b"".join(chunks)
    resp._content_consumed = True
    return resp
