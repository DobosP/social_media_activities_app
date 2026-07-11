from datetime import UTC, datetime
from unittest import mock

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command

from apps.events.models import Event
from apps.events.services import StaleRoeduSnapshot, reconcile_roedu_snapshot, upcoming_events
from apps.ingestion.sources.roedu_client import AppPackRead
from apps.places.models import Place

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

    def iter_app_pack(self, pack, *, max_records=None, **filters):
        return iter(
            [
                {
                    "id": "venue-1",
                    "kind": "venue",
                    "title": "Teatrul National",
                    "tags": ["venue:theatre"],
                    "facets": {
                        "city": "Cluj-Napoca",
                        "county": "Cluj",
                        "category": "theatre",
                        "venue_category": "theatre",
                    },
                    "source": "synthetic-fixture",
                    "provenance": {},
                    "license": "CC BY 4.0",
                    "access_type": "open_license",
                    "legal_basis": "fixture license",
                    "gdpr_relevant": False,
                    "redistributable": True,
                    "confidence": 1.0,
                    "location": {"lat": 46.7712, "lon": 23.5949},
                },
                {
                    "id": "event-1",
                    "kind": "event",
                    "title": "Concert",
                    "tags": ["event:music"],
                    "facets": {"city": "Cluj-Napoca", "county": "Cluj", "category": "music"},
                    "source": "synthetic-fixture",
                    "provenance": {},
                    "license": "CC BY 4.0",
                    "access_type": "open_license",
                    "legal_basis": "fixture license",
                    "gdpr_relevant": False,
                    "redistributable": True,
                    "confidence": 1.0,
                    "start_datetime": datetime(2030, 1, 1, 18, 0, tzinfo=UTC).isoformat(),
                    "end_datetime": "",
                    "place_id": "venue-1",
                    "description": "This prose must not be stored.",
                },
            ]
        )

    def read_app_pack(self, pack, *, max_records=None, **filters):
        items = tuple(self.iter_app_pack(pack, max_records=max_records, **filters))
        if max_records:
            items = items[:max_records]
        return AppPackRead(
            items=items,
            pack_id="roedu:social_media_activities_app:events_places:v1",
            snapshot_id="sha256-snapshot-1",
            release_id="sha256-release-1",
            snapshot_generated_at="2026-07-12T08:00:00Z",
            snapshot_mode="full",
            snapshot_complete=not bool(max_records),
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


def test_sync_roedu_events_app_pack_is_facts_only_and_no_provenance_url():
    with (
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.RoeduClient", FakeRoeduClient
        ),
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.find_duplicate",
            return_value=None,
        ),
    ):
        call_command("sync_roedu_events", "--city", "Cluj-Napoca", "--app-pack", "events_places")

    event = Event.objects.get(external_id="roedu:event-1")
    assert event.title == "Concert"
    assert event.description == ""
    assert event.url == ""
    assert event.attribution == "synthetic-fixture"
    assert event.license_name == "CC BY 4.0"
    assert event.provenance_url == ""


def test_app_pack_retains_category_lifecycle_and_stable_venue_identity():
    place = Place.objects.create(
        name="Teatrul National",
        location=Point(23.5949, 46.7712, srid=4326),
        source=Place.Source.ROEDU,
        external_id="venue-1",
    )

    class CancelledClient(FakeRoeduClient):
        def iter_app_pack(self, pack, *, max_records=None, **filters):
            rows = list(super().iter_app_pack(pack, max_records=max_records, **filters))
            event = rows[-1]
            event["category"] = "concert"
            event.pop("place_id")
            event["facets"].update({"place_id": "venue-1", "lifecycle_status": "cancelled"})
            event["updated_at"] = "2026-07-12T07:00:00Z"
            return iter(rows)

    with (
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.RoeduClient", CancelledClient
        ),
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.find_duplicate",
            side_effect=AssertionError("stable venue id should resolve before geo fallback"),
        ),
    ):
        call_command("sync_roedu_events", "--app-pack", "events_places")

    event = Event.objects.get(external_id="roedu:event-1")
    assert event.place == place
    assert event.source_venue_id == "venue-1"
    assert event.source_category == "concert"
    assert event.activity_type.slug == "concert"
    assert event.lifecycle_status == Event.LifecycleStatus.CANCELLED
    assert not event.is_tombstone
    assert not upcoming_events().filter(pk=event.pk).exists()


