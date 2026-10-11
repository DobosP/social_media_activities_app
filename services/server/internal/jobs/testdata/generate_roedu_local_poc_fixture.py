"""Regenerate roedu-local-poc-page.json from the real 5/8 producer (ADR-0057).

Local-POC contract v1 (cross-repo). Needs a Python where romania_scraper
(policy schema 5 / ruleset 8) is importable; the 2026-10-12 run used
romania_scraper main 000ecee2. From any directory outside the repo:

    python -I generate_roedu_local_poc_fixture.py <repo root> <output dir>

then copy roedu-local-poc-page.json into this directory. The chain:
  1. the production fixture's own lineage evidence (generate_roedu_fixtures.py:
     same document, capture and acquisition digests), but with the source's
     REAL privacy classification `unknown`, evaluated by the producer's
     evaluate_policy -> a real publish_source DENY whose reasons are all in the
     contract's WAIVABLE set (asserted);
  2. the contract §2 admission over that deny (canonical JSON + sha256), with
     both known-answer vectors asserted first;
  3. the served production page (roedu-page.json) with each item's policy
     fields replaced by that deny attestation, the honest gdpr_relevant /
     privacy_classification, and the admission; the envelope gains
     `local_poc: true` and is partial with withheld > 0, as every POC page is.

ro_data_server's POC projection is not built yet, so step 3 follows contract
§3/§5 (production keys plus the one admission key; real privacy values;
attestation fields verbatim from the producer's compact deny).
"""

from __future__ import annotations

import copy
import hashlib
import json
import sys
from pathlib import Path

from romania_scraper.acquisition import (
    AcquisitionEvidence,
    AcquisitionLane,
    acquisition_evidence_sha256,
)
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

EXPECTED_HASH = "140290d4bc6374ebb5b7922dee98c79bee5501a6cf76e1ef18923a8647068fd1"
WAIVABLE = {
    "capability_denied",
    "confidentiality_not_public",
    "copyright_unknown",
    "missing_legal_basis",
    "personal_data_not_cleared",
    "privacy_personal_data",
    "privacy_revision_missing",
    "privacy_unknown",
}
POC_ROOT = "/mnt/data/roedu-local-poc"
# Identical to gen_social_fixtures so subject/capture/acquisition fields of the
# production fixture stay the real evidence this deny was evaluated over.
SOURCE = "opera_cluj_events"
SOURCE_URL = "https://opera-cluj.ro/"
ACCESS_TYPE = "public_document"
LEGAL_BASIS = "Law 8/1996 art. 9"
LAST_SEEN = "2026-07-12T08:00:00+00:00"
SURFACE = '{"processed_document":"public event listing"}'


def _hex(label: str) -> str:
    return hashlib.sha256(label.encode("utf-8")).hexdigest()


def canonical_json(value: object) -> bytes:
    return json.dumps(
        value, sort_keys=True, separators=(",", ":"), ensure_ascii=False
    ).encode("utf-8")


def admission(decision: dict, poc_root: str) -> dict:
    body = {
        "schema_version": 1,
        "kind": "owner_local_development_override",
        "not_a_legal_basis": True,
        "underlying_decision_id": decision["decision_id"],
        "waived_reasons": sorted(set(decision["reasons"])),
        "poc_root": poc_root,
    }
    body["admission_digest"] = hashlib.sha256(canonical_json(body)).hexdigest()
    return body


