"""Unit tests for the vendored RoeduClient (apps/ingestion/sources/roedu_client.py).

No network, no DB: the only I/O boundary is ``urllib.request.urlopen``, which we
monkeypatch to return canned JSON pages. ``SimpleTestCase`` because nothing here
touches the ORM (and the client itself is pure stdlib).
"""

from __future__ import annotations

import io
import json

from django.test import SimpleTestCase

from apps.ingestion.sources import roedu_client as rc
from apps.ingestion.sources.roedu_client import RoeduClient


class _FakeResponse(io.BytesIO):
    """Minimal stand-in for the http.client.HTTPResponse urlopen returns: it is a
    context manager that yields itself and exposes ``.read()`` (inherited from BytesIO)."""

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False


def _canned_urlopen(pages_by_url):
    """Build a fake urlopen that records every Request it sees and replies with the
    canned page registered for that request's *full URL* (path + query)."""
    calls = []

    def fake_urlopen(req, timeout=None):
        calls.append({"url": req.full_url, "headers": dict(req.headers), "timeout": timeout})
        # Match the most specific registered key contained in the requested URL.
        for key, payload in pages_by_url.items():
            if key in req.full_url:
                return _FakeResponse(json.dumps(payload).encode("utf-8"))
        return _FakeResponse(json.dumps({"available": True, "records": []}).encode("utf-8"))

    fake_urlopen.calls = calls
    return fake_urlopen


class RoeduClientConfigTests(SimpleTestCase):
    def test_explicit_args_win_over_env(self):
        client = RoeduClient("http://example.test:8077/", api_key="my-key")
        # base_url has its trailing slash stripped.
        self.assertEqual(client.base_url, "http://example.test:8077")
        self.assertEqual(client.api_key, "my-key")

    def test_falls_back_to_env_defaults(self):
        env = {"ROEDU_API_URL": "http://env-host:9000", "ROEDU_API_KEY": "env-key"}
        with self.modify_environ(env):
            client = RoeduClient()
        self.assertEqual(client.base_url, "http://env-host:9000")
        self.assertEqual(client.api_key, "env-key")

    def test_hardcoded_default_url_when_no_env(self):
        with self.modify_environ({}, clear=("ROEDU_API_URL", "ROEDU_API_KEY")):
            client = RoeduClient()
        self.assertEqual(client.base_url, "http://localhost:8077")
        self.assertEqual(client.api_key, "")

    # --- tiny os.environ context manager (SimpleTestCase has no env helper) ---
    def modify_environ(self, mapping, clear=()):
        import contextlib
        import os

        @contextlib.contextmanager
        def _cm():
            saved = {k: os.environ.get(k) for k in list(mapping) + list(clear)}
            try:
                for k in clear:
                    os.environ.pop(k, None)
                os.environ.update(mapping)
                yield
            finally:
                for k, v in saved.items():
                    if v is None:
                        os.environ.pop(k, None)
                    else:
                        os.environ[k] = v

        return _cm()


