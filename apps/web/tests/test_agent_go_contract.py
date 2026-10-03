"""Real producer -> native Go contract test; the serving binary contains no Python.

Build the test-only executable at services/agentapi/agentapi-test before this suite.
The ordinary Python-only suite skips this file when that executable is absent;
the dedicated Go contract CI job builds it and runs this test explicitly.
"""

import json
import socket
import subprocess
import time
from contextlib import contextmanager
from datetime import timedelta
from decimal import Decimal
from pathlib import Path
from urllib.error import HTTPError, URLError
from urllib.parse import urlencode
from urllib.request import urlopen

import pytest
from django.contrib.gis.geos import Point
from django.utils import timezone
from rest_framework.request import Request
from rest_framework.test import APIRequestFactory

from apps.accounts.models import Cohort, User
from apps.events.models import Event
from apps.events.views import EventViewSet
from apps.places.models import Place
from apps.social.models import Activity, UserPlaceProposal
from apps.taxonomy.models import ActivityType
from apps.web.agent_snapshot import export_snapshot

TEST_BINARY = Path(__file__).resolve().parents[3] / "services/agentapi/agentapi-test"
pytestmark = [
    pytest.mark.django_db,
    pytest.mark.skipif(not TEST_BINARY.is_file(), reason="Go contract test binary not built"),
]


def _get(base_url, path):
    # Every caller supplies the loopback base chosen by _native_server, never a producer URL.
    with urlopen(base_url + path, timeout=3) as response:  # noqa: S310
        return response.status, json.load(response), response.headers


@contextmanager
def _native_server(snapshot_dir):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        port = sock.getsockname()[1]
    base_url = f"http://127.0.0.1:{port}"
    # Pass only test configuration, rather than inheriting secrets from the Python process.
    process = subprocess.Popen(
        [str(TEST_BINARY)],
        env={
            "AGENT_API_ADDR": f"127.0.0.1:{port}",
            "AGENT_SNAPSHOT_DIR": str(snapshot_dir),
            "AGENT_API_RELOAD_SECONDS": "30",
            "AGENT_API_RATE_PER_MIN": "300",
            "AGENT_API_RATE_BURST": "60",
        },
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
    )
    try:
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            if process.poll() is not None:
                pytest.fail("Native Go contract server exited before readiness")
            try:
                status, body, _ = _get(base_url, "/agent/v1/healthz")
                if status == 200 and body["status"] == "ok":
                    break
            except (URLError, HTTPError, OSError):
                pass
            time.sleep(0.025)
        else:
            pytest.fail("Native Go contract server did not load the producer snapshot")
        yield base_url
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=3)


def _read_snapshot(directory, dataset):
    return json.loads((directory / f"{dataset}.json").read_text(encoding="utf-8"))


