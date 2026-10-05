package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestPostgresOperatorCase5MergeExactEdgesAndRetainedSourceProvenance(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	osm := testdb.Place(t, r.DB, "Central Library", "osm")
	duplicate := testdb.Place(t, r.DB, "Central Library", "overture")
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET external_id='ov-9',attribution='Reviewed Overture source',license_name='CDLA-Permissive-2.0',provenance_url='https://example.invalid/ov-9' WHERE id=$1`, duplicate); err != nil {
		t.Fatal(err)
	}
	operatorCase4Edge(t, r.DB, osm, "reading", .95, "inferred", "")
	operatorCase4Edge(t, r.DB, duplicate, "board_games", .8, "inferred", "")
	if applied, err := s.mergePlaces(ctx, osm, duplicate, false); err != nil || !applied {
		t.Fatal("source merge was not applied", err)
	}
	var count int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place WHERE id=$1`, duplicate).Scan(&count); err != nil || count != 0 {
		t.Fatal("merged duplicate survives", err)
	}
	var slugs []string
	if err := r.DB.QueryRow(ctx, `SELECT array_agg(t.slug ORDER BY t.slug) FROM places_placeactivity e JOIN taxonomy_activitytype t ON t.id=e.activity_id WHERE e.place_id=$1`, osm).Scan(&slugs); err != nil || len(slugs) != 2 || slugs[0] != "board_games" || slugs[1] != "reading" {
		t.Fatal("exact source merged edge set changed", slugs, err)
	}
	var raw []byte
	if err := r.DB.QueryRow(ctx, `SELECT raw_tags->'merged_sources' FROM places_place WHERE id=$1`, osm).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var provenance []map[string]any
	if json.Unmarshal(raw, &provenance) != nil || len(provenance) != 1 {
		t.Fatal("invalid merge provenance cardinality")
	}
	p := provenance[0]
	if p["source"] != "overture" || p["external_id"] != "ov-9" || p["attribution"] != "Reviewed Overture source" || p["license_name"] != "CDLA-Permissive-2.0" || p["provenance_url"] != "https://example.invalid/ov-9" || len(p) != 7 {
		t.Fatal("original identity pair or mandatory provenance extension lost")
	}
}

func operatorCase5Calendar(t *testing.T, text string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "synthetic.ics")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPostgresOperatorCase5ICSFiltersPastAndReplaysPlaceIdentity(t *testing.T) {
	r, _ := commandFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	calls := 0
	r.Config.Now = func() time.Time { calls++; return now }
	place := testdb.Place(t, r.DB, "City Library", "osm")
	text := "BEGIN:VEVENT\nUID:evt-1@venue\nSUMMARY:Chess club night\nDTSTART:" + now.AddDate(0, 0, 3).Format("20060102T150405Z") + "\nEND:VEVENT\nBEGIN:VEVENT\nUID:evt-old@venue\nSUMMARY:Past event\nDTSTART:" + now.AddDate(0, 0, -3).Format("20060102T150405Z") + "\nEND:VEVENT"
	path := operatorCase5Calendar(t, text)
	for n := 0; n < 2; n++ {
		result, err := r.Run(ctx, "ingest_events", args(map[string]any{"ics_file": path, "place": place}))
		if err != nil || result.(map[string]int)["imported"] != 1 {
			t.Fatal("source past-skip/import count changed", result, err)
		}
	}
	if calls != 2 {
		t.Fatal("each importer must capture exactly one Runner.Now", calls)
	}
	var count int
	var retained int64
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM events_event`).Scan(&count); err != nil || count != 1 {
		t.Fatal("UID-keyed import replay or past filtering failed", count, err)
	}
	if err := r.DB.QueryRow(ctx, `SELECT place_id FROM events_event WHERE external_id='evt-1@venue'`).Scan(&retained); err != nil || retained != place {
		t.Fatal("imported source event lost exact place", err)
	}
}

func TestPostgresOperatorCase5ICSClassificationAndExplicitRunningType(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			r, _ := commandFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			r.Config.Now = func() time.Time { return now }
			title := "Festival de muzică"
			if explicit {
				title = "Some generic gathering"
			}
			path := operatorCase5Calendar(t, "BEGIN:VEVENT\nUID:c1\nSUMMARY:"+title+"\nDTSTART:"+now.AddDate(0, 0, 2).Format("20060102T150405Z")+"\nEND:VEVENT")
			input := map[string]any{"ics_file": path}
			want := "festival"
			if explicit {
				var typ int64
				if err := r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='running'`).Scan(&typ); err != nil {
					t.Fatal(err)
				}
				input["activity_type"] = typ
				want = "running"
			}
			if _, err := r.Run(ctx, "ingest_events", args(input)); err != nil {
				t.Fatal(err)
			}
			var got string
			if err := r.DB.QueryRow(ctx, `SELECT t.slug FROM events_event e JOIN taxonomy_activitytype t ON t.id=e.activity_type_id WHERE e.external_id='c1'`).Scan(&got); err != nil || got != want {
				t.Fatal("source auto/explicit classification changed", got, want, err)
			}
		})
	}
}

