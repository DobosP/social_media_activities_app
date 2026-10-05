package social

import (
	"context"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestCasePortChildVenueSourceAllowlistAndRationale(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, c := range []struct {
		name, source, tags string
		want               bool
	}{
		{"osm_library", "osm", `{"amenity":"library"}`, true},
		{"osm_park", "osm", `{"leisure":"park"}`, true},
		{"osm_bar", "osm", `{"amenity":"bar"}`, false},
		{"osm_untagged", "osm", `{}`, false},
		{"user_untagged", "user", `{}`, false},
		{"overture_library", "overture", `{"overture:category":"library"}`, true},
		{"overture_alternate", "overture", `{"overture:category":"cafe","overture:alternate":["park"]}`, true},
		{"overture_nightclub", "overture", `{"overture:category":"nightclub"}`, false},
		{"google_unknown", "google", `{"x":"y"}`, false},
		{"roedu_culture", "roedu", `{"amenity":"theatre"}`, false},
		{"malformed_alternate", "overture", `{"overture:category":"nightclub","overture:alternate":"park"}`, false},
		{"malformed_tags", "osm", `[]`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			id := testdb.Place(t, s.DB, c.name, c.source)
			if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags=$2::jsonb WHERE id=$1`, id, c.tags); err != nil {
				t.Fatal(err)
			}
			got, err := s.ChildVenueSafe(ctx, id)
			if err != nil || got != c.want {
				t.Fatal("child venue source gate", got, c.want, err)
			}
			why, err := s.ChildVenueRationale(ctx, s.DB, id)
			wantWhy := ""
			if c.want {
				wantWhy = "rule_match"
			}
			if err != nil || why != wantWhy {
				t.Fatal("honest rationale", why, wantWhy, err)
			}
		})
	}
	for _, source := range []string{"osm", "roedu"} {
		t.Run("staff_override_"+source, func(t *testing.T) {
			id := testdb.Place(t, s.DB, "Staff override "+source, source)
			if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags='{"amenity":"bar"}' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			if got, err := s.ChildVenueSafe(ctx, id); err != nil || got {
				t.Fatal("unknown allowed before approval", got, err)
			}
			if _, err := s.DB.Exec(ctx, `INSERT INTO places_approvedchildvenue(place_id,approved_by_id,note,created_at) VALUES($1,NULL,'Synthetic review',now())`, id); err != nil {
				t.Fatal(err)
			}
			if got, err := s.ChildVenueSafe(ctx, id); err != nil || !got {
				t.Fatal("exact approval missing", got, err)
			}
			if why, err := s.ChildVenueRationale(ctx, s.DB, id); err != nil || why != "staff_verified" {
				t.Fatal("staff rationale", why, err)
			}
		})
	}
	for _, id := range []int64{0, -1, 9223372036854775807} {
		if safe, err := s.ChildVenueSafe(ctx, id); err != nil || safe {
			t.Fatal("absent place allowed", id, safe, err)
		}
	}
	t.Run("inactive_rule", func(t *testing.T) {
		id := testdb.Place(t, s.DB, "Inactive library", "osm")
		if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags='{"amenity":"library"}' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(ctx, `UPDATE places_childvenueclass SET is_active=false WHERE key='library'`); err != nil {
			t.Fatal(err)
		}
		if safe, err := s.ChildVenueSafe(ctx, id); err != nil || safe {
			t.Fatal("inactive class remained allowed", safe, err)
		}
		if why, err := s.ChildVenueRationale(ctx, s.DB, id); err != nil || why != "" {
			t.Fatal("inactive rationale", why, err)
		}
	})
	t.Run("empty_rule", func(t *testing.T) {
		if _, err := s.DB.Exec(ctx, `INSERT INTO places_childvenueclass(key,label,osm_match,overture_categories,is_active,created_at) VALUES('bad','Bad','{}','[]',true,now())`); err != nil {
			t.Fatal(err)
		}
		id := testdb.Place(t, s.DB, "Empty-rule unknown", "osm")
		if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags='{"amenity":"bar"}' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if safe, err := s.ChildVenueSafe(ctx, id); err != nil || safe {
			t.Fatal("empty criterion blanket allowed", safe, err)
		}
	})
}
