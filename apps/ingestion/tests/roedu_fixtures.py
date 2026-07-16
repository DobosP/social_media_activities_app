"""Canonical schema-v1 social app-pack response fixtures."""

from __future__ import annotations

from copy import deepcopy

from apps.ingestion.sources.roedu_client import (
    POLICY_RULESET_HASH,
    SOCIAL_APP_NAME,
    SOCIAL_APP_PACK_ID,
)

_CONTENT_ID = "a" * 64
_CAPTURE_ID = "b" * 64
_ACQUISITION_DIGEST = "c" * 64
_PRIVACY_REVISION = "d" * 64
_RELEASE_ID = "sha256-" + "7" * 64


def _common(*, item_id: str, kind: str, title: str) -> dict:
    decision_id = "e" * 64
    return {
        "id": item_id,
        "kind": kind,
        "title": title,
        "tags": [],
        "facets": {},
        "source": "opera_cluj_events",
        "provenance": {},
        "license": "RO-LAW-8-1996-ART-9",
        "attribution": "opera_cluj_events",
        "access_type": "public_document",
        "legal_basis": "Law 8/1996 art. 9",
        "gdpr_relevant": False,
        "privacy_classification": "no_personal_data",
        "privacy_revision": _PRIVACY_REVISION,
        "policy_decision_id": decision_id,
        "redistributable": True,
        "confidence": 1.0,
        "content_id": _CONTENT_ID,
        "capture_id": _CAPTURE_ID,
        "capture_schema_version": 3,
        "acquisition_lane": "sanctioned_api",
        "acquisition_evidence_sha256": _ACQUISITION_DIGEST,
        "policy_attestation": {
            "decision_id": decision_id,
            "evidence_digest": "f" * 64,
            "clearance_digest": "1" * 64,
            "subject_sha256": _CONTENT_ID,
            "schema_version": 4,
            "ruleset_version": 6,
            "ruleset_hash": POLICY_RULESET_HASH,
            "action": "publish_source",
            "effect": "allow",
            "reasons": ["explicitly_cleared"],
            "obligations": [],
            "capture_id": _CAPTURE_ID,
            "capture_schema_version": 3,
            "acquisition_lane": "sanctioned_api",
            "acquisition_evidence_sha256": _ACQUISITION_DIGEST,
        },
        "first_seen": "2026-07-01T08:00:00+00:00",
        "last_seen": "2026-07-12T08:00:00+00:00",
        "updated_at": "2026-07-12T08:00:00+00:00",
    }


def venue_item(**updates) -> dict:
    item = _common(item_id="venue-1", kind="venue", title="Teatrul Național Cluj")
    item.update(
        {
            "tags": ["venue:theatre"],
            "facets": {
                "city": "Cluj-Napoca",
                "county": "Cluj",
                "category": "theatre",
                "venue_category": "theatre",
                "place_category": "theatre",
            },
            "location": {"lat": 46.7712, "lon": 23.5949},
            "address": {
                "street": "Piața Ștefan cel Mare 2",
                "city": "Cluj-Napoca",
                "county": "Cluj",
                "country": "RO",
            },
            "category": "theatre",
            "website": "https://opera-cluj.ro/",
        }
    )
    item.update(updates)
    return item


def event_item(**updates) -> dict:
    item = _common(item_id="event-1", kind="event", title="Concert")
    item.update(
        {
            "tags": ["event:concert", "lifecycle:scheduled"],
            "facets": {
                "city": "Cluj-Napoca",
                "county": "Cluj",
                "category": "concert",
                "venue_id": "venue-1",
                "place_id": "venue-1",
                "status": "scheduled",
                "lifecycle_status": "scheduled",
            },
            "category": "concert",
            "status": "scheduled",
            "lifecycle_status": "scheduled",
            "cancelled": False,
            "tombstone": False,
            "is_tombstone": False,
            "venue_id": "venue-1",
            "place_id": "venue-1",
            "start_datetime": "2030-01-01T18:00:00+02:00",
            "starts_at": "2030-01-01T18:00:00+02:00",
            "end_datetime": "",
            "ends_at": "",
            "timezone": "Europe/Bucharest",
            "recurrence": "FREQ=WEEKLY",
            "ticket_url": "https://tickets.example.test/concert",
            "price_min": 20.0,
            "price_max": 50.0,
            "currency": "RON",
            "is_free": False,
            "availability": "available",
        }
    )
    item.update(updates)
    return item


def tombstone_item(**updates) -> dict:
    item = _common(item_id="deleted", kind="event_tombstone", title="")
    item.update(
        {
            "tags": ["event:concert", "lifecycle:removed"],
            "facets": {
                "city": "Cluj-Napoca",
                "county": "Cluj",
                "category": "concert",
                "venue_id": "venue-1",
                "place_id": "venue-1",
                "status": "removed",
                "lifecycle_status": "removed",
            },
            "category": "concert",
            "status": "removed",
            "lifecycle_status": "removed",
            "cancelled": False,
            "tombstone": True,
            "is_tombstone": True,
            "venue_id": "venue-1",
            "place_id": "venue-1",
        }
    )
    item.update(updates)
    return item


def pack_page(
    items: list[dict] | None = None,
    *,
    cursor: str | None = None,
    snapshot_id: str = _RELEASE_ID,
    release_id: str = _RELEASE_ID,
    mode: str = "full",
    complete: bool = True,
    withheld: int = 0,
    errors: list[str] | None = None,
    **updates,
) -> dict:
    page = {
        "pack_id": SOCIAL_APP_PACK_ID,
        "app": SOCIAL_APP_NAME,
        "layer": "redistributable",
        "schema_version": 1,
        "snapshot_id": snapshot_id,
        "release_id": release_id,
        "snapshot_generated_at": "2026-07-12T08:00:00+00:00",
        "snapshot_mode": mode,
        "snapshot_complete": complete,
        "items": deepcopy(items or []),
        "pagination": {"next_cursor": cursor},
        "withheld": withheld,
        "errors": list(errors or []),
    }
    page.update(updates)
    return page