func TestPostgresOperatorCase5RecurringImportKeepsThreeStableOccurrences(t *testing.T) {
	r, _ := commandFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	r.Config.Now = func() time.Time { return now }
	place := testdb.Place(t, r.DB, "Club", "osm")
	path := operatorCase5Calendar(t, "BEGIN:VEVENT\nUID:club@venue\nSUMMARY:Chess club\nDTSTART:"+now.AddDate(0, 0, 1).Format("20060102T150405Z")+"\nRRULE:FREQ=WEEKLY;COUNT=3\nEND:VEVENT")
	for n := 0; n < 2; n++ {
		result, err := r.Run(ctx, "ingest_events", args(map[string]any{"ics_file": path, "place": place}))
		if err != nil || result.(map[string]int)["imported"] != 3 {
			t.Fatal("recurring source import count changed", result, err)
		}
	}
	var count, distinct int
	if err := r.DB.QueryRow(ctx, `SELECT count(*),count(DISTINCT external_id) FROM events_event WHERE place_id=$1`, place).Scan(&count, &distinct); err != nil || count != 3 || distinct != 3 {
		t.Fatal("recurring replay duplicated or lost source identities", count, distinct, err)
	}
}

func TestPostgresOperatorCase5ICSOngoingBoundaryAndInvalidExplicitType(t *testing.T) {
	r, _ := commandFixture(t)
	ctx := context.Background()
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	r.Config.Now = func() time.Time { return now }
	path := operatorCase5Calendar(t, "BEGIN:VEVENT\nUID:ongoing\nSUMMARY:Ongoing\nDTSTART:"+now.Add(-time.Hour).Format("20060102T150405Z")+"\nDTEND:"+now.Add(time.Hour).Format("20060102T150405Z")+"\nEND:VEVENT\nBEGIN:VEVENT\nUID:boundary\nSUMMARY:Boundary\nDTSTART:"+now.Format("20060102T150405Z")+"\nEND:VEVENT")
	for _, id := range []any{0, -1, 9223372036854775807, "running", 1.5} {
		if _, err := r.Run(ctx, "ingest_events", args(map[string]any{"ics_file": path, "activity_type": id})); err == nil {
			t.Fatal("invalid explicit taxonomy identity imported events")
		}
	}
	var count int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM events_event`).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid explicit type wrote source events", err)
	}
	result, err := r.Run(ctx, "ingest_events", args(map[string]any{"ics_file": path}))
	if err != nil || result.(map[string]int)["imported"] != 2 {
		t.Fatal("source ongoing/boundary event was incorrectly treated as ended", result, err)
	}
}

func TestPostgresOperatorCase5DisabledGoogleSkipsAndContinuesHours(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	if _, err := r.Run(ctx, "ingest_places", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET opening_hours=NULL`); err != nil {
		t.Fatal(err)
	}
	result, err := s.enrichPlaces(ctx, args(map[string]any{"google": true}))
	if err != nil || result.(map[string]int)["google_disabled"] != 1 || result.(map[string]int)["hours_parsed"] != 1 || result.(map[string]int)["hours_updated"] != 1 {
		t.Fatal("disabled Google did not succeed with exact skip and hours counts", result, err)
	}
	var metadata, parsed bool
	if err := r.DB.QueryRow(ctx, `SELECT raw_tags?'google',opening_hours IS NOT NULL AND opening_hours!='null'::jsonb FROM places_place`).Scan(&metadata, &parsed); err != nil || metadata || !parsed {
		t.Fatal("disabled provider wrote metadata or suppressed durable hours", err)
	}
	if _, err := s.enrichPlaces(ctx, args(map[string]any{"wikidata": true})); err == nil {
		t.Fatal("missing Wikidata adapter error was weakened")
	}
	called := 0
	s.Config.GoogleEnrich = func(context.Context, EnrichPlace) (EnrichResult, error) { called++; return EnrichResult{}, nil }
	if _, err := s.enrichPlaces(ctx, nil); err != nil || called != 0 {
		t.Fatal("provider callback ran without explicit Google opt-in")
	}
}
