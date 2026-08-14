# GENERATED FILE - DO NOT EDIT.
#
# Vendored copy of the canonical RO-EDU `/v1` client (ADR-0069).
#
#     canonical:  romania_scraper/romania_scraper/dataapi/roedu_client.py
#     regenerate: python3 scripts/sync_roedu_client.py --write   (in romania_scraper)
#
# Edit the canonical file and re-run the sync script; never edit this copy. The
# VENDORED_SHA256 stamp at the end of this file digests the region between the
# BEGIN/END markers, and this repo's own drift test checks it.
# --- BEGIN VENDORED romania_scraper/dataapi/roedu_client.py ---
"""Canonical RO-EDU `/v1` HTTP client — the ONE source of truth (ADR-0069).

This repository defines the `/v1` contract (``dataapi/products.py`` + ``auth.py``),
so it also owns the consumer-side client for it. Consumer apps do not depend on
this package; they carry a **vendored copy** of this exact file, produced by
``scripts/sync_roedu_client.py``.

    canonical:  romania_scraper/dataapi/roedu_client.py   <- edit HERE
    vendored:   ro_teacher      screener/apps/learning/_roedu_client_core.py
                social          apps/ingestion/sources/_roedu_client_core.py
                cat_de_roman    cat_de_roman_esti/_roedu_client_core.py

Every copy carries a `VENDORED_SHA256` stamp of its own body, so a hand-edit in a
consumer is caught by that repo's own drift test; re-running the sync script is
idempotent, so staleness shows up as a diff.

Deliberately **stdlib-only** (a test enforces it): a vendored file must drop into a
Django app that keeps its runtime dependencies deliberately tiny, and must not drag
this producer package along.

Why this exists: the five transport methods below were byte-identical in all three
consumers, while `iter` had drifted into three variants — and only ro_teacher's
guarded against a server repeating a pagination cursor. The other two could loop
forever on a broken or hostile server. One contract deserves one client.

Page identity (ADR-0117 §2): every page's `snapshot_id`, when present, is one
of two disjoint grammars — `"sha256-" + <64 hex>` is a verified, re-resolvable,
pinnable release id (a promoted `ro_data_server`); `"live-" + <64 hex>` is an
observed generation on unpinned live data, proving only that the stores a page
read did not change between two observations on that host. A `live-` value is
never a pin and is never re-resolvable; `release_id` is `null` alongside it.
`pages()` refuses (`RoeduContractError`) a `snapshot_id` matching neither
grammar, and refuses a torn read (`available=false` with the
`"snapshot changed during read"` note) instead of ending iteration as if it
were a clean end of corpus — the two outcomes were indistinguishable before
ADR-0117.
"""

from __future__ import annotations

import json
import os
import re
import urllib.parse
import urllib.request
from collections.abc import Iterator

__all__ = ["RoeduClient", "RoeduContractError"]

# ADR-0117 §2: every page's `snapshot_id` is one of two disjoint grammars.
# `sha256-` means verified content-addressed identity (a promoted, pinnable
# release); `live-` means an observed generation on unpinned live data
# (tear-detection only, never re-resolvable, never a pin). A value matching
# neither is a contract violation this client refuses rather than forwards.
# Matched with `fullmatch`, NOT `match` + `$`: Python's `$` also matches just before a
# trailing newline, so `"sha256-" + 64hex + "\n"` slipped through this refusal and reached
# consumers as a pinnable release id. JSON strings carry `\n` freely, so it was reachable by
# exactly the case this check exists for — a server answering outside the contract.
_SNAPSHOT_ID_RE = re.compile(r"(?:sha256|live)-[0-9a-f]{64}")

# ADR-0117 §1: the exact `note` a page carries when its covered stores changed
# between the first and the last observation of one page's read (a torn read,
# refused rather than served). Duplicated here rather than imported: this file
# is vendored stdlib-only into four consumer repos (ADR-0069) and must not
# depend on the producer package.
_SNAPSHOT_CHANGED_NOTE = "snapshot changed during read"


class RoeduContractError(ValueError):
    """The API answered in a shape the `/v1` contract forbids."""


