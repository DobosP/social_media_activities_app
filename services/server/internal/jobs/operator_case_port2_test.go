package jobs

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestOperatorCase2ROEDUCultureHeuristicProjectionAndFreshMaps(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  map[string]any
	}{
		{[]string{"Opera Națională Română", "Cluj Opera House", "Sala de operă"}, map[string]any{"amenity": "theatre", "theatre:genre": "opera"}},
		{[]string{"Filarmonica Transilvania", "Sala de Concerte"}, map[string]any{"amenity": "theatre", "theatre:type": "concert"}},
		{[]string{"Teatrul Național", "Hungarian Theatre"}, map[string]any{"amenity": "theatre"}},
		{[]string{"Muzeul de Artă", "National Museum"}, map[string]any{"tourism": "museum"}},
		{[]string{"Galeria de Artă Contemporană", "Quadro Gallery"}, map[string]any{"tourism": "gallery"}},
		{[]string{"Biblioteca Județeană", "Central Library"}, map[string]any{"amenity": "library"}},
		{[]string{"Cinema Florin Piersic", "Festivalul de Film"}, map[string]any{"amenity": "cinema"}},
		{[]string{"Casa de Cultură a Studenților", ""}, map[string]any{"amenity": "arts_centre"}},
	} {
		for _, name := range c.names {
			tags := RoeduVenueTags(map[string]any{"title": name})
			classification := map[string]any{}
			for key, value := range tags {
				if !strings.HasPrefix(key, "roedu:") {
					classification[key] = value
				}
			}
			if !reflect.DeepEqual(classification, c.want) {
				t.Fatal("culture classification or precedence differs", name)
			}
		}
	}
	if tags := RoeduVenueTags(nil); tags["amenity"] != "arts_centre" {
		t.Fatal("absent title default differs")
	}
	first, second := RoeduVenueTags(map[string]any{"title": "Teatru"}), RoeduVenueTags(map[string]any{"title": "Teatru"})
	first["amenity"] = "MUTATED"
	if second["amenity"] != "theatre" {
		t.Fatal("caller mutation changed later tags")
	}
}

func TestPostgresOperatorCase2AllNativeMappingSlugsResolve(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	known := map[string]bool{}
	for _, rule := range placeRules {
		if known[rule.Slug] {
			continue
		}
		known[rule.Slug] = true
		var id int64
		if err := r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug=$1`, rule.Slug).Scan(&id); err != nil || id < 1 {
			t.Fatal("mapping slug missing from native seeded taxonomy", rule.Slug)
		}
	}
	if len(known) < 20 {
		t.Fatal("mapping closure accidentally inspected a trivial subset")
	}
}
