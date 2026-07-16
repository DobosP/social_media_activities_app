from datetime import UTC, datetime
from decimal import Decimal
from unittest import mock

import pytest
from django.contrib.gis.geos import Point
from django.core.management import call_command
from django.core.management.base import CommandError

from apps.events.models import Event
from apps.events.services import StaleRoeduSnapshot, reconcile_roedu_snapshot, upcoming_events
from apps.ingestion.sources.roedu_client import SOCIAL_APP_PACK_ID, AppPackRead
from apps.ingestion.tests.roedu_fixtures import event_item, tombstone_item, venue_item
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
        return iter([venue_item(), event_item()])

    def read_app_pack(self, pack, *, max_records=None, **filters):
        items = tuple(self.iter_app_pack(pack, max_records=max_records, **filters))
        if max_records:
            items = items[:max_records]
        return AppPackRead(
            items=items,
            pack_id=SOCIAL_APP_PACK_ID,
            snapshot_id="sha256-" + "7" * 64,
            release_id="sha256-" + "7" * 64,
            snapshot_generated_at="2026-07-12T08:00:00Z",
            snapshot_mode="full",
            snapshot_complete=not bool(max_records),
        )


def _create_roedu_place() -> Place:
    return Place.objects.create(
        name="Teatrul National",
        location=Point(23.5949, 46.7712, srid=4326),
        source=Place.Source.ROEDU,
        external_id="venue-1",
    )


def test_short_app_pack_alias_is_rejected_before_client_or_writes():
    with (
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.RoeduClient",
            side_effect=AssertionError("client must not be constructed"),
        ),
        pytest.raises(CommandError, match="canonical"),
    ):
        call_command("sync_roedu_events", "--app-pack", "events_places")
    assert not Event.objects.exists()


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
    _create_roedu_place()
    with (
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.RoeduClient", FakeRoeduClient
        ),
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.find_duplicate",
            return_value=None,
        ),
    ):
        call_command(
            "sync_roedu_events",
            "--city",
            "Cluj-Napoca",
            "--app-pack",
            SOCIAL_APP_PACK_ID,
        )

    event = Event.objects.get(external_id="roedu:event-1")
    assert event.title == "Concert"
    assert event.description == ""
    assert event.url == "https://tickets.example.test/concert"
    assert event.attribution == "opera_cluj_events"
    assert event.license_name == "RO-LAW-8-1996-ART-9"
    assert event.provenance_url == ""
    assert event.source_timezone == "Europe/Bucharest"
    assert event.source_recurrence == "FREQ=WEEKLY"
    assert event.source_price_min == Decimal("20.00")
    assert event.source_price_max == Decimal("50.00")
    assert event.source_currency == "RON"
    assert event.source_is_free is False
    assert event.source_availability == "available"


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
            event.update(
                {
                    "status": "cancelled",
                    "lifecycle_status": "cancelled",
                    "cancelled": True,
                    "tags": ["event:concert", "lifecycle:cancelled"],
                    "updated_at": "2026-07-12T09:00:00+00:00",
                }
            )
            event["facets"].update({"status": "cancelled", "lifecycle_status": "cancelled"})
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
        call_command("sync_roedu_events", "--app-pack", SOCIAL_APP_PACK_ID)

    event = Event.objects.get(external_id="roedu:event-1")
    assert event.place == place
    assert event.source_venue_id == "venue-1"
    assert event.source_category == "concert"
    assert event.activity_type.slug == "concert"
    assert event.lifecycle_status == Event.LifecycleStatus.CANCELLED
    assert not event.is_tombstone
    assert not upcoming_events().filter(pk=event.pk).exists()


def test_complete_snapshot_retracts_absent_event_and_bodyless_tombstone():
    _create_roedu_place()
    pack_id = SOCIAL_APP_PACK_ID
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
            rows.append(tombstone_item(updated_at="2026-07-12T09:30:00+00:00"))
            return iter(rows)

    with mock.patch(
        "apps.events.management.commands.sync_roedu_events.RoeduClient", TombstoneClient
    ):
        call_command("sync_roedu_events", "--app-pack", SOCIAL_APP_PACK_ID)

    old.refresh_from_db()
    deleted.refresh_from_db()
    assert old.is_tombstone and old.lifecycle_status == Event.LifecycleStatus.REMOVED
    assert deleted.is_tombstone and deleted.lifecycle_status == Event.LifecycleStatus.REMOVED
    assert not Event.objects.filter(pk__in=[old.pk, deleted.pk], is_tombstone=False).exists()


def test_limited_snapshot_never_retracts_absent_rows():
    pack_id = SOCIAL_APP_PACK_ID
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
        call_command(
            "sync_roedu_events",
            "--app-pack",
            SOCIAL_APP_PACK_ID,
            "--limit",
            "1",
        )
    old.refresh_from_db()
    assert not old.is_tombstone


def test_older_snapshot_replay_fails_closed_without_operator_override():
    kwargs = {
        "pack_id": SOCIAL_APP_PACK_ID,
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
    _create_roedu_place()

    class LowConfidenceClient(FakeRoeduClient):
        def iter_app_pack(self, pack, *, max_records=None, **filters):
            rows = list(super().iter_app_pack(pack, max_records=max_records, **filters))
            rows[-1]["confidence"] = 0.6
            return iter(rows)

    with mock.patch(
        "apps.events.management.commands.sync_roedu_events.RoeduClient", LowConfidenceClient
    ):
        call_command("sync_roedu_events", "--app-pack", SOCIAL_APP_PACK_ID)

    event = Event.objects.get(external_id="roedu:event-1")
    assert event.is_import_held
    assert event.source_confidence == 0.6
    assert not upcoming_events().filter(pk=event.pk).exists()


def test_unresolved_canonical_venue_never_creates_null_place_event_or_reconciles():
    old = Event.objects.create(
        title="Keep after unresolved venue",
        starts_at=datetime(2030, 2, 1, 18, 0, tzinfo=UTC),
        source=Event.Source.SCRAPER,
        external_id="roedu:keep-unresolved",
        source_pack_id=SOCIAL_APP_PACK_ID,
        source_city="Cluj-Napoca",
    )
    with (
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.RoeduClient",
            FakeRoeduClient,
        ),
        mock.patch(
            "apps.events.management.commands.sync_roedu_events.find_duplicate",
            return_value=None,
        ),
    ):
        call_command("sync_roedu_events", "--app-pack", SOCIAL_APP_PACK_ID)

    old.refresh_from_db()
    assert not old.is_tombstone
    assert not Event.objects.filter(external_id="roedu:event-1").exists()


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
