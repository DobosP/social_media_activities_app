"""Regenerate roedu-page.json and roedu-validator.json from the real producer.

ADR-0055. The policy-bound fields of these fixtures (content/capture/acquisition
digests, privacy revision, decision id and the whole policy attestation) are
outputs of the producer's evaluator, so they are regenerated, never edited.
Needs a Python where romania_scraper (policy schema 5 / ruleset 8) and
ro_data_server are importable; the 2026-10-11 run used romania_scraper main
000ecee2 and ro_data_server 5183272. From any directory outside the repo:

    python -I generate_roedu_fixtures.py <repo root> <output dir>

then copy the two JSON files into this directory. The chain is all real code:
  1. producer rights classifier (classifier 8), acquisition evidence and
     evaluate_policy -> compact publish_source decision for one cleared
     redistributable event_listing document;
  2. producer EventStore -> events.db (venue, event and, for the validator
     golden, a removed event);
  3. producer lake.social.materialize_social_items -> raw social pack items;
  4. ro_data_server app_packs._public_pack_view -> the served safe page;
  5. this repo's legacy Python oracle (apps/ingestion/sources/roedu_client.py)
     decides `valid` for every validator golden row.

Consumer-facing values (ids, names, coordinates, timestamps, release id) keep
the earlier synthetic fixtures' values where the real chain serves them, so the
Go tests that assert on them keep their meaning.
"""

from __future__ import annotations

import copy
import hashlib
import importlib.util
import json
import sqlite3
import sys
import tempfile
import types
from pathlib import Path

from ro_data_server.app_packs import CANONICAL_SOCIAL_PACK_ID, _public_pack_view
from romania_scraper.acquisition import (
    AcquisitionEvidence,
    AcquisitionLane,
    acquisition_evidence_sha256,
)
from romania_scraper.events import Event, EventCategory, EventStore, Venue
from romania_scraper.lake.semantic import LakeSemanticDocument
from romania_scraper.lake.social import materialize_social_items
from romania_scraper.models import Decision, PrivacyClassification
from romania_scraper.policy import (
    POLICY_RULESET_HASH,
    POLICY_RULESET_VERSION,
    POLICY_SCHEMA_VERSION,
    EvidenceStatus,
    PolicyAction,
    PolicyEvidence,
    artifact_text_sha256,
    evaluate_policy,
)
from romania_scraper.rights import classify_record
from romania_scraper.rights.models import CLASSIFIER_VERSION

EXPECTED_HASH = "140290d4bc6374ebb5b7922dee98c79bee5501a6cf76e1ef18923a8647068fd1"
SOURCE = "opera_cluj_events"
SOURCE_URL = "https://opera-cluj.ro/"
ACCESS_TYPE = "public_document"
LEGAL_BASIS = "Law 8/1996 art. 9"
FIRST_SEEN = "2026-07-01T08:00:00+00:00"
LAST_SEEN = "2026-07-12T08:00:00+00:00"
GENERATED_AT = "2026-07-12T08:00:00+00:00"
RELEASE_ID = "sha256-" + "7" * 64
SURFACE = '{"processed_document":"public event listing"}'


def _hex(label: str) -> str:
    return hashlib.sha256(label.encode("utf-8")).hexdigest()