def deny_decision(privacy: PrivacyClassification) -> dict:
    sha256 = hashlib.sha256(SURFACE.encode("utf-8")).hexdigest()
    capture_id = _hex("social-fixture:capture")
    privacy_revision = _hex("social-fixture:privacy-revision")
    processed_ref = f"{sha256[:2]}/{sha256}.json.zst"
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
    rights_tag = classify_record(
        sha256=sha256,
        access_type=ACCESS_TYPE,
        legal_basis=LEGAL_BASIS,
        document_type="event_listing",
        source=SOURCE,
        privacy_classification=privacy,
        privacy_revision=privacy_revision,
        capture_id=capture_id,
        text=SURFACE,
    ).model_copy(update={"scanned_source": processed_ref})
    evidence = PolicyEvidence(
        sha256=sha256,
        capture_id=capture_id,
        capture_schema_version=3,
        acquisition_lane=acquisition.lane,
        acquisition_evidence_sha256=acquisition_evidence_sha256(acquisition),
        acquisition_evidence=acquisition,
        source=SOURCE,
        access_type=ACCESS_TYPE,
        legal_basis=LEGAL_BASIS,
        privacy_classification=privacy,
        privacy_revision=privacy_revision,
        metadata_status=EvidenceStatus.OK,
        rights_status=EvidenceStatus.OK,
        current_processed_refs=(processed_ref,),
        artifact_processed_ref=processed_ref,
        artifact_text_sha256=artifact_text_sha256(SURFACE),
        rights_tag=rights_tag,
    )
    decision = evaluate_policy(evidence).compact(PolicyAction.PUBLISH_SOURCE)
    assert decision["effect"] == "deny", decision
    assert decision["reasons"] and set(decision["reasons"]) <= WAIVABLE, decision["reasons"]
    assert decision["subject_sha256"] == sha256
    return decision


def poc_item(item: dict, decision: dict, privacy: str) -> dict:
    out = copy.deepcopy(item)
    attestation = out["policy_attestation"]
    for key in (
        "decision_id",
        "evidence_digest",
        "clearance_digest",
        "subject_sha256",
        "schema_version",
        "ruleset_version",
        "ruleset_hash",
        "action",
        "effect",
        "reasons",
        "obligations",
    ):
        attestation[key] = decision[key]
    out["policy_decision_id"] = decision["decision_id"]
    out["gdpr_relevant"] = True
    out["privacy_classification"] = privacy
    out["local_poc_admission"] = admission(decision, POC_ROOT)
    return out


def main() -> None:
    social = Path(sys.argv[1]).resolve()
    out = Path(sys.argv[2]).resolve()
    assert (POLICY_SCHEMA_VERSION, POLICY_RULESET_VERSION, POLICY_RULESET_HASH) == (5, 8, EXPECTED_HASH)
    production = json.loads(
        (social / "services/server/internal/jobs/testdata/roedu-page.json").read_text("utf-8")
    )
    decision = deny_decision(PrivacyClassification.UNKNOWN)
    page = copy.deepcopy(production)
    page["snapshot_mode"] = "partial"
    page["snapshot_complete"] = False
    page["withheld"] = 2
    page["items"] = [poc_item(item, decision, "unknown") for item in production["items"]]
    order = (
        "pack_id app layer schema_version snapshot_id release_id snapshot_generated_at "
        "snapshot_mode snapshot_complete items pagination withheld errors local_poc"
    ).split()
    page["local_poc"] = True
    page = {key: page[key] for key in order}
    kat = {
        "schema_version": 1,
        "kind": "owner_local_development_override",
        "not_a_legal_basis": True,
        "underlying_decision_id": "pd-" + "0123456789abcdef" * 4,
        "waived_reasons": ["copyright_unknown", "personal_data_not_cleared", "privacy_personal_data"],
        "poc_root": "/mnt/data/roedu-local-poc",
    }
    assert hashlib.sha256(canonical_json(kat)).hexdigest() == "974486a47fc9191208ee3b8c6dc8c8a2584303ae5e8b7570336285324c3d2681"
    kat["poc_root"] = "/mnt/data/roedu-local-poc/ă<&>"
    assert hashlib.sha256(canonical_json(kat)).hexdigest() == "b354282b94e0f0c1a3ff9a2a12036899a17b9ad9df22156adaa3375f028bb650"
    out.mkdir(parents=True, exist_ok=True)
    (out / "roedu-local-poc-page.json").write_text(
        json.dumps(page, separators=(",", ":")) + "\n", encoding="utf-8"
    )
    print(json.dumps({"reasons": decision["reasons"], "effect": decision["effect"], "obligations": decision["obligations"], "admission": page["items"][0]["local_poc_admission"]}, indent=1, ensure_ascii=False))


if __name__ == "__main__":
    main()
