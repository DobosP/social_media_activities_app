package export

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var exportDSN = flag.String("export-test-dsn", "", "Explicit disposable PostgreSQL fixture server")

func TestSlugAndUTCSourceContract(t *testing.T) {
	if Slug("Cât de român ești?", "event") != "cat-de-roman-esti" || Slug("😀", "event") != "event" {
		t.Fatal("canonical Django slug parity")
	}
	if isoZ("2026-10-01T12:00:00+03:00") != "2026-10-01T09:00:00Z" {
		t.Fatal("UTC not normalized")
	}
}
func TestPostgresSnapshotProducerPrivacyAndIntegrity(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *exportDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	owner := testdb.Actor(t, db, "export-owner", "adult")
	minor := testdb.Actor(t, db, "export-minor", "child")
	place := testdb.Place(t, db, "Crowd venue", "osm")
	testdb.Place(t, db, "Private pending venue", "user")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	for _, a := range []platform.Actor{owner, minor} {
		id, err := soc.CreateActivity(ctx, a, social.ActivityInput{Place: place, ActivityType: typ, Title: "Public display", Description: "Never export private description", StartsAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE social_activity SET is_publicly_listed=true WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	directory := t.TempDir()
	s := New(db, "https://example.invalid")
	summary, err := s.Snapshot(ctx, directory)
	if err != nil || summary.Places != 1 || summary.Activities != 1 || summary.Truncated {
		t.Fatal(summary, err)
	}
	raw, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Schema    int    `json:"schema_version"`
		Generated string `json:"generated_at"`
		Datasets  map[string]struct {
			File   string
			Count  int
			SHA256 string
		}
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != 2 || !strings.HasSuffix(manifest.Generated, "Z") {
		t.Fatal(manifest)
	}
	for name, item := range manifest.Datasets {
		data, err := os.ReadFile(filepath.Join(directory, item.File))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != item.SHA256 {
			t.Fatal("untrusted digest", name)
		}
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["generated_at"] != manifest.Generated {
			t.Fatal("mixed generation")
		}
		if strings.Contains(string(data), "Never export private") || strings.Contains(string(data), "Private pending") || strings.Contains(string(data), "raw_tags") || strings.Contains(string(data), "owner_id") {
			t.Fatal("privacy allowlist failed", name)
		}
		if name == "activities" {
			records := payload["records"].([]any)
			record := records[0].(map[string]any)
			if len(record) != 7 || record["cohort"] != "adult" {
				t.Fatal(record)
			}
		}
		if name == "taxonomy" && item.Count != len(payload["categories"].([]any))+len(payload["activity_types"].([]any)) {
			t.Fatal("taxonomy count")
		}
	}
}
