package export

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRetirementPostgresSnapshotPreservesLargePrimaryForeignKeysAndPaths(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *exportDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	const placeID int64 = 9007199254740993
	const eventID int64 = 9007199254740995
	if _, err := db.Exec(ctx, `INSERT INTO places_place(id,name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES($1,'Large-key venue','osm','',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{}','','','Cluj-Napoca','','RO','','{}','','',now(),now(),'Public credit','CC0','https://source.invalid')`, placeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO events_event(id,title,description,starts_at,ends_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,activity_type_id,source_category,source_confidence,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_price_min,source_price_max,source_currency,source_is_free,source_availability) VALUES($1,'Large-key event','Public event',now()+interval '1 day',NULL,'https://source.invalid','manual','','Public credit','CC0','https://source.invalid',now(),now(),$2,NULL,'',NULL,false,'scheduled',false,'','','','','','','Europe/Bucharest',NULL,NULL,'',NULL,'')`, eventID, placeID); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := New(db, "https://site.invalid")
	s.Now = func() time.Time { return time.Now().UTC() }
	if out, err := s.Snapshot(ctx, dir); err != nil || out.Events != 1 || out.Places != 1 {
		t.Fatal("large-key public snapshot failed", err)
	}
	load := func(name string) map[string]any {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		var v map[string]any
		if err := d.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	place := load("places.json")["records"].([]any)[0].(map[string]any)
	event := load("events.json")["records"].([]any)[0].(map[string]any)
	if place["id"] != json.Number("9007199254740993") || place["path"] != "/places/9007199254740993/large-key-venue/" {
		t.Fatal("snapshot rounded place primary key/path")
	}
	if event["id"] != json.Number("9007199254740995") || event["place_id"] != json.Number("9007199254740993") || event["path"] != "/events/9007199254740995/large-key-event/" || event["place_summary"].(map[string]any)["id"] != json.Number("9007199254740993") {
		t.Fatal("snapshot rounded event primary/foreign key/path")
	}
	manifest := load("manifest.json")
	for _, value := range manifest["datasets"].(map[string]any) {
		item := value.(map[string]any)
		raw, err := os.ReadFile(filepath.Join(dir, item["file"].(string)))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if item["sha256"] != hex.EncodeToString(sum[:]) {
			t.Fatal("large-key snapshot digest changed published bytes")
		}
	}
	// The fixture uses the real FK graph; a fabricated venue reference must fail
	// even though a complete schema/data copy is adopted without ORM execution.
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `UPDATE events_event SET place_id=9223372036854775807 WHERE id=$1`, eventID)
	if err == nil {
		err = tx.Commit(ctx)
	}
	if err == nil {
		t.Fatal("snapshot fixture did not retain actual venue FK integrity")
	}
}