def _lineage() -> LakeSemanticDocument:
    sha256 = hashlib.sha256(SURFACE.encode("utf-8")).hexdigest()
    capture_id = _hex("social-fixture:capture")
    privacy_revision = _hex("social-fixture:privacy-revision")
    processed_ref = f"{sha256[:2]}/{sha256}.json.zst"
    text_sha256 = artifact_text_sha256(SURFACE)
    acquisition = AcquisitionEvidence(
        lane=AcquisitionLane.SANCTIONED_API,
        source=SOURCE,
        source_policy_revision=privacy_revision,
        run_id="social-consumer-fixture",
        observed_at=LAST_SEEN,
        artifact_url=SOURCE_URL,
        outcome=Decision.FETCHED,
        content_sha256=sha256,
        content_length=len(SURFACE.encode("utf-8")),
        producer="sanctioned-api:social-consumer-fixture-v1",
    )
    acquisition_digest = acquisition_evidence_sha256(acquisition)
    rights_tag = classify_record(
        sha256=sha256,
        access_type=ACCESS_TYPE,
        legal_basis=LEGAL_BASIS,
        document_type="event_listing",
        source=SOURCE,
        privacy_classification=PrivacyClassification.NO_PERSONAL_DATA,
        privacy_revision=privacy_revision,
        capture_id=capture_id,
        text=SURFACE,
    ).model_copy(update={"scanned_source": processed_ref})
    evidence = PolicyEvidence(
        sha256=sha256,
        capture_id=capture_id,
        capture_schema_version=3,
        acquisition_lane=acquisition.lane,
        acquisition_evidence_sha256=acquisition_digest,
        acquisition_evidence=acquisition,
        source=SOURCE,
        access_type=ACCESS_TYPE,
        legal_basis=LEGAL_BASIS,
        privacy_classification=PrivacyClassification.NO_PERSONAL_DATA,
        privacy_revision=privacy_revision,
        metadata_status=EvidenceStatus.OK,
        rights_status=EvidenceStatus.OK,
        current_processed_refs=(processed_ref,),
        artifact_processed_ref=processed_ref,
        artifact_text_sha256=text_sha256,
        rights_tag=rights_tag,
    )
    decision = evaluate_policy(evidence).compact(PolicyAction.PUBLISH_SOURCE)
    assert decision["effect"] == "allow", decision["reasons"]
    assert decision["reasons"] == ["explicitly_cleared"], decision["reasons"]
    return LakeSemanticDocument(
        doc_id="doc:social-consumer-fixture",
        layer="redistributable",
        title="Program Teatrul Național Cluj",
        language="ro",
        source=SOURCE,
        source_url=SOURCE_URL,
        sha256=sha256,
        capture_id=capture_id,
        capture_schema_version=3,
        acquisition_lane=acquisition.lane.value,
        acquisition_evidence_sha256=acquisition_digest,
        processed_ref=processed_ref,
        text_sha256=text_sha256,
        proc_key="passthrough@1",
        document_type="event_listing",
        access_type=ACCESS_TYPE,
        legal_basis=LEGAL_BASIS,
        privacy_classification="no_personal_data",
        privacy_revision=privacy_revision,
        policy_decision=decision,
        rights=rights_tag.model_dump(mode="json"),
        redistributable=True,
        generated_at=GENERATED_AT,
        topics=("roedu:topic:culture",),
        tags=("event",),
        facets={"city": "Cluj-Napoca"},
        app_packs=("social_media_activities_app",),
    )


def _event(event_id: str, sha256: str) -> Event:
    return Event(
        id=event_id,
        dedup_key=event_id,
        title="Concert",
        category=EventCategory.CONCERT,
        start_datetime="2030-01-01T18:00:00+02:00",
        end_datetime=None,
        timezone="Europe/Bucharest",
        rrule="FREQ=WEEKLY",
        venue_id="venue-1",
        city="Cluj-Napoca",
        county="Cluj",
        ticket_url="https://tickets.example.org/concert",
        price_min=20.0,
        price_max=50.0,
        currency="RON",
        is_free=False,
        availability="available",
        source=SOURCE,
        source_url=SOURCE_URL,
        access_type=ACCESS_TYPE,
        legal_basis=LEGAL_BASIS,
        gdpr_relevant=False,
        sha256=sha256,
        extractor="jsonld@1",
        confidence=1.0,
        cross_source_urls=[SOURCE_URL],
        first_seen=FIRST_SEEN,
        last_seen=LAST_SEEN,
    )


def _events_db(path: Path, lineage: LakeSemanticDocument, *, tombstone: bool) -> Path:
    venue = Venue(
        id="venue-1",
        name="Teatrul Național Cluj",
        address="Piața Ștefan cel Mare 2",
        city="Cluj-Napoca",
        county="Cluj",
        country="RO",
        lat=46.7712,
        lon=23.5949,
        source=SOURCE,
        source_url=SOURCE_URL,
        access_type=ACCESS_TYPE,
        legal_basis=LEGAL_BASIS,
        first_seen=FIRST_SEEN,
        last_seen=LAST_SEEN,
    )
    store = EventStore(path)
    try:
        store.upsert_venue(venue)
        store.upsert_event(_event("event-1", lineage.sha256))
        if tombstone:
            store.upsert_event(_event("deleted", lineage.sha256))
    finally:
        store.close()
    conn = sqlite3.connect(path)
    try:
        # Same observation-time pinning the producer's own social tests use.
        conn.execute("UPDATE venues SET first_seen=?, last_seen=?", (FIRST_SEEN, LAST_SEEN))
        conn.execute(
            "UPDATE events SET first_seen=?, last_seen=?, status_observed_at=?",
            (FIRST_SEEN, LAST_SEEN, LAST_SEEN),
        )
        conn.execute("UPDATE events SET status='removed' WHERE id='deleted'")
        conn.commit()
    finally:
        conn.close()
    return path