def test_native_go_queries_match_exported_public_records(tmp_path):
    at = ActivityType.objects.get(slug="basketball")
    owner = User.objects.create_user(username="go-contract-owner", password="test-only")
    library = Place.objects.create(
        name="Biblioteca Științei",
        location=Point(23.6236, 46.7712, srid=4326),
        source=Place.Source.OSM,
        address_city="Cluj-Napoca",
        license_name="ODbL-1.0",
        attribution="OpenStreetMap contributors",
    )
    park = Place.objects.create(
        name="Dobo Park",
        location=Point(23.6236, 46.7810881, srid=4326),
        source=Place.Source.OSM,
        address_city="Cluj-Napoca",
    )
    pending = Place.objects.create(
        name="Private pending venue",
        location=Point(23.6, 46.7, srid=4326),
        source=Place.Source.USER,
    )
    UserPlaceProposal.objects.create(
        place=pending, proposer=owner, status=UserPlaceProposal.Status.PENDING
    )
    anchor = timezone.now().replace(microsecond=0) + timedelta(days=10)
    first = Event.objects.create(
        title="Public first event",
        description="A reviewed descriptive keyword",
        starts_at=anchor,
        place=library,
        activity_type=at,
        source_price_min=Decimal("20.00"),
        source_price_max=Decimal("50.00"),
        source_currency="RON",
        source_availability="limited",
        source_timezone="Europe/Bucharest",
        attribution="Public source",
        license_name="CC-BY-4.0",
        provenance_url="https://example.org/facts/first",
    )
    second = Event.objects.create(
        title="Later park event", starts_at=anchor + timedelta(days=2), place=park
    )
    standalone = Event.objects.create(
        title="No venue event", starts_at=anchor + timedelta(days=1), place=None
    )
    for fields in [
        {"place": pending},
        {"place": library, "lifecycle_status": Event.LifecycleStatus.CANCELLED},
        {"place": library, "is_tombstone": True},
        {"place": library, "is_import_held": True},
        {"place": library, "starts_at": timezone.now() - timedelta(days=1)},
    ]:
        Event.objects.create(title="Excluded source event", **{"starts_at": anchor, **fields})

    public_activity = Activity.objects.create(
        owner=owner,
        title="Public adult card",
        cohort=Cohort.ADULT,
        is_publicly_listed=True,
        starts_at=anchor,
        place=library,
        activity_type=at,
        status=Activity.Status.OPEN,
    )
    for cohort, listed in [(Cohort.CHILD, True), (Cohort.TEEN, True), (Cohort.ADULT, False)]:
        Activity.objects.create(
            owner=owner,
            title="Excluded activity card",
            cohort=cohort,
            is_publicly_listed=listed,
            starts_at=anchor,
            place=library,
            activity_type=at,
            status=Activity.Status.OPEN,
        )

    counts = export_snapshot(str(tmp_path))
    assert counts["truncated"] is False
    exported = _read_snapshot(tmp_path, "events")
    records = {record["id"]: record for record in exported["records"]}
    assert set(records) == {first.pk, second.pk, standalone.pk}
    cases = [
        {},
        {"place": library.pk},
        {"place": park.pk, "city": "cluj-napoca"},
        {"activity": at.slug},
        {"q": "descriptive"},
        {"q": "Științei"},
        {"from": anchor.isoformat(), "to": (anchor + timedelta(days=2)).isoformat()},
        {
            "near_lat": "46.7810881",
            "near_lon": "23.6236",
            "radius_m": "2000",
        },
    ]
    with _native_server(tmp_path) as base_url:
        factory = APIRequestFactory()
        for params in cases:
            reference = EventViewSet()
            reference.request = Request(factory.get("/api/v1/events/", data=params))
            expected_ids = list(reference.get_queryset().values_list("id", flat=True))
            status, response, headers = _get(base_url, "/agent/v1/events?" + urlencode(params))
            assert status == 200
            assert response["generated_at"] == exported["generated_at"]
            assert response["total"] == len(expected_ids)
            assert response["data"] == [records[event_id] for event_id in expected_ids]
            assert "Set-Cookie" not in headers
            assert headers["Cache-Control"] == "public, max-age=300"

        status, response, _ = _get(base_url, f"/agent/v1/events/{first.pk}")
        assert status == 200
        assert response["data"] == records[first.pk]
        assert response["data"]["source_price_min"] == "20.00"
        assert response["data"]["license_name"] == "CC-BY-4.0"
        assert response["data"]["provenance_url"] == "https://example.org/facts/first"

        _, activities, _ = _get(base_url, "/agent/v1/activities")
        assert activities["data"] == _read_snapshot(tmp_path, "activities")["records"]
        assert [record["id"] for record in activities["data"]] == [public_activity.pk]
        _, places, _ = _get(base_url, "/agent/v1/places")
        assert {record["id"] for record in places["data"]} == {library.pk, park.pk}
        _, health, _ = _get(base_url, "/agent/v1/healthz")
        assert health["snapshot_age_seconds"] < 30

        with pytest.raises(HTTPError) as unavailable:
            _get(base_url, "/api/v1/events/")
        assert unavailable.value.code == 404  # No falsely equivalent Django API alias.
