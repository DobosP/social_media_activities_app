"""Unit tests for the vendored RoeduClient (apps/ingestion/sources/roedu_client.py).

No network, no DB: the only I/O boundary is ``urllib.request.urlopen``, which we
monkeypatch to return canned JSON pages. ``SimpleTestCase`` because nothing here
touches the ORM (and the client itself is pure stdlib).
"""

from __future__ import annotations

import io
import json
from copy import deepcopy

from django.test import SimpleTestCase

from apps.ingestion.sources import roedu_client as rc
from apps.ingestion.sources.roedu_client import (
    SOCIAL_APP_PACK_ID,
    RoeduClient,
    is_canonical_social_app_pack_item,
)
from apps.ingestion.tests.roedu_fixtures import (
    event_item,
    pack_page,
    tombstone_item,
    venue_item,
)


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


class CanonicalSocialItemTests(SimpleTestCase):
    def test_current_venue_event_and_tombstone_are_valid(self):
        self.assertTrue(is_canonical_social_app_pack_item(venue_item()))
        self.assertTrue(is_canonical_social_app_pack_item(event_item()))
        self.assertTrue(is_canonical_social_app_pack_item(tombstone_item()))

    def test_policy_shape_and_consumer_bound_mutations_fail_closed(self):
        cases = []

        stale_policy = event_item()
        stale_policy["policy_attestation"]["ruleset_version"] = 5
        cases.append(stale_policy)

        lane_mismatch = event_item()
        lane_mismatch["acquisition_lane"] = "web_http"
        cases.append(lane_mismatch)

        prose = event_item(description="copyrighted prose")
        cases.append(prose)

        bad_id = event_item(id="x" * 129)
        cases.append(bad_id)

        bad_location = venue_item(location={"lat": float("inf"), "lon": 23.5})
        cases.append(bad_location)

        bad_address = venue_item()
        bad_address["address"]["street"] = "x" * 256
        cases.append(bad_address)

        huge_number = event_item(price_min=10**400)
        cases.append(huge_number)

        unhashable_obligation = event_item()
        unhashable_obligation["policy_attestation"]["obligations"] = [{}]
        cases.append(unhashable_obligation)

        whitespace_url = event_item(ticket_url="https://tickets.example.test/a b")
        cases.append(whitespace_url)

        missing_venue = event_item()
        missing_venue.pop("venue_id")
        missing_venue.pop("place_id")
        missing_venue["facets"].update({"venue_id": None, "place_id": None})
        cases.append(missing_venue)

        for field, value in (
            ("access_type", {}),
            ("acquisition_lane", []),
            ("availability", {}),
        ):
            malformed_json_type = event_item()
            malformed_json_type[field] = value
            cases.append(malformed_json_type)

        malformed_status = event_item(status={})
        malformed_status["lifecycle_status"] = {}
        malformed_status["facets"].update({"status": {}, "lifecycle_status": {}})
        malformed_status["tags"] = ["event:concert", "lifecycle:{}"]
        cases.append(malformed_status)

        stale_tombstone = tombstone_item(timezone="Europe/Bucharest")
        cases.append(stale_tombstone)

        for item in cases:
            with self.subTest(item=deepcopy(item)):
                self.assertFalse(is_canonical_social_app_pack_item(item))


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
        page = pack_page()
        expected_path = f"/v1/app-packs/social_media_activities_app/{SOCIAL_APP_PACK_ID}"
        fake = _canned_urlopen({expected_path: page})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            client.app_pack_page(SOCIAL_APP_PACK_ID, city="Cluj-Napoca", kind="venue")
        url = fake.calls[0]["url"]
        self.assertTrue(url.startswith(f"http://h:8077{expected_path}?"))
        self.assertIn("layer=redistributable", url)
        self.assertIn("city=Cluj-Napoca", url)
        self.assertIn("kind=venue", url)

    def test_iter_app_pack_withholds_invalid_policy_item_fail_closed(self):
        invalid = event_item(id="invalid")
        invalid["policy_attestation"]["ruleset_version"] = 5
        page = pack_page([venue_item(), event_item(), invalid])
        fake = _canned_urlopen({"/v1/app-packs/": page})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            out = list(client.iter_app_pack(SOCIAL_APP_PACK_ID))
        self.assertEqual([item["id"] for item in out], ["venue-1", "event-1"])

    def test_iter_app_pack_rejects_non_redistributable_layer(self):
        fake = _canned_urlopen({})
        with self._patched(fake):
            client = RoeduClient("http://h:8077", api_key="k")
            out = list(client.iter_app_pack(SOCIAL_APP_PACK_ID, layer="internal"))
        self.assertEqual(out, [])
        self.assertEqual(fake.calls, [])

    def test_read_app_pack_retains_one_consistent_complete_snapshot_identity(self):
        responses = [
            pack_page([venue_item()], cursor="snapshot-bound-c1"),
            pack_page([event_item()]),
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)
        self.assertEqual([item["id"] for item in result.items], ["venue-1", "event-1"])
        self.assertEqual(result.snapshot_id, "sha256-" + "7" * 64)
        self.assertTrue(result.snapshot_complete)

    def test_event_may_precede_its_venue_on_a_later_page(self):
        responses = [
            pack_page([event_item()], cursor="snapshot-bound-c1"),
            pack_page([venue_item()]),
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)
        self.assertEqual({item["id"] for item in result.items}, {"event-1", "venue-1"})
        self.assertTrue(result.snapshot_complete)

    def test_duplicate_or_dangling_items_make_read_incomplete(self):
        cases = (
            ([venue_item(), venue_item()], ["venue-1"]),
            ([event_item()], []),
        )
        for items, expected_ids in cases:
            with self.subTest(expected_ids=expected_ids):
                fake = _canned_urlopen({"/v1/app-packs/": pack_page(items)})
                with self._patched(fake):
                    result = RoeduClient("http://h:8077", api_key="k").read_app_pack(
                        SOCIAL_APP_PACK_ID
                    )
                self.assertEqual([item["id"] for item in result.items], expected_ids)
                self.assertFalse(result.snapshot_complete)

    def test_read_app_pack_rejects_snapshot_identity_drift_between_pages(self):
        responses = [
            pack_page(
                cursor="c1",
                snapshot_id="sha256-" + "1" * 64,
                release_id="sha256-" + "1" * 64,
            ),
            pack_page(
                snapshot_id="sha256-" + "2" * 64,
                release_id="sha256-" + "2" * 64,
            ),
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen), self.assertRaises(rc.RoeduContractError):
            RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)

    def test_read_app_pack_rejects_page_that_omits_identity(self):
        responses = [
            pack_page(cursor="c1"),
            pack_page(release_id=""),
        ]

        def fake_urlopen(req, timeout=None):
            return _FakeResponse(json.dumps(responses.pop(0)).encode("utf-8"))

        with self._patched(fake_urlopen), self.assertRaises(rc.RoeduContractError):
            RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)

    def test_read_app_pack_rejects_non_promoted_or_mismatched_release_identity(self):
        for snapshot_id, release_id in (
            ("snapshot", "snapshot"),
            ("sha256-" + "1" * 64, "sha256-" + "2" * 64),
        ):
            with self.subTest(snapshot_id=snapshot_id, release_id=release_id):
                page = pack_page(snapshot_id=snapshot_id, release_id=release_id)
                fake = _canned_urlopen({"/v1/app-packs/": page})
                with self._patched(fake), self.assertRaises(rc.RoeduContractError):
                    RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)

    def test_read_app_pack_rejects_unknown_page_envelope_field(self):
        page = pack_page(unreviewed=True)
        fake = _canned_urlopen({"/v1/app-packs/": page})
        with self._patched(fake), self.assertRaises(rc.RoeduContractError):
            RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)

    def test_read_app_pack_never_completes_after_filter_mismatch(self):
        wrong_city = venue_item()
        wrong_city["facets"]["city"] = "București"
        wrong_city["address"]["city"] = "București"
        page = pack_page([wrong_city])
        fake = _canned_urlopen({"/v1/app-packs/": page})
        with self._patched(fake):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack(
                SOCIAL_APP_PACK_ID,
                city="Cluj-Napoca",
            )
        self.assertEqual(result.items, ())
        self.assertFalse(result.snapshot_complete)

        missing_city = venue_item()
        missing_city["facets"]["city"] = None
        missing_city["address"]["city"] = ""
        fake = _canned_urlopen({"/v1/app-packs/": pack_page([missing_city])})
        with self._patched(fake):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack(
                SOCIAL_APP_PACK_ID,
                city="Cluj-Napoca",
            )
        self.assertEqual(result.items, ())
        self.assertFalse(result.snapshot_complete)

    def test_read_app_pack_rejects_noncanonical_page_datetime_and_bool_schema(self):
        for update in (
            {"snapshot_generated_at": "2026-07-12 08:00:00+00:00"},
            {"schema_version": True},
            {"snapshot_mode": {}},
        ):
            with self.subTest(update=update):
                fake = _canned_urlopen({"/v1/app-packs/": pack_page(**update)})
                with self._patched(fake), self.assertRaises(rc.RoeduContractError):
                    RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)

    def test_read_app_pack_rejects_missing_items_before_absence_can_be_inferred(self):
        page = pack_page()
        page.pop("items")
        fake = _canned_urlopen({"/v1/app-packs/": page})

        with self._patched(fake), self.assertRaises(rc.RoeduContractError):
            RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)

    def test_read_app_pack_rejects_falsy_non_null_next_cursor(self):
        for cursor in ("", False, 0, [], {}):
            with self.subTest(cursor=cursor):
                page = pack_page()
                page["pagination"]["next_cursor"] = cursor
                fake = _canned_urlopen({"/v1/app-packs/": page})
                with self._patched(fake), self.assertRaises(rc.RoeduContractError):
                    RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)

    def test_read_app_pack_never_reconciles_after_client_withholds_invalid_item(self):
        invalid = event_item()
        invalid["acquisition_lane"] = "invented"
        page = pack_page([venue_item(), invalid])
        fake = _canned_urlopen({"/v1/app-packs/": page})
        with self._patched(fake):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)
        self.assertEqual([item["id"] for item in result.items], ["venue-1"])
        self.assertFalse(result.snapshot_complete)

    def test_read_app_pack_preserves_partial_withheld_state_without_dropping_valid_items(self):
        page = pack_page(
            [venue_item(), event_item()],
            mode="partial",
            complete=False,
            withheld=1,
            errors=["producer declared partial snapshot"],
        )
        fake = _canned_urlopen({"/v1/app-packs/": page})
        with self._patched(fake):
            result = RoeduClient("http://h:8077", api_key="k").read_app_pack(SOCIAL_APP_PACK_ID)
        self.assertEqual(len(result.items), 2)
        self.assertFalse(result.snapshot_complete)

    def test_short_pack_alias_is_rejected_before_network(self):
        fake = _canned_urlopen({})
        with self._patched(fake), self.assertRaises(rc.RoeduContractError):
            RoeduClient("http://h:8077", api_key="k").read_app_pack("events_places")
        self.assertEqual(fake.calls, [])

    # --- helper: patch the client's single HTTP boundary ---
    def _patched(self, fake_urlopen):
        import contextlib
        import unittest.mock as mock

        @contextlib.contextmanager
        def _cm():
            with mock.patch.object(rc.urllib.request, "urlopen", fake_urlopen):
                yield

        return _cm()
