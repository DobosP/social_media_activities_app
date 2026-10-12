package commands

import (
	"context"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// ADR-0058: dedup_places never folds two different RO-EDU venue ids into one
// place, even at one location with one name; an RO-EDU venue still merges into
// a nearby place no other venue has claimed.
func TestNativeDedupKeepsDistinctRoeduVenues(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	place := func(name, source, external, tags string, lon, lat float64) int64 {
		id := testdb.Place(t, r.DB, name, source)
		if _, err := r.DB.Exec(ctx, `UPDATE places_place SET external_id=$2,raw_tags=$3::jsonb,location=ST_SetSRID(ST_MakePoint($4,$5),4326) WHERE id=$1`, id, external, tags, lon, lat); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// One building, three RO-EDU venue ids: two RO-EDU places with one name,
	// and an OSM place already claimed by a third venue.
	theatre := place("Teatrul Național Cluj", "roedu", "venue-a", `{"amenity":"theatre"}`, 23.5949, 46.7712)
	hall := place("Teatrul Național Cluj", "roedu", "venue-b", `{"amenity":"theatre"}`, 23.5949, 46.7712)
	claimed := place("Teatrul Național Cluj", "osm", "", `{"amenity":"theatre","merged_sources":[{"source":"roedu","external_id":"venue-c"}]}`, 23.5949, 46.7712)
	// Elsewhere: an unclaimed OSM library and an RO-EDU copy of it.
	library := place("Biblioteca Centrală", "osm", "", `{"amenity":"library"}`, 23.61, 46.78)
	roeduCopy := place("Biblioteca Centrala", "roedu", "venue-d", `{"amenity":"library"}`, 23.61, 46.78)
	out, err := r.Run(ctx, "dedup_places", args(map[string]any{"apply": true}))
	if err != nil || out.(map[string]int)["merged"] != 1 || out.(map[string]int)["pairs"] != 1 {
		t.Fatal(out, err)
	}
	for _, id := range []int64{theatre, hall, claimed, library} {
		var exists bool
		if err = r.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_place WHERE id=$1)`, id).Scan(&exists); err != nil || !exists {
			t.Fatal("distinct RO-EDU venue place merged away", id, err)
		}
	}
	var venues []string
	if err = r.DB.QueryRow(ctx, `SELECT `+jobs.RoeduVenueIdentitiesSQL("p")+` FROM places_place p WHERE id=$1`, library).Scan(&venues); err != nil || len(venues) != 1 || venues[0] != "venue-d" {
		t.Fatal("unclaimed place did not absorb its one RO-EDU venue", roeduCopy, venues, err)
	}
	// The locked guard refuses a direct merge as well, in either direction.
	for _, pair := range [][2]int64{{claimed, theatre}, {theatre, hall}, {library, hall}} {
		applied, err := s.mergePlaces(ctx, pair[0], pair[1], false)
		if err != nil || applied {
			t.Fatal("merge folded two RO-EDU venues", pair, applied, err)
		}
	}
}
