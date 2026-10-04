package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRetirementSnapshotDisabledDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	r := New(nil, DefaultConfig())
	out, err := r.ExportAgentSnapshot(context.Background(), nil)
	if err != nil || out.(map[string]bool)["disabled"] != true {
		t.Fatal("unset snapshot opt-in not honored", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("disabled export wrote files", err)
	}
}

func TestRetirementSnapshotAtomicUTF8DigestAndFailurePreservesPrevious(t *testing.T) {
	dir := t.TempDir()
	old := []byte("previous generation")
	path := filepath.Join(dir, "places.json")
	if err := os.WriteFile(path, old, 0600); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"name": "Bibliotecă românească", "line": "a\nb", "count": 1}
	digest, err := atomicJSON(dir, "places.json", payload)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if digest != hex.EncodeToString(sum[:]) || !strings.Contains(string(raw), "Bibliotecă românească") || !strings.HasSuffix(string(raw), "\n") {
		t.Fatal("digest did not bind exact UTF8 published bytes")
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil || got["name"] != payload["name"] || got["line"] != payload["line"] {
		t.Fatal("atomic JSON values changed", err)
	}
	if _, err := atomicJSON(dir, "places.json", map[string]any{"invalid": make(chan int)}); err == nil {
		t.Fatal("invalid JSON payload accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || !reflect.DeepEqual(after, raw) {
		t.Fatal("failed atomic write damaged previous generation", err)
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 || files[0].Name() != "places.json" {
		t.Fatal("atomic writer left a temporary artifact", err)
	}
}

func TestRetirementPostgresAgentSnapshotPublicWorld(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *jobsDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	cfg := DefaultConfig()
	cfg.Social = social.New(db, platform.RecordAudit)
	r := New(db, cfg)
	owner := testdb.Actor(t, db, "snapshot-retirement-owner", "adult")
	place := testdb.Place(t, db, "Bibliotecă românească", "osm")
	pending := testdb.Place(t, db, "Private pending snapshot venue", "user")
	if _, err := db.Exec(ctx, `UPDATE places_place SET raw_tags='{"secret":"snapshot-private-marker","amenity":"library"}',website='https://venue.invalid',attribution='© OpenStreetMap contributors',license_name='ODbL',provenance_url='https://www.openstreetmap.org/' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO places_approvedchildvenue(place_id,approved_by_id,note,created_at) VALUES($1,NULL,'Isolated snapshot test venue',now())`, place); err != nil {
		t.Fatal(err)
	}
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	for _, cohort := range []string{"adult", "teen", "child"} {
		actor := owner
		if cohort != "adult" {
			actor = testdb.Actor(t, db, "snapshot-retirement-"+cohort, cohort)
		}
		id, err := r.Config.Social.CreateActivity(ctx, actor, social.ActivityInput{Place: place, ActivityType: typ, Title: "Snapshot " + cohort, Description: "Never export this private activity prose", StartsAt: time.Now().Add(24 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=true WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Config.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Unlisted adult", StartsAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	event := func(title, status string, p *int64, at *int64, when time.Time) int64 {
		var id int64
		if err := db.QueryRow(ctx, `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,activity_type_id,source_category,source_confidence,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_price_min,source_price_max,source_currency,source_is_free,source_availability) VALUES($1,'Public event prose',$2,$2::timestamptz+interval '1 hour','https://source.invalid','roedu','','Public source credit','CC-BY-4.0','https://source.invalid/facts',now(),now(),$3,$4,'concert',0.98,false,$5,false,'','','','','','FREQ=WEEKLY','Europe/Bucharest',20.00,50.00,'RON',false,'limited') RETURNING id`, title, when, p, at, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now()
	event("Public typed event", "rescheduled", &place, &typ, now.Add(24*time.Hour))
	event("Public untyped event", "scheduled", nil, nil, now.Add(24*time.Hour))
	event("Past event", "scheduled", &place, &typ, now.Add(-24*time.Hour))
	event("At private pending venue", "scheduled", &pending, &typ, now.Add(24*time.Hour))
	for _, status := range []string{"cancelled", "postponed", "removed", "moved_online"} {
		event("Hidden "+status, status, &place, &typ, now.Add(24*time.Hour))
	}
	for _, column := range []string{"is_tombstone", "is_import_held"} {
		id := event("Hidden "+column, "scheduled", &place, &typ, now.Add(24*time.Hour))
		if _, err := db.Exec(ctx, "UPDATE events_event SET "+column+"=true WHERE id=$1", id); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	r.Config.AgentSnapshotDir = dir
	r.Config.SiteBaseURL = "https://site.invalid"
	if _, err := r.ExportAgentSnapshot(ctx, nil); err != nil {
		t.Fatal(err)
	}
	load := func(name string) map[string]any {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var obj map[string]any
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		return obj
	}
	activities, events, places, tax, manifest := load("activities.json"), load("events.json"), load("places.json"), load("taxonomy.json"), load("manifest.json")
	t.Run("adult_optin_exact_shape", func(t *testing.T) {
		rows := activities["records"].([]any)
		if len(rows) != 1 {
			t.Fatal("minor/unlisted activity exported")
		}
		a := rows[0].(map[string]any)
		keys := []string{}
		for key := range a {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if !reflect.DeepEqual(keys, []string{"activity_type", "cohort", "id", "place_id", "starts_at", "status", "title"}) || a["cohort"] != "adult" || a["activity_type"] != "basketball" {
			t.Fatal("public activity field/cohort cap changed")
		}
	})
	t.Run("event_visibility_type_and_source_facts", func(t *testing.T) {
		rows := events["records"].([]any)
		if len(rows) != 2 {
			t.Fatal("past/held/tombstone/nonlive/private-venue event exported")
		}
		for _, raw := range rows {
			e := raw.(map[string]any)
			if e["title"] == "Public untyped event" {
				if e["activity"] != nil {
					t.Fatal("untyped source event gained activity")
				}
				continue
			}
			for key, want := range map[string]any{"activity": "basketball", "source_category": "concert", "lifecycle_status": "rescheduled", "source_confidence": 0.98, "source_recurrence": "FREQ=WEEKLY", "source_timezone": "Europe/Bucharest", "source_price_min": "20.00", "source_price_max": "50.00", "source_currency": "RON", "source_is_free": false, "source_availability": "limited"} {
				if e[key] != want {
					t.Fatal("safe source fact changed", key)
				}
			}
		}
	})
	t.Run("venue_minimization_licenses_and_taxonomy", func(t *testing.T) {
		rows := places["records"].([]any)
		if len(rows) != 1 {
			t.Fatal("pending venue exported")
		}
		p := rows[0].(map[string]any)
		raw, _ := json.Marshal(p)
		if strings.Contains(string(raw), "raw_tags") || strings.Contains(string(raw), "snapshot-private-marker") || p["website"] != "https://venue.invalid" || !strings.HasPrefix(p["path"].(string), "/places/") {
			t.Fatal("public place minimization lost")
		}
		licenses := manifest["licenses"].([]any)
		found := false
		for _, entry := range licenses {
			v := entry.(map[string]any)
			if v["license_name"] == "" {
				t.Fatal("blank license credit")
			}
			found = found || v["license_name"] == "ODbL" && v["attribution"] == "© OpenStreetMap contributors"
		}
		if !found {
			t.Fatal("source license/attribution lost")
		}
		if tax["records"] != nil || tax["schema_version"] != float64(2) {
			t.Fatal("taxonomy envelope changed")
		}
		foundType := false
		for _, raw := range tax["activity_types"].([]any) {
			row := raw.(map[string]any)
			if row["slug"] == "basketball" {
				foundType = true
				keys := []string{}
				for key := range row {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				if !reflect.DeepEqual(keys, []string{"category", "family_friendly", "name", "parent", "slug", "wellness"}) || row["wellness"] != true || row["family_friendly"] != true {
					t.Fatal("taxonomy trait field shape changed")
				}
			}
		}
		if !foundType {
			t.Fatal("seeded type absent from snapshot")
		}
	})
	t.Run("utc_generation_exact_digests_counts_no_temps", func(t *testing.T) {
		generation := manifest["generated_at"].(string)
		parsedGeneration, err := time.Parse(time.RFC3339Nano, generation)
		if err != nil || parsedGeneration.Location() != time.UTC || !strings.HasSuffix(generation, "Z") || manifest["truncated"] != false {
			t.Fatal("generation/truncation contract")
		}
		for _, meta := range manifest["datasets"].(map[string]any) {
			d := meta.(map[string]any)
			if len(d) != 3 {
				t.Fatal("manifest dataset shape")
			}
			raw, err := os.ReadFile(filepath.Join(dir, d["file"].(string)))
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if d["sha256"] != hex.EncodeToString(sum[:]) {
				t.Fatal("manifest did not bind published bytes")
			}
			data := load(d["file"].(string))
			if data["generated_at"] != generation || data["schema_version"] != manifest["schema_version"] {
				t.Fatal("mixed generation/schema")
			}
			if records, ok := data["records"].([]any); ok {
				if d["count"] != float64(len(records)) {
					t.Fatal("dataset count mismatch")
				}
				for _, record := range records {
					row := record.(map[string]any)
					for _, key := range []string{"starts_at", "ends_at"} {
						if value, ok := row[key].(string); ok {
							parsed, err := time.Parse(time.RFC3339Nano, value)
							if err != nil || !strings.HasSuffix(value, "Z") || parsed.Location() != time.UTC {
								t.Fatal("timestamp not UTC RFC3339", key, err)
							}
						}
					}
				}
			} else if d["count"] != float64(len(data["categories"].([]any))+len(data["activity_types"].([]any))) {
				t.Fatal("taxonomy entity count mismatch")
			}
		}
		files, err := os.ReadDir(dir)
		if err != nil || len(files) != 5 {
			t.Fatal("snapshot temporary artifacts remain", err)
		}
	})
}
