package commands

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var commandsDSN = flag.String("commands-test-dsn", "", "explicit disposable native manual-command fixture database")

func commandFixture(t *testing.T) (*jobs.Runner, *Service) {
	t.Helper()
	db := testdb.New(t, *commandsDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	runner := jobs.New(db, jobs.DefaultConfig())
	cfg := Config{Recommendations: recommendations.New(db, catalog.New(db), social.New(db, platform.RecordAudit)), OverpassURL: "https://overpass.fixture/api", FetchOverpass: func(context.Context, string, string) ([]byte, error) {
		return []byte(`{"elements":[{"type":"node","id":123,"lon":23.61,"lat":46.77,"tags":{"name":"Fixture OSM library","amenity":"library","addr:city":"Cluj-Napoca","opening_hours":"24/7","website":"https://library.fixture"}}]}`), nil
	}}
	if err := Install(runner, cfg); err != nil {
		t.Fatal(err)
	}
	return runner, &Service{runner, cfg}
}
func args(in map[string]any) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for k, v := range in {
		out[k], _ = json.Marshal(v)
	}
	return out
}
func TestNativeManualSourceConversionAndQuerySafety(t *testing.T) {
	query, err := OverpassQuery("Cluj-Napoca", "23,46,24,47")
	if err != nil || !strings.Contains(query, "(46,23,47,24)") {
		t.Fatal(query, err)
	}
	if _, err = OverpassQuery("city", "180,91,190,95"); err == nil {
		t.Fatal("invalid geo area")
	}
	raw, err := ParseOverpass([]byte(`{"elements":[{"type":"way","id":12,"center":{"lon":23.6,"lat":46.77},"tags":{"name":"Fixture","contact:website":"https://fixture.local","addr:city":"Cluj-Napoca"}}]}`))
	if err != nil || len(raw) != 1 || raw[0].Website != "https://fixture.local" || raw[0].OSMType != "way" {
		t.Fatal(raw, err)
	}
	matches := MatchOverture(" LIBRARY ", []string{"soccer_field"})
	if len(matches) != 2 || matches[0].Slug != "reading" || matches[1].Confidence != .63 {
		t.Fatal(matches)
	}
}
func TestNativeManualOSMIdempotenceProtectedDisputesAndDryRun(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	out, err := r.Run(ctx, "ingest_places", args(map[string]any{"city": "Cluj-Napoca", "dry_run": true}))
	if err != nil || out.(map[string]int)["seen"] != 1 {
		t.Fatal(out, err)
	}
	var count int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	out, err = r.Run(ctx, "ingest_places", nil)
	if err != nil || out.(map[string]int)["place_created"] != 1 {
		t.Fatal(out, err)
	}
	var place, edge int64
	if err = r.DB.QueryRow(ctx, `SELECT p.id,pa.id FROM places_place p JOIN places_placeactivity pa ON pa.place_id=p.id WHERE p.osm_id=123 AND pa.source='osm'`).Scan(&place, &edge); err != nil {
		t.Fatal(err)
	}
	if _, err = r.DB.Exec(ctx, `UPDATE places_placeactivity SET is_disputed=true,origin='confirmed',confidence=0.99 WHERE id=$1`, edge); err != nil {
		t.Fatal(err)
	}
	out, err = r.Run(ctx, "ingest_places", nil)
	if err != nil || out.(map[string]int)["place_updated"] != 1 {
		t.Fatal(out, err)
	}
	var disputed bool
	var origin string
	if err = r.DB.QueryRow(ctx, `SELECT is_disputed,origin FROM places_placeactivity WHERE id=$1`, edge).Scan(&disputed, &origin); err != nil || !disputed || origin != "confirmed" {
		t.Fatal(disputed, origin, err)
	}
	if _, err = r.Run(ctx, "ingest_places", args(map[string]any{"source": "overture", "bbox": "23,46,24,47", "overture_path": "fixture.parquet"})); err == nil {
		t.Fatal("missing native provider silently accepted")
	}
	if out, err = s.enrichPlaces(ctx, args(map[string]any{"google": true})); err != nil || out.(map[string]int)["google_disabled"] != 1 || out.(map[string]int)["hours_parsed"] != 1 {
		t.Fatal("disabled Google did not skip with exact count and continue hours", out, err)
	}
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place WHERE raw_tags?'google'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("disabled Google changed provider metadata", count, err)
	}
	if _, err = r.Run(ctx, "ingest_places", args(map[string]any{"password": "injected"})); err == nil {
		t.Fatal("unknown option ignored")
	}
}
func TestNativeManualMergeDependenciesProvenanceAndAggregation(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	canonical := testdb.Place(t, r.DB, "Café fixture library", "osm")
	duplicate := testdb.Place(t, r.DB, "Cafe fixture library.", "overture")
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET external_id='fixture-secondary',license_name='CC BY 4.0',attribution='Fixture secondary' WHERE id=$1`, duplicate); err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(ctx, "dedup_places", args(map[string]any{"apply": true}))
	if err != nil || out.(map[string]int)["merged"] != 1 {
		t.Fatal(out, err)
	}
	var tags []byte
	if err = r.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE id=$1`, canonical).Scan(&tags); err != nil || !strings.Contains(string(tags), "CC BY 4.0") {
		t.Fatal(string(tags), err)
	}
	protected := testdb.Place(t, r.DB, "Cafe fixture library.", "overture")
	var typ int64
	if err = r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='reading'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	owner := testdb.Actor(t, r.DB, "fixture-owner", "adult")
	min := 1
	if _, err = social.New(r.DB, platform.RecordAudit).CreateActivity(ctx, owner, social.ActivityInput{Place: protected, ActivityType: typ, Title: "Fixture dependent meetup", StartsAt: time.Now().Add(time.Hour), MinToGo: &min}); err != nil {
		t.Fatal(err)
	}
	applied, err := s.mergePlaces(ctx, canonical, protected, false)
	if err != nil || applied {
		t.Fatal("dependent venue destroyed", applied, err)
	}
	parent := testdb.Place(t, r.DB, "Sports fixture complex", "osm")
	child := testdb.Place(t, r.DB, "", "osm")
	if _, err = r.DB.Exec(ctx, `UPDATE places_place SET raw_tags=CASE WHEN id=$1 THEN '{"leisure":"sports_centre"}'::jsonb ELSE '{"leisure":"pitch","sport":"basketball"}'::jsonb END WHERE id IN($1,$2)`, parent, child); err != nil {
		t.Fatal(err)
	}
	out, err = r.Run(ctx, "aggregate_unnamed_places", args(map[string]any{"dry_run": true}))
	if err != nil || out.(map[string]int)["would_merge"] != 1 {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "aggregate_unnamed_places", nil)
	if err != nil || out.(map[string]int)["merged"] != 1 {
		t.Fatal(out, err)
	}
}
func TestNativeManualICSBookingAndHours(t *testing.T) {
	r, _ := commandFixture(t)
	ctx := context.Background()
	place := testdb.Place(t, r.DB, "Calendar fixture", "osm")
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET website='https://fixture.local/book',opening_hours=NULL WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(ctx, "seed_booking_links", nil)
	if err != nil || out.(map[string]int64)["created"] != 1 {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "seed_booking_links", nil)
	if err != nil || out.(map[string]int64)["created"] != 0 {
		t.Fatal(out, err)
	}
	out, err = r.Run(ctx, "enrich_places", nil)
	if err != nil || out.(map[string]int)["hours_updated"] != 1 {
		t.Fatal(out, err)
	}
	r.Config.FetchFeed = func(context.Context, string) ([]byte, error) {
		return []byte(fmt.Sprintf("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:fixture-only\nSUMMARY:Reading fixture\nDTSTART:%s\nEND:VEVENT\nEND:VCALENDAR\n", time.Now().Add(24*time.Hour).UTC().Format("20060102T150405Z"))), nil
	}
	for i := 0; i < 2; i++ {
		out, err = r.Run(ctx, "ingest_events", args(map[string]any{"ics_url": "https://fixture.local/calendar", "place": place}))
		if err != nil || out.(map[string]int)["imported"] != 1 {
			t.Fatal(out, err)
		}
	}
	var count int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM events_event WHERE external_id='fixture-only' AND source='ical'`).Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}
