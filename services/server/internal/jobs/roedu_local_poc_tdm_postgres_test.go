package jobs

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// jobFixture requires root's explicit disposable PostgreSQL fixture. This is
// storage qualification, never a request to run against the active local POC.
func TestPostgresLocalPOCTDMSyncStoresOriginalAccessAndDeny(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	page := tdmPOCPage(t)
	r.Config.Roedu = pocClient(t, "http://127.0.0.1:8077", true, pocMarshal(t, page))
	pack, err := r.Config.Roedu.Read(ctx, "Cluj-Napoca")
	if err != nil {
		t.Fatal(err)
	}
	summary, err := r.ApplyRoeduWithOptions(ctx, pack, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: LocalPOCMinConfidence})
	if err != nil || summary["local_poc"] != true || summary["complete"] != false || summary["venues"] != 1 || summary["events"] != 1 || summary["tombstones"] != 0 {
		t.Fatal("local TDM sync", summary, err)
	}
	var tags map[string]any
	if err := r.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE source='roedu' AND external_id='venue-1'`).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	if tags["roedu_local_poc"] != true || tags["roedu:access_type"] != "tdm_exception" || tags["roedu:gdpr_relevant"] != true || tags["roedu:privacy_classification"] != "unknown" || !bytes.Equal(pocMarshal(t, tags["roedu:policy_attestation"].(map[string]any)), pocMarshal(t, pack.Items[0]["policy_attestation"].(map[string]any))) || !bytes.Equal(pocMarshal(t, tags["roedu:local_poc_admission"].(map[string]any)), pocMarshal(t, pack.Items[0]["local_poc_admission"].(map[string]any))) {
		t.Fatal("stored venue access or real deny was relabelled")
	}
	var local bool
	var evidence map[string]any
	if err := r.DB.QueryRow(ctx, `SELECT source_local_poc,source_local_poc_evidence FROM events_event WHERE source='roedu' AND external_id='event-1'`).Scan(&local, &evidence); err != nil {
		t.Fatal(err)
	}
	if !local || !bytes.Equal(pocMarshal(t, evidence), pocMarshal(t, LocalPOCEventEvidence(pack.Items[1]))) || evidence["access_type"] != "tdm_exception" || evidence["policy_attestation"].(map[string]any)["effect"] != "deny" {
		t.Fatal("stored event lost original access, real deny or admission")
	}
}

func TestPostgresLocalPOCTDMOutsideGateWritesNothing(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	var beforeEvents, beforePlaces int
	if err := r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM events_event),(SELECT count(*) FROM places_place)`).Scan(&beforeEvents, &beforePlaces); err != nil {
		t.Fatal(err)
	}
	beforeColumn := localPOCColumnExists(t, r)
	page := tdmPOCPage(t)
	items := []map[string]any{}
	for _, item := range page["items"].([]any) {
		items = append(items, item.(map[string]any))
	}
	pack := PackRead{Items: items, Pack: SocialPack, LocalPOC: true}
	r.Config.Roedu = &RoeduClient{BaseURL: "http://127.0.0.1:8077", LocalPOC: false}
	if _, err := r.ApplyRoeduWithOptions(ctx, pack, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: LocalPOCMinConfidence}); !errors.Is(err, ErrLocalPOCRefused) {
		t.Fatal("TDM POC write accepted with development opt-in off", err)
	}
	var afterEvents, afterPlaces int
	if err := r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM events_event),(SELECT count(*) FROM places_place)`).Scan(&afterEvents, &afterPlaces); err != nil {
		t.Fatal(err)
	}
	if beforeEvents != afterEvents || beforePlaces != afterPlaces || beforeColumn != localPOCColumnExists(t, r) {
		t.Fatal("off-gate refusal changed rows or the development schema")
	}
}