def test_complete_snapshot_retracts_absent_event_and_bodyless_tombstone():
    pack_id = "roedu:social_media_activities_app:events_places:v1"
    old = Event.objects.create(
        title="Old listing",
        starts_at=datetime(2030, 2, 1, 18, 0, tzinfo=UTC),
        source=Event.Source.SCRAPER,
        external_id="roedu:old",
        source_pack_id=pack_id,
        source_city="Cluj-Napoca",
    )
    deleted = Event.objects.create(
        title="Deleted listing",
        starts_at=datetime(2030, 2, 2, 18, 0, tzinfo=UTC),
        source=Event.Source.SCRAPER,
        external_id="roedu:deleted",
        source_pack_id=pack_id,
        source_city="Cluj-Napoca",
    )

    class TombstoneClient(FakeRoeduClient):
        def iter_app_pack(self, pack, *, max_records=None, **filters):
            rows = list(super().iter_app_pack(pack, max_records=max_records, **filters))
            rows.append(
                {
                    "id": "deleted",
                    "kind": "event_tombstone",
                    "title": "",
                    "tags": [],
                    "facets": {"city": "Cluj-Napoca"},
                    "source": "synthetic-fixture",
                    "license": "CC BY 4.0",
                    "access_type": "open_license",
                    "legal_basis": "fixture license",
                    "gdpr_relevant": False,
                    "redistributable": True,
                    "confidence": 1.0,
                    "tombstone": True,
                    "updated_at": "2026-07-12T07:30:00Z",
                }
            )
            return iter(rows)

    with mock.patch(
        "apps.events.management.commands.sync_roedu_events.RoeduClient", TombstoneClient
    ):
        call_command("sync_roedu_events", "--app-pack", "events_places")

    old.refresh_from_db()
    deleted.refresh_from_db()
    assert old.is_tombstone and old.lifecycle_status == Event.LifecycleStatus.REMOVED
    assert deleted.is_tombstone and deleted.lifecycle_status == Event.LifecycleStatus.REMOVED
    assert not Event.objects.filter(pk__in=[old.pk, deleted.pk], is_tombstone=False).exists()


def test_limited_snapshot_never_retracts_absent_rows():
    pack_id = "roedu:social_media_activities_app:events_places:v1"
    old = Event.objects.create(
        title="Keep on partial read",
        starts_at=datetime(2030, 2, 1, 18, 0, tzinfo=UTC),
        source=Event.Source.SCRAPER,
        external_id="roedu:keep",
        source_pack_id=pack_id,
        source_city="Cluj-Napoca",
    )
    with mock.patch(
        "apps.events.management.commands.sync_roedu_events.RoeduClient", FakeRoeduClient
    ):
        call_command("sync_roedu_events", "--app-pack", "events_places", "--limit", "1")
    old.refresh_from_db()
    assert not old.is_tombstone


def test_older_snapshot_replay_fails_closed_without_operator_override():
    kwargs = {
        "pack_id": "roedu:social_media_activities_app:events_places:v1",
        "city": "Cluj-Napoca",
        "release_id": "release-new",
        "seen_external_ids": set(),
    }
    reconcile_roedu_snapshot(
        **kwargs,
        snapshot_id="snapshot-new",
        snapshot_generated_at=datetime(2026, 7, 12, 9, 0, tzinfo=UTC),
    )
    with pytest.raises(StaleRoeduSnapshot):
        reconcile_roedu_snapshot(
            **{**kwargs, "release_id": "release-old"},
            snapshot_id="snapshot-old",
            snapshot_generated_at=datetime(2026, 7, 12, 8, 0, tzinfo=UTC),
        )


def test_low_confidence_event_is_retained_for_review_but_not_public():
    class LowConfidenceClient(FakeRoeduClient):
        def iter_app_pack(self, pack, *, max_records=None, **filters):
            rows = list(super().iter_app_pack(pack, max_records=max_records, **filters))
            rows[-1]["confidence"] = 0.6
            return iter(rows)

    with mock.patch(
        "apps.events.management.commands.sync_roedu_events.RoeduClient", LowConfidenceClient
    ):
        call_command("sync_roedu_events", "--app-pack", "events_places")

    event = Event.objects.get(external_id="roedu:event-1")
    assert event.is_import_held
    assert event.source_confidence == 0.6
    assert not upcoming_events().filter(pk=event.pk).exists()


def test_legacy_delta_applies_bodyless_tombstone_without_inferring_absence():
    deleted = Event.objects.create(
        title="Delete from delta",
        starts_at=datetime(2030, 2, 2, 18, 0, tzinfo=UTC),
        source=Event.Source.SCRAPER,
        external_id="roedu:delta-deleted",
    )
    untouched = Event.objects.create(
        title="Not mentioned in delta",
        starts_at=datetime(2030, 2, 3, 18, 0, tzinfo=UTC),
        source=Event.Source.SCRAPER,
        external_id="roedu:not-mentioned",
    )

    class DeltaClient(FakeRoeduClient):
        event_filters = None

        def iter(self, product, *, max_records=None, **filters):
            if product == "venues":
                return iter([])
            type(self).event_filters = filters
            return iter(
                [
                    {
                        "id": "delta-deleted",
                        "deleted": True,
                        "updated_at": "2026-07-12T10:00:00Z",
                    }
                ]
            )

    with mock.patch("apps.events.management.commands.sync_roedu_events.RoeduClient", DeltaClient):
        call_command(
            "sync_roedu_events",
            "--updated-since",
            "change-token-42",
            "--city",
            "Cluj-Napoca",
        )

    deleted.refresh_from_db()
    untouched.refresh_from_db()
    assert deleted.is_tombstone
    assert not untouched.is_tombstone
    assert DeltaClient.event_filters == {
        "city": "Cluj-Napoca",
        "updated_since": "change-token-42",
    }