class RoeduClient:
    """Read-only client for the RO-EDU data API.

    Consumers subclass this to add their own domain layer (app-pack reading,
    record normalization); the transport and pagination semantics stay here so
    every consumer inherits the same guarantees.
    """

    def __init__(
        self,
        base_url: str | None = None,
        api_key: str | None = None,
        *,
        timeout: float = 30.0,
    ) -> None:
        default_url = os.environ.get("ROEDU_API_URL", "http://localhost:8077")
        self.base_url = (base_url or default_url).rstrip("/")
        self.api_key = api_key or os.environ.get("ROEDU_API_KEY", "")
        self.timeout = timeout

    def _get(self, path: str, params: dict | None = None) -> dict:
        url = f"{self.base_url}{path}"
        if params:
            clean = {k: v for k, v in params.items() if v is not None}
            url += "?" + urllib.parse.urlencode(clean)
        headers = {"X-API-Key": self.api_key, "Accept": "application/json"}
        req = urllib.request.Request(url, headers=headers)
        with urllib.request.urlopen(req, timeout=self.timeout) as resp:  # noqa: S310
            return json.loads(resp.read().decode("utf-8"))

    def health(self) -> dict:
        return self._get("/v1/health")

    def products(self) -> list[dict]:
        return self._get("/v1/products")

    def page(self, product: str, *, cursor: str | None = None, limit: int = 200, **filters) -> dict:
        params = {"cursor": cursor, "limit": limit, **filters}
        return self._get(f"/v1/products/{product}", params)

    def pages(self, product: str, *, limit: int = 200, **filters) -> Iterator[dict]:
        """Yield complete product pages while retaining snapshot/trace metadata.

        Consumers that only need records should use :meth:`iter`. Evidence-sensitive
        consumers (notably curriculum RAG) use this so page-level
        ``snapshot_id``/``release_id`` are not discarded before persistence.

        A repeated cursor **fails closed** rather than looping forever on a broken
        or hostile server. This guard previously existed in only one of the three
        consumer copies; it is the main reason this file is shared.

        Two more fail-closed checks, both ADR-0117: an available page's
        ``snapshot_id`` must match the ``sha256-``/``live-`` grammar (§2) —
        anything else is a server that answered outside the contract; and an
        unavailable page whose ``note`` is the torn-read marker ends the walk
        with an error rather than silently, so a caller cannot mistake a torn
        read for a clean end of corpus (previously indistinguishable).
        """

        cursor = None
        seen_cursors: set[str] = set()
        while True:
            page = self.page(product, cursor=cursor, limit=limit, **filters)
            available = page.get("available", False)
            snapshot_id = page.get("snapshot_id")
            if (
                available
                and snapshot_id is not None
                and not _SNAPSHOT_ID_RE.fullmatch(str(snapshot_id))
            ):
                raise RoeduContractError(
                    f"RO-EDU API returned an unrecognised snapshot_id grammar: {snapshot_id!r}"
                )
            yield page
            if not available:
                if page.get("note") == _SNAPSHOT_CHANGED_NOTE:
                    raise RoeduContractError(
                        "RO-EDU API torn read: " + _SNAPSHOT_CHANGED_NOTE
                    )
                return
            next_cursor = page.get("next_cursor")
            if not next_cursor:
                return
            next_cursor = str(next_cursor)
            if next_cursor in seen_cursors:
                raise RoeduContractError("RO-EDU API repeated a pagination cursor")
            seen_cursors.add(next_cursor)
            cursor = next_cursor

    def iter(
        self, product: str, *, limit: int = 200, max_records: int | None = None, **filters
    ) -> Iterator[dict]:
        """Yield every record of a product, following cursors. Stops at max_records.

        Fail-closed: a page with ``available=false`` (gate refusal / store not
        built) ends the iteration without yielding partial or fabricated records.
        """
        seen = 0
        for page in self.pages(product, limit=limit, **filters):
            if not page.get("available", False):
                return
            for rec in page.get("records", []):
                yield rec
                seen += 1
                if max_records and seen >= max_records:
                    return
# --- END VENDORED ---

VENDORED_SHA256 = "c781c790dc4630be7836214e7777426dabf51b3c6e6b965e5f748575abb63679"
