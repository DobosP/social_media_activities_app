package web

import (
	"context"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestWebCasePortKidFactsAreSoftAndUseTheSourceSubset(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	read := func(t *testing.T, place int64, want bool) {
		t.Helper()
		path := fmt.Sprintf("/places/%d/", place)
		html := webCasePortHTML(t, mux, actor, path)
		r := socialLegacyRequest("GET", path, actor, place, nil)
		data, _, handled, err := s.PublicView(r, actor, "place_detail")
		if err != nil || !handled || data["has_kid_facts"] != want {
			t.Fatal("kid fact context differs from source subset", err, data["has_kid_facts"], want)
		}
		if want {
			webCasePortContains(t, html, "kid-friendly facilities")
		} else {
			webCasePortAbsent(t, html, "kid-friendly facilities")
		}
	}
	t.Run("unknown_venue_is_public_without_badge", func(t *testing.T) {
		place := testdb.Place(t, s.DB, "Unknown source facts venue", "osm")
		read(t, place, false)
	})
	for _, test := range []struct {
		name, tags string
		want       bool
	}{
		{"map_toilets_is_a_kid_fact", `{"toilets":"yes"}`, true},
		{"map_drinking_water_is_a_kid_fact", `{"drinking_water":"yes"}`, true},
		{"map_playground_is_a_kid_fact", `{"leisure":"playground"}`, true},
		{"changing_table_is_not_in_kid_subset", `{"changing_table":"yes"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			place := testdb.Place(t, s.DB, "Source facts venue "+test.name, "osm")
			if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags=$2::jsonb WHERE id=$1`, place, test.tags); err != nil {
				t.Fatal(err)
			}
			read(t, place, test.want)
		})
	}
	t.Run("crowd_fenced_quorum_lights_badge", func(t *testing.T) {
		place := testdb.Place(t, s.DB, "Crowd fenced source venue", "osm")
		read(t, place, false)
		for i := 0; i < 3; i++ {
			voter := testdb.Actor(t, s.DB, fmt.Sprintf("kid-case-fenced-voter-%d", i), "adult")
			if err := s.Catalog.VoteFact(ctx, voter, place, "fenced", true); err != nil {
				t.Fatal(err)
			}
		}
		read(t, place, true)
	})
	t.Run("crowd_transit_quorum_is_not_a_kid_fact", func(t *testing.T) {
		place := testdb.Place(t, s.DB, "Crowd transit source venue", "osm")
		for i := 0; i < 3; i++ {
			voter := testdb.Actor(t, s.DB, fmt.Sprintf("kid-case-transit-voter-%d", i), "adult")
			if err := s.Catalog.VoteFact(ctx, voter, place, "bus_tram_nearby", true); err != nil {
				t.Fatal(err)
			}
		}
		facts, err := s.Catalog.VenueFacts(ctx, actor, place, false)
		if err != nil {
			t.Fatal(err)
		}
		confirmed := false
		for _, fact := range facts {
			if fact["key"] == "bus_tram_nearby" && fact["state"] == "true" {
				confirmed = true
			}
		}
		if !confirmed {
			t.Fatal("transit quorum fixture did not actually confirm the fact")
		}
		read(t, place, false)
	})
}