class RoeduClientRequestTests(SimpleTestCase):
    def test_sends_api_key_header_and_accepts_json(self):
        fake = _canned_urlopen({"/v1/health": {"status": "ok"}})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="secret")
            self.assertEqual(client.health(), {"status": "ok"})
        # urllib title-cases header keys.
        sent = fake.calls[0]["headers"]
        self.assertEqual(sent.get("X-api-key"), "secret")
        self.assertEqual(sent.get("Accept"), "application/json")
        self.assertEqual(fake.calls[0]["url"], "http://h:8077/v1/health")

    def test_page_builds_url_with_params_and_drops_none(self):
        fake = _canned_urlopen({"/v1/products/venues": {"available": True, "records": []}})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            # cursor=None must be dropped; limit + filters must be present.
            client.page("venues", cursor=None, limit=50, city="Cluj-Napoca")
        url = fake.calls[0]["url"]
        self.assertTrue(url.startswith("http://h:8077/v1/products/venues?"))
        self.assertNotIn("cursor", url)
        self.assertIn("limit=50", url)
        self.assertIn("city=Cluj-Napoca", url)

    def test_page_includes_cursor_when_given(self):
        fake = _canned_urlopen({"/v1/products/venues": {"available": True, "records": []}})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            client.page("venues", cursor="abc123", limit=200)
        self.assertIn("cursor=abc123", fake.calls[0]["url"])

    def test_iter_follows_next_cursor_across_pages(self):
        # /v1/products/venues is requested 3 times; we reply by cursor state. We can't
        # key on cursor inside a single registered URL, so drive responses positionally.
        responses = [
            {"available": True, "records": [{"id": 1}, {"id": 2}], "next_cursor": "c1"},
            {"available": True, "records": [{"id": 3}], "next_cursor": "c2"},
            {"available": True, "records": [{"id": 4}], "next_cursor": None},
        ]
        seen_cursors = []

        def fake_urlopen(req, timeout=None):
            seen_cursors.append("cursor=" in req.full_url and req.full_url.split("cursor=")[1])
            payload = responses.pop(0)
            return _FakeResponse(json.dumps(payload).encode("utf-8"))

        with self._patched(fake_urlopen):
            client = RoeduClient("http://h:8077", api_key="k")
            ids = [r["id"] for r in client.iter("venues", limit=2)]

        self.assertEqual(ids, [1, 2, 3, 4])
        # First call has no cursor; later calls carry the previous next_cursor.
        self.assertEqual(seen_cursors[0], False)
        self.assertIn("c1", seen_cursors[1])
        self.assertIn("c2", seen_cursors[2])

    def test_iter_stops_immediately_when_license_unavailable(self):
        # available=false => fail-closed: yield nothing, even if records present.
        page = {"available": False, "records": [{"id": 1}], "next_cursor": "c1"}
        fake = _canned_urlopen({"/v1/products/venues": page})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            out = list(client.iter("venues"))
        self.assertEqual(out, [])
        # Exactly one request was made; we did NOT follow the cursor.
        self.assertEqual(len(fake.calls), 1)

    def test_iter_defaults_available_missing_to_fail_closed(self):
        # A page with no 'available' key is treated as unavailable.
        fake = _canned_urlopen({"/v1/products/venues": {"records": [{"id": 1}]}})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            self.assertEqual(list(client.iter("venues")), [])

    def test_iter_honors_max_records(self):
        responses = [
            {"available": True, "records": [{"id": i} for i in range(5)], "next_cursor": "c1"},
            {"available": True, "records": [{"id": i} for i in range(5, 10)], "next_cursor": None},
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen):
            client = RoeduClient("http://h:8077", api_key="k")
            ids = [r["id"] for r in client.iter("venues", limit=5, max_records=3)]
        self.assertEqual(ids, [0, 1, 2])

    def test_iter_stops_when_no_next_cursor(self):
        page = {"available": True, "records": [{"id": 1}], "next_cursor": None}
        fake = _canned_urlopen({"/v1/products/venues": page})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            out = list(client.iter("venues"))
        self.assertEqual([r["id"] for r in out], [1])
        self.assertEqual(len(fake.calls), 1)

    def test_app_pack_page_uses_expected_public_path_and_filters(self):
        page = {
            "pack_id": "roedu:social_media_activities_app:events_places:v1",
            "app": "social_media_activities_app",
            "layer": "redistributable",
            "schema_version": 1,
            "items": [],
            "pagination": {"next_cursor": None},
            "withheld": 0,
            "errors": [],
        }
        fake = _canned_urlopen({"/v1/app-packs/social_media_activities_app/events_places": page})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            client.app_pack_page("events_places", city="Cluj-Napoca", kind="venue")
        url = fake.calls[0]["url"]
        self.assertTrue(
            url.startswith("http://h:8077/v1/app-packs/social_media_activities_app/events_places?")
        )
        self.assertIn("layer=redistributable", url)
        self.assertIn("city=Cluj-Napoca", url)
        self.assertIn("kind=venue", url)

    def test_iter_app_pack_filters_unknown_or_internal_legal_metadata_fail_closed(self):
        page = {
            "pack_id": "roedu:social_media_activities_app:events_places:v1",
            "app": "social_media_activities_app",
            "layer": "redistributable",
            "schema_version": 1,
            "items": [
                {
                    "id": "event-ok",
                    "kind": "event",
                    "title": "Concert",
                    "tags": ["category:music"],
                    "facets": {"city": "Cluj-Napoca", "category": "music"},
                    "source": "fixture",
                    "provenance": {},
                    "license": "CC BY 4.0",
                    "access_type": "open_license",
                    "legal_basis": "fixture license",
                    "gdpr_relevant": False,
                    "redistributable": True,
                    "confidence": 1.0,
                },
                {
                    "id": "tdm-only",
                    "kind": "event",
                    "title": "Internal",
                    "tags": [],
                    "facets": {},
                    "source": "fixture",
                    "license": "TDM only",
                    "access_type": "tdm_exception",
                    "legal_basis": "internal text/data mining",
                    "gdpr_relevant": False,
                    "redistributable": False,
                    "confidence": 0.6,
                },
                {
                    "id": "missing-legal",
                    "kind": "venue",
                    "title": "Unknown",
                    "tags": [],
                    "facets": {},
                    "source": "fixture",
                    "license": "Unknown",
                    "access_type": "open_license",
                    "gdpr_relevant": False,
                    "redistributable": True,
                    "confidence": 1.0,
                },
                {
                    "id": "gdpr",
                    "kind": "venue",
                    "title": "Personal Data",
                    "tags": [],
                    "facets": {},
                    "source": "fixture",
                    "license": "CC BY 4.0",
                    "access_type": "open_license",
                    "legal_basis": "fixture license",
                    "gdpr_relevant": True,
                    "redistributable": True,
                    "confidence": 1.0,
                },
            ],
            "pagination": {"next_cursor": None},
            "withheld": 3,
            "errors": [],
        }
        fake = _canned_urlopen({"/v1/app-packs/social_media_activities_app/events_places": page})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            out = list(client.iter_app_pack("events_places"))
        self.assertEqual([item["id"] for item in out], ["event-ok"])

    def test_iter_app_pack_rejects_non_redistributable_layer(self):
        fake = _canned_urlopen({})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            out = list(client.iter_app_pack("events_places", layer="internal"))
        self.assertEqual(out, [])
        self.assertEqual(fake.calls, [])

    def test_read_app_pack_retains_one_consistent_complete_snapshot_identity(self):
        def item(item_id):
            return {
                "id": item_id,
                "kind": "event",
                "title": item_id,
                "access_type": "open_license",
                "legal_basis": "fixture license",
                "gdpr_relevant": False,
                "redistributable": True,
            }

        responses = [
            {
                "pack_id": "roedu:social_media_activities_app:events:v1",
                "layer": "redistributable",
                "snapshot_id": "sha256-snapshot",
                "release_id": "sha256-release",
                "snapshot_generated_at": "2026-07-12T08:00:00Z",
                "snapshot_mode": "full",
                "snapshot_complete": True,
                "items": [item("one")],
                "pagination": {"next_cursor": "snapshot-bound-c1"},
            },
            {
                "pack_id": "roedu:social_media_activities_app:events:v1",
                "layer": "redistributable",
                "snapshot_id": "sha256-snapshot",
                "release_id": "sha256-release",
                "snapshot_generated_at": "2026-07-12T08:00:00Z",
                "snapshot_mode": "full",
                "snapshot_complete": True,
                "items": [item("two")],
                "pagination": {"next_cursor": None},
            },
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack("events")
        self.assertEqual([item["id"] for item in result.items], ["one", "two"])
        self.assertEqual(result.snapshot_id, "sha256-snapshot")
        self.assertTrue(result.snapshot_complete)

    def test_read_app_pack_rejects_snapshot_identity_drift_between_pages(self):
        base = {
            "pack_id": "roedu:social_media_activities_app:events:v1",
            "layer": "redistributable",
            "release_id": "sha256-release",
            "snapshot_generated_at": "2026-07-12T08:00:00Z",
            "snapshot_mode": "full",
            "snapshot_complete": True,
            "items": [],
        }
        responses = [
            {**base, "snapshot_id": "snapshot-a", "pagination": {"next_cursor": "c1"}},
            {**base, "snapshot_id": "snapshot-b", "pagination": {"next_cursor": None}},
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen), self.assertRaises(rc.RoeduContractError):
            RoeduClient("http://h:8077", api_key="k").read_app_pack("events")

    def test_read_app_pack_never_declares_complete_when_a_page_omits_identity(self):
        base = {
            "pack_id": "roedu:social_media_activities_app:events:v1",
            "layer": "redistributable",
            "snapshot_id": "snapshot-a",
            "release_id": "release-a",
            "snapshot_generated_at": "2026-07-12T08:00:00Z",
            "snapshot_mode": "full",
            "snapshot_complete": True,
            "items": [],
        }
        responses = [
            {**base, "pagination": {"next_cursor": "c1"}},
            {**base, "release_id": "", "pagination": {"next_cursor": None}},
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack("events")
        self.assertFalse(result.snapshot_complete)

    def test_read_app_pack_never_reconciles_after_client_withholds_invalid_item(self):
        page = {
            "pack_id": "roedu:social_media_activities_app:events:v1",
            "layer": "redistributable",
            "snapshot_id": "snapshot-a",
            "release_id": "release-a",
            "snapshot_generated_at": "2026-07-12T08:00:00Z",
            "snapshot_mode": "full",
            "snapshot_complete": True,
            "items": [{"id": "missing-policy", "kind": "event"}],
            "pagination": {"next_cursor": None},
        }
        fake = _canned_urlopen({"/v1/app-packs/": page})
        with self._patched(fake):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack("events")
        self.assertEqual(result.items, ())
        self.assertFalse(result.snapshot_complete)

    # --- helper: patch the client's single HTTP boundary ---
    def _patched(self, fake_urlopen):
        import contextlib
        import unittest.mock as mock

        @contextlib.contextmanager
        def _cm():
            with mock.patch.object(rc.urllib.request, "urlopen", fake_urlopen):
                yield

        return _cm()