def _served_page(work: Path, lineage: LakeSemanticDocument, *, tombstone: bool) -> dict:
    db = _events_db(work / f"events-{int(tombstone)}.db", lineage, tombstone=tombstone)
    social = materialize_social_items(db, [lineage])
    assert social.available and social.withheld == 0, social
    raw = {
        "pack_id": CANONICAL_SOCIAL_PACK_ID,
        "app": "social_media_activities_app",
        "layer": "redistributable",
        "schema_version": 1,
        "snapshot_generated_at": GENERATED_AT,
        "snapshot_mode": "full",
        "snapshot_complete": True,
        "items": list(social.items),
        "pagination": {"next_cursor": None},
        "withheld": 0,
        "errors": [],
    }
    # The promotion only contributes the immutable release identity
    # (`payload.update(snapshot_id/release_id)` in _public_pack_view).
    view = _public_pack_view(raw, snapshot=types.SimpleNamespace(release_id=RELEASE_ID))
    payload = view.payload if hasattr(view, "payload") else view[0]
    assert payload["withheld"] == 0 and payload["snapshot_mode"] == "full", payload
    assert len(payload["items"]) == len(raw["items"]), "projection withheld an item"
    order = (
        "pack_id app layer schema_version snapshot_id release_id snapshot_generated_at "
        "snapshot_mode snapshot_complete items pagination withheld errors"
    ).split()
    assert set(payload) == set(order), sorted(payload)
    return {key: payload[key] for key in order}


def _mutations(item: dict) -> list[dict]:
    """The ten rows per kind the previous golden carried, in the same order."""

    def changed(**updates) -> dict:
        mutated = copy.deepcopy(item)
        mutated.update(updates)
        return mutated

    lifecycle = changed(status={}, lifecycle_status={})
    lifecycle["facets"] = {**lifecycle["facets"], "status": {}, "lifecycle_status": {}}
    return [
        copy.deepcopy(item),
        changed(gdpr_relevant=True),
        changed(access_type={}),
        changed(acquisition_lane=[]),
        changed(capture_schema_version=True),
        changed(description="private prose"),
        changed(confidence=-1),
        changed(availability={}),
        lifecycle,
        changed(venue_id={}),
    ]


def _legacy_oracle(social_root: Path):
    package = social_root / "apps" / "ingestion" / "sources"
    sys.path.insert(0, str(social_root))
    spec = importlib.util.spec_from_file_location(
        "apps.ingestion.sources.roedu_client", package / "roedu_client.py"
    )
    assert spec and spec.loader
    module = importlib.util.module_from_spec(spec)
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    assert (
        module.POLICY_SCHEMA_VERSION,
        module.POLICY_RULESET_VERSION,
        module.POLICY_RULESET_HASH,
    ) == (5, 8, EXPECTED_HASH), "legacy oracle is not pinned to 5/8"
    return module.is_canonical_social_app_pack_item


def _dump(value: object) -> str:
    return json.dumps(value, separators=(",", ":")) + "\n"


def main() -> None:
    social_root = Path(sys.argv[1]).resolve()
    out = Path(sys.argv[2]).resolve()
    assert (POLICY_SCHEMA_VERSION, POLICY_RULESET_VERSION) == (5, 8)
    assert POLICY_RULESET_HASH == EXPECTED_HASH
    assert CLASSIFIER_VERSION == 8
    oracle = _legacy_oracle(social_root)
    lineage = _lineage()
    out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory() as tmp:
        work = Path(tmp)
        page = _served_page(work, lineage, tombstone=False)
        full = _served_page(work, lineage, tombstone=True)
    by_id = {item["id"]: item for item in full["items"]}
    assert [item["id"] for item in page["items"]] == ["venue-1", "event-1"]
    assert page["items"] == [by_id["venue-1"], by_id["event-1"]]
    rows = []
    for base_id in ("venue-1", "event-1", "deleted"):
        for index, item in enumerate(_mutations(by_id[base_id])):
            valid = oracle(item)
            assert valid is (index == 0), (base_id, index, valid)
            rows.append({"item": item, "valid": valid})
    (out / "roedu-page.json").write_text(_dump(page), encoding="utf-8")
    (out / "roedu-validator.json").write_text(_dump(rows), encoding="utf-8")
    attestation = page["items"][0]["policy_attestation"]
    report = {
        "producer": [POLICY_SCHEMA_VERSION, POLICY_RULESET_VERSION, POLICY_RULESET_HASH],
        "classifier_version": CLASSIFIER_VERSION,
        "rights_lawful_basis_fields": {
            key: lineage.rights.get(key)
            for key in ("personal_data", "lawful_basis", "lawful_basis_reason", "lawful_basis_ref")
        },
        "attestation_keys": sorted(attestation),
        "item_keys": {item["kind"]: sorted(item) for item in full["items"]},
        "reasons": attestation["reasons"],
        "obligations": attestation["obligations"],
        "rows": len(rows),
    }
    print(json.dumps(report, indent=1))


if __name__ == "__main__":
    main()
