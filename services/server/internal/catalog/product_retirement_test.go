package catalog

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestRetirementOpeningHoursGrammarMatrix(t *testing.T) {
	for _, c := range []struct {
		name, raw string
		expected  map[string][][2]int
	}{
		{"weekday_range", "Mo-Fr 09:00-18:00", map[string][][2]int{"mo": {{540, 1080}}, "fr": {{540, 1080}}, "sa": {}}},
		{"always", "24/7", map[string][][2]int{"mo": {{0, 1440}}, "su": {{0, 1440}}}},
		{"comma_days", "Mo,We,Fr 10:00-20:00", map[string][][2]int{"mo": {{600, 1200}}, "we": {{600, 1200}}, "tu": {}}},
		{"split_day", "Mo-Fr 09:00-12:00,13:00-17:00", map[string][][2]int{"mo": {{540, 720}, {780, 1020}}}},
		{"separate_rules", "Mo-Fr 09:00-18:00; Sa 10:00-14:00; Su off", map[string][][2]int{"fr": {{540, 1080}}, "sa": {{600, 840}}, "su": {}}},
		{"midnight_split", "Fr 20:00-02:00", map[string][][2]int{"fr": {{1200, 1440}}, "sa": {{0, 120}}}},
		{"week_wrap", "Su 20:00-02:00", map[string][][2]int{"su": {{1200, 1440}}, "mo": {{0, 120}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := ParseOpeningHours(c.raw)
			if got == nil || len(got) != 7 {
				t.Fatal("common schedule not parsed")
			}
			for day, want := range c.expected {
				if !reflect.DeepEqual(got[day], want) {
					t.Fatalf("%s intervals got%v want%v", day, got[day], want)
				}
			}
		})
	}
	for _, raw := range []string{"", "  ", "garbage", "Mo", "Mo 9-5pm", "Xx 09:00-10:00", "Mo 24:01-25:00"} {
		t.Run("unknown_"+raw, func(t *testing.T) {
			if ParseOpeningHours(raw) != nil {
				t.Fatal("unparseable hours guessed a schedule")
			}
		})
	}
}

func TestRetirementPostgresTaxonomyPublicAPIAndForeignKeys(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	for _, base := range []string{"/api", "/api/v1"} {
		t.Run(base, func(t *testing.T) {
			out := request(t, s, base+"/taxonomy/activities/basketball/", nil)
			if out.Code != 200 {
				t.Fatal("public type detail unavailable", out.Code)
			}
			var typ map[string]any
			if err := json.Unmarshal(out.Body.Bytes(), &typ); err != nil {
				t.Fatal(err)
			}
			if typ["slug"] != "basketball" || typ["wellness"] != true || typ["family_friendly"] != true || typ["category"] != "team_sport" {
				t.Fatal("public seeded type traits or category lost")
			}
			out = request(t, s, base+"/taxonomy/categories/", nil)
			if out.Code != 200 {
				t.Fatal("public taxonomy categories unavailable")
			}
			var categories struct {
				Results []map[string]any `json:"results"`
			}
			if err := json.Unmarshal(out.Body.Bytes(), &categories); err != nil || len(categories.Results) == 0 {
				t.Fatal("public category projection empty", err)
			}
			if request(t, s, base+"/taxonomy/activities/missing-retirement-type/", nil).Code != 404 {
				t.Fatal("missing taxonomy type did not return404")
			}
		})
	}
	// Exercise PostgreSQL itself, rather than importing ORM model definitions.
	for _, c := range []struct{ name, query string }{
		{"category_self_parent", `UPDATE taxonomy_activitycategory SET parent_id=id WHERE slug='sport'`},
		{"type_self_parent", `UPDATE taxonomy_activitytype SET parent_id=id WHERE slug='basketball'`},
		{"dangling_category", `UPDATE taxonomy_activitytype SET category_id=9223372036854775807 WHERE slug='basketball'`},
		{"duplicate_slug", `INSERT INTO taxonomy_activitycategory(slug,name,description,parent_id,created_at,updated_at) VALUES('sport','Duplicate','',NULL,now(),now())`},
	} {
		t.Run(c.name, func(t *testing.T) {
			tx, err := s.DB.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			_, err = tx.Exec(ctx, c.query)
			if err == nil {
				err = tx.Commit(ctx)
			}
			if err == nil {
				t.Fatal("real taxonomy PK/FK/cycle constraint accepted invalid state")
			}
		})
	}
	var ann bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE schemaname=current_schema() AND tablename='recommendations_activityembedding' AND indexdef ILIKE '%USING hnsw%' AND indexdef LIKE '%vector_cosine_ops%')`).Scan(&ann); err != nil || !ann {
		t.Fatal("native baseline omitted the cosine HNSW index", err)
	}
}

func TestRetirementOpeningHoursExclusiveCloseAndUnknown(t *testing.T) {
	schedule := ParseOpeningHours("Mo-Fr 09:00-18:00")
	for _, c := range []struct {
		name string
		when time.Time
		open bool
	}{
		{"inside", time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC), true},
		{"before", time.Date(2024, 1, 1, 8, 0, 0, 0, time.UTC), false},
		{"opening_inclusive", time.Date(2024, 1, 1, 9, 0, 0, 0, time.UTC), true},
		{"closing_exclusive", time.Date(2024, 1, 1, 18, 0, 0, 0, time.UTC), false},
		{"weekend_closed", time.Date(2024, 1, 6, 10, 0, 0, 0, time.UTC), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := OpenAt(schedule, c.when)
			if got == nil || *got != c.open {
				t.Fatal("opening boundary changed")
			}
		})
	}
	if OpenAt(nil, time.Now()) != nil {
		t.Fatal("unknown schedule became open or closed")
	}
}

func TestRetirementPostgresTaxonomyReferenceTraitsRelationsAndMappingClosure(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	for _, slug := range []string{"outdoor", "fitness", "culture"} {
		var exists bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitycategory WHERE slug=$1)`, slug).Scan(&exists); err != nil || !exists {
			t.Fatal("required seeded category absent", slug, err)
		}
	}
	for _, c := range []struct {
		slug             string
		wellness, family bool
	}{{"running", true, false}, {"hiking", true, true}, {"festival", false, true}, {"basketball", true, true}} {
		var wellness, family bool
		if err := s.DB.QueryRow(ctx, `SELECT wellness,family_friendly FROM taxonomy_activitytype WHERE slug=$1`, c.slug).Scan(&wellness, &family); err != nil || wellness != c.wellness || family != c.family {
			t.Fatal("seeded taxonomy traits changed", c.slug, err)
		}
	}
	for _, slug := range []string{"marathon", "cycling", "city_day", "archive", "used_bookshop"} {
		var exists bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE slug=$1 AND is_active)`, slug).Scan(&exists); err != nil || !exists {
			t.Fatal("required activity seed absent", slug, err)
		}
	}
	var related, romanian bool
	if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activityrelation r JOIN taxonomy_activitytype a ON a.id=r.source_id JOIN taxonomy_activitytype b ON b.id=r.target_id WHERE a.slug='marathon' AND b.slug='running'),EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE slug='football' AND aliases ? 'fotbal')`).Scan(&related, &romanian); err != nil || !related || !romanian {
		t.Fatal("relation/Romanian alias seed lost", err)
	}
}
