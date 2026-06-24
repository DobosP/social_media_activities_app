from datetime import UTC, datetime
from unittest import mock

import pytest
from django.core.management import call_command

from apps.events.models import Event

pytestmark = pytest.mark.django_db


class FakeRoeduClient:
    def __init__(self, *args, **kwargs):
        pass

    def iter(self, product, *, max_records=None, **filters):
        if product == "venues":
            return iter(
                [
                    {
                        "id": "venue-1",
                        "name": "Teatrul National",
                        "lat": 46.7712,
                        "lon": 23.5949,
                    }
                ]
            )
        return iter(
            [
                {
                    "id": "event-1",
                    "title": "Concert",
                    "start_datetime": datetime(2030, 1, 1, 18, 0, tzinfo=UTC).isoformat(),
                    "end_datetime": "",
                    "venue_id": "venue-1",
                    "confidence": 1.0,
                    "source_url": "https://events.example/concert",
                    "attribution": "RO-EDU",
                    "license_name": "CC BY 4.0",
                    "provenance_url": "https://data.example/events/event-1",
                }
            ]
        )


def test_sync_roedu_events_maps_optional_source_credit():
    with (
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.RoeduClient", FakeRoeduClient
        ),
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.find_duplicate",
            return_value=None,
        ),
    ):
        call_command("sync_roedu_events", "--city", "Cluj-Napoca")

    event = Event.objects.get(external_id="roedu:event-1")
    assert event.attribution == "RO-EDU"
    assert event.license_name == "CC BY 4.0"
    assert event.provenance_url == "https://data.example/events/event-1"
