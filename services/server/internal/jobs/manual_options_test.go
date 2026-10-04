package jobs

import (
	"context"
	"strings"
	"testing"
)

func TestNativeROEDUManualOptionsDryRunThresholdAndExplicitRollback(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	pack := readPackFixture(t)
	for _, item := range pack.Items {
		if item["kind"] == "event" {
			item["confidence"] = 0.8
		}
	}
	result, err := r.ApplyRoeduWithOptions(ctx, pack, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: 1, DryRun: true})
	if err != nil || result["held"] != 1 {
		t.Fatal(result, err)
	}
	var count int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM events_event`).Scan(&count); err != nil || count != 0 {
		t.Fatal("dry run wrote", count, err)
	}
	if _, err = r.ApplyRoedu(ctx, pack, "Cluj-Napoca"); err != nil {
		t.Fatal(err)
	}
	var held bool
	var source string
	if err = r.DB.QueryRow(ctx, `SELECT is_import_held,source FROM events_event WHERE external_id='event-1'`).Scan(&held, &source); err != nil || !held || source != "roedu" {
		t.Fatal(held, source, err)
	}
	if _, err = r.ApplyRoeduWithOptions(ctx, pack, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: .7}); err != nil {
		t.Fatal(err)
	}
	if err = r.DB.QueryRow(ctx, `SELECT is_import_held FROM events_event WHERE external_id='event-1'`).Scan(&held); err != nil || held {
		t.Fatal(held, err)
	}
	old := pack
	old.Snapshot = "sha256-" + strings.Repeat("a", 64)
	old.Release = old.Snapshot
	old.Generated = "2026-07-11T08:00:00+00:00"
	if _, err = r.ApplyRoeduWithOptions(ctx, old, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: 1, DryRun: true}); err == nil {
		t.Fatal("rollback silently accepted")
	}
	if _, err = r.ApplyRoeduWithOptions(ctx, old, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: 1, AllowSnapshotRollback: true, DryRun: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = r.ApplyRoeduWithOptions(ctx, old, "Cluj-Napoca", RoeduApplyOptions{MinConfidence: 1, AllowSnapshotRollback: true}); err != nil {
		t.Fatal(err)
	}
	typ, err := ClassifyActivity(ctx, r.DB, "Spectacol de teatru pentru familie")
	if err != nil || typ == nil {
		t.Fatal("source alias classifier", typ, err)
	}
}
func TestNativeCommonsManualDryRunAndRecheckOptions(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	if _, err := r.DB.Exec(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Synthetic Commons fixture','osm','',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{"wikimedia_commons":"File:Fixture.png","cover_checked":true}','','','Cluj-Napoca','','RO','',NULL,'','',now(),now(),'','','')`); err != nil {
		t.Fatal(err)
	}
	result, err := r.ResolveCoversWithOptions(ctx, CoverResolveOptions{Limit: 200, DryRun: true})
	if err != nil || result.(map[string]int)["seen"] != 0 || result.(map[string]int)["skipped_checked"] != 1 {
		t.Fatal(result, err)
	}
	result, err = r.ResolveCoversWithOptions(ctx, CoverResolveOptions{Limit: 200, DryRun: true, Recheck: true})
	if err != nil || result.(map[string]int)["seen"] != 1 {
		t.Fatal(result, err)
	}
	var covers int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placecover`).Scan(&covers); err != nil || covers != 0 {
		t.Fatal(covers, err)
	}
}
