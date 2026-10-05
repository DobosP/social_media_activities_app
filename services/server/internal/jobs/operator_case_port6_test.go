package jobs

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestOperatorCase6RunnerAtomicExactCompactUTF8AndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "replace", true: "failed_replace"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			final := filepath.Join(dir, "places.json")
			old := []byte("previous generation")
			if err := os.WriteFile(final, old, 0600); err != nil {
				t.Fatal(err)
			}
			payload := struct {
				Name  string `json:"name"`
				Line  string `json:"line"`
				Count int    `json:"count"`
			}{"Bibliotecă", "a\nb", 1}
			expected := []byte(`{"name":"Bibliotecă","line":"a\nb","count":1}`)
			calls := 0
			digest, err := atomicJSONWithReplace(dir, "places.json", payload, func(source, target string) error {
				calls++
				prior, e := os.ReadFile(final)
				if e != nil || !bytes.Equal(prior, old) {
					t.Fatal("runner published before atomic replace")
				}
				staged, e := os.ReadFile(source)
				if e != nil || !bytes.Equal(staged, expected) || target != final {
					t.Fatal("runner temporary bytes are not exact compact UTF8")
				}
				if fail {
					return errors.New("synthetic replacement failure")
				}
				return os.Rename(source, target)
			})
			actual, e := os.ReadFile(final)
			if e != nil {
				t.Fatal(e)
			}
			if calls != 1 {
				t.Fatal("runner replaced more than once")
			}
			if fail {
				if err == nil || digest != "" || !bytes.Equal(actual, old) {
					t.Fatal("runner failed replace changed prior generation/reported success")
				}
			} else {
				sum := sha256.Sum256(expected)
				if err != nil || digest != hex.EncodeToString(sum[:]) || !bytes.Equal(actual, expected) {
					t.Fatal("runner digest differs from exact published bytes")
				}
			}
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != 1 || entries[0].Name() != "places.json" {
				t.Fatal("runner left a staged artifact")
			}
		})
	}
}

func TestOperatorCase6ExportCommandDisabledDoesNotWrite(t *testing.T) {
	r := New(nil, DefaultConfig())
	dir := t.TempDir()
	out, err := r.Run(context.Background(), "export_agent_snapshot", nil)
	if err != nil || out.(map[string]bool)["disabled"] != true {
		t.Fatal("unset exporter opt-in did not report disabled", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("disabled command wrote files")
	}
}

func TestPostgresOperatorCase6ExportCommandFilesAndExactLicensePairs(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	licensed := testdb.Place(t, r.DB, "Licensed venue", "osm")
	testdb.Place(t, r.DB, "Bare venue", "osm")
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET attribution='© OpenStreetMap contributors',license_name='ODbL',provenance_url='https://www.openstreetmap.org/' WHERE id=$1`, licensed); err != nil {
		t.Fatal(err)
	}
	r.Config.AgentSnapshotDir = t.TempDir()
	out, err := r.Run(ctx, "export_agent_snapshot", nil)
	if err != nil || out.(map[string]any)["places"] != 2 {
		t.Fatal("enabled actual export command failed", out, err)
	}
	for _, name := range []string{"events.json", "places.json", "activities.json", "taxonomy.json", "manifest.json"} {
		if _, err := os.Stat(filepath.Join(r.Config.AgentSnapshotDir, name)); err != nil {
			t.Fatal("enabled exporter omitted dataset", name, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(r.Config.AgentSnapshotDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Licenses []map[string]string `json:"licenses"`
	}
	if json.Unmarshal(raw, &manifest) != nil || len(manifest.Licenses) != 1 || manifest.Licenses[0]["license_name"] != "ODbL" || manifest.Licenses[0]["attribution"] != "© OpenStreetMap contributors" {
		t.Fatal("source exact licensed pair or blank exclusion failed")
	}
}
