package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
)

func TestOperatorCase2OvertureRawParquetInvalidRowsBBoxAndLimit(t *testing.T) {
	row := fixtureOvertureRow("08f1234", "Central Library", 23.59, 46.77)
	row.Websites = []string{"https://lib.example.ro"}
	raw, valid := overtureRaw(row)
	if !valid || raw.Source != "overture" || raw.ExternalID != "08f1234" || math.Abs(raw.Lon-23.59) > 1e-9 || raw.Address["city"] != "Cluj-Napoca" || raw.Tags["overture:category"] != "library" || raw.Tags["overture:website"] != "https://lib.example.ro" {
		t.Fatal("raw Overture normalization differs")
	}
	blank := row
	blank.Names = nil
	if _, ok := overtureRaw(blank); ok {
		t.Fatal("missing name accepted")
	}
	blank = fixtureOvertureRow("empty-name", "", 23.59, 46.77)
	if _, ok := overtureRaw(blank); ok {
		t.Fatal("blank name accepted")
	}
	missing := row
	missing.BBox = nil
	if _, ok := overtureRaw(missing); ok {
		t.Fatal("missing coordinates accepted")
	}
	path := filepath.Join(t.TempDir(), "case.parquet")
	rows := []overtureRow{}
	for i := 0; i < 5; i++ {
		rows = append(rows, fixtureOvertureRow(string(rune('0'+i)), "Place", 23.5, 46.7))
	}
	if err := os.WriteFile(path, parquetFixture(t, rows), 0600); err != nil {
		t.Fatal(err)
	}
	source := overtureSource{DefaultPath: path}
	if err := source.Fetch(context.Background(), commands.PlaceOptions{Source: "overture"}, func(commands.RawPlace) error { return nil }); err == nil {
		t.Fatal("missing bounding box accepted")
	}
	found := []commands.RawPlace{}
	if err := source.Fetch(context.Background(), commands.PlaceOptions{Source: "overture", BBox: "23,46,24,47", Limit: 3}, func(p commands.RawPlace) error { found = append(found, p); return nil }); err != nil || len(found) != 3 {
		t.Fatal("parquet source limit differs")
	}
	for _, p := range found {
		if p.Source != "overture" {
			t.Fatal("source identity lost")
		}
	}
}
