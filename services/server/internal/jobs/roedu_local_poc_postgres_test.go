package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func localPOCColumnExists(t *testing.T, r *Runner) bool {
	t.Helper()
	var exists bool
	if err := r.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='events_event'::regclass AND attname='source_local_poc' AND NOT attisdropped)`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

// A production sync is unchanged: no POC column, flag or tag appears.
func TestPostgresLocalPOCProductionSyncLeavesNoPOCTrace(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	// The fixture clones the template database's tables; start from the
	// production schema even if a POC sync ever ran against that template.
	if _, err := r.DB.Exec(ctx, `ALTER TABLE events_event DROP COLUMN IF EXISTS source_local_poc`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ApplyRoedu(ctx, readPackFixture(t), "Cluj-Napoca"); err != nil {
		t.Fatal(err)
	}
	if localPOCColumnExists(t, r) {
		t.Fatal("production sync altered events_event")
	}
	var tagged bool
	if err := r.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_place WHERE raw_tags?'roedu_local_poc' OR raw_tags?'roedu:local_poc_admission')`).Scan(&tagged); err != nil || tagged {
		t.Fatal("production venue carries POC tags", tagged, err)
	}
}

// ADR-0057: a gated POC sync stores honest evidence, flags every row it wrote,
// imports 0.9 rows, keeps past/expired events, and — never complete — neither
// tombstones nor records a sync state. The multi-city job skips an empty city
// and never resolves Commons covers.
func TestPostgresLocalPOCSyncFlagsRowsAndKeepsHonestEvidence(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	// jobFixture's LIKE clone drops foreign keys; restore the baseline's deferred
	// event FK, whose pending trigger events forbid a mid-transaction ALTER TABLE.
	if _, err := r.DB.Exec(ctx, `ALTER TABLE events_event ADD CONSTRAINT poc_fixture_place_fk FOREIGN KEY (place_id) REFERENCES places_place(id) DEFERRABLE INITIALLY DEFERRED`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.Exec(ctx, `INSERT INTO events_event(title,description,starts_at,url,source,external_id,created_at,updated_at,attribution,license_name,provenance_url,is_import_held,is_tombstone,lifecycle_status,source_category,source_city,source_pack_id,source_release_id,source_snapshot_id,source_venue_id,source_availability,source_currency,source_recurrence,source_timezone) VALUES('Older production row','',now()+interval '3 days','','roedu','older-1',now(),now(),'','','',false,false,'scheduled','other','Cluj-Napoca',$1,'','','','','','','Europe/Bucharest')`, SocialPack); err != nil {
		t.Fatal(err)
	}
	page := pocPage(t)
	items := page["items"].([]any)
	event := items[1].(map[string]any)
	past := deepCopy(t, event)
	past["id"], past["start_datetime"], past["starts_at"], past["status"], past["lifecycle_status"], past["confidence"] = "past-1", "2020-05-01T19:00:00+03:00", "2020-05-01T19:00:00+03:00", "expired", "expired", json.Number("0.9")
	facets := past["facets"].(map[string]any)
	facets["status"], facets["lifecycle_status"] = "expired", "expired"
	past["tags"] = []any{"event:concert", "lifecycle:expired"}
	low := deepCopy(t, event)
	low["id"], low["confidence"] = "low-1", json.Number("0.5")
	page["items"] = append(items, past, low)
	raw := pocMarshal(t, page)
	r.Config.RoeduSyncEnabled = true
	r.Config.RoeduCities = []string{"Cluj-Napoca", "Pitesti"}
	r.Config.Roedu = pocClient(t, "http://127.0.0.1:8077", true, raw)
	r.Config.ResolvePlaceCovers = func(context.Context, map[string]json.RawMessage) (any, error) {
		t.Fatal("POC sync resolved Commons covers")
		return nil, nil
	}
	result, err := r.SyncRoedu(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cities := result["cities"].(map[string]any)
	applied := cities["Cluj-Napoca"].(map[string]any)
	skipped := cities["Pitesti"].(map[string]any)
	if applied["local_poc"] != true || applied["complete"] != false || applied["venues"] != 1 || applied["events"] != 3 || skipped["skipped"] != true || skipped["refused"] != true {
		t.Fatal("multi-city POC sync", result)
	}
	var tags map[string]any
	if err = r.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE source='roedu' AND external_id='venue-1'`).Scan(&tags); err != nil {
		t.Fatal(err)
	}
	admission, _ := tags["roedu:local_poc_admission"].(map[string]any)
	attestation, _ := tags["roedu:policy_attestation"].(map[string]any)
	if tags["roedu_local_poc"] != true || tags["roedu:gdpr_relevant"] != true || tags["roedu:privacy_classification"] != "unknown" || attestation["effect"] != "deny" || admission["admission_digest"] != items[0].(map[string]any)["local_poc_admission"].(map[string]any)["admission_digest"] {
		t.Fatal("POC venue evidence not stored honestly", tags)
	}
	if !localPOCColumnExists(t, r) {
		t.Fatal("POC sync did not add its flag column")
	}
	rows, err := r.DB.Query(ctx, `SELECT external_id,source_local_poc,is_import_held,lifecycle_status,is_tombstone,starts_at<now() FROM events_event WHERE source='roedu' ORDER BY external_id`)
	if err != nil {
		t.Fatal(err)
	}
	type state struct {
		poc, held bool
		status    string
		tomb, old bool
	}
	got := map[string]state{}
	for rows.Next() {
		var id string
		var s state
		if err = rows.Scan(&id, &s.poc, &s.held, &s.status, &s.tomb, &s.old); err != nil {
			t.Fatal(err)
		}
		got[id] = s
	}
	rows.Close()
	want := map[string]state{
		"event-1": {poc: true, status: "scheduled"},
		"past-1":  {poc: true, status: "expired", old: true},
		"low-1":   {poc: true, held: true, status: "scheduled"},
		"older-1": {status: "scheduled"},
	}
	if len(got) != len(want) {
		t.Fatal("synced rows", got)
	}
	for id, s := range want {
		if got[id] != s {
			t.Fatal("row", id, got[id], "want", s)
		}
	}
	var states, audits int
	if err = r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM events_roedueventsyncstate),(SELECT count(*) FROM safety_auditlog WHERE event='ingestion.roedu_synced' AND (data::jsonb->>'local_poc')='true')`).Scan(&states, &audits); err != nil {
		t.Fatal(err)
	}
	if states != 0 || audits != 1 {
		t.Fatal("POC sync recorded a complete snapshot or lost its audit marker", states, audits)
	}

	// The same POC page with the client gate off is refused before any write.
	r.Config.Roedu = pocClient(t, "http://127.0.0.1:8077", false, raw)
	r.Config.RoeduCities = nil
	if _, err = r.SyncRoedu(ctx); err == nil {
		t.Fatal("POC page synced through a production client")
	}
	pack := PackRead{Items: []map[string]any{items[0].(map[string]any)}, Pack: SocialPack, Snapshot: "sha256-" + string(bytes.Repeat([]byte("7"), 64)), Release: "sha256-" + string(bytes.Repeat([]byte("7"), 64)), Generated: "2026-07-12T08:00:00+00:00", Mode: "partial", LocalPOC: true}
	if _, err = r.ApplyRoeduWithOptions(ctx, pack, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: 1}); !errors.Is(err, ErrLocalPOCRefused) {
		t.Fatal("POC pack applied without the gate", err)
	}
}
