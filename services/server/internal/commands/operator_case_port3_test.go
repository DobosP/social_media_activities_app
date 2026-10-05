package commands

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPostgresOperatorCase3OvertureMergePreservesIdentityAndLicenseMetadata(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	if _, err := r.DB.Exec(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Central Library','osm','node',42,'',ST_SetSRID(ST_MakePoint(23.59001,46.77001),4326),'{}','','','Cluj-Napoca','','RO','',NULL,'','',now(),now(),'OSM contributors','ODbL','https://example.invalid/osm')`); err != nil {
		t.Fatal(err)
	}
	s.Config.Sources = map[string]PlaceSource{"overture": operatorCaseSource2{{Source: "overture", ExternalID: "ov-lib", Name: "Central Library", Lon: 23.59, Lat: 46.77, Tags: map[string]any{"overture:category": "library", "overture:alternate": []string{}}, Attribution: "Reviewed Overture source", License: "CDLA-Permissive-2.0", Provenance: "https://example.invalid/overture/ov-lib"}, {Source: "overture", ExternalID: "ov-cafe", Name: "Joc Board Game Cafe", Lon: 23.7, Lat: 46.8, Tags: map[string]any{"overture:category": "board_game_store", "overture:alternate": []string{}, "overture:website": "https://joc.example.ro"}, Website: "https://joc.example.ro"}}}
	if _, err := s.ingestPlaces(ctx, args(map[string]any{"source": "overture", "bbox": "23,46,24,47"})); err != nil {
		t.Fatal(err)
	}
	var separate int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place WHERE source='overture' AND external_id='ov-lib'`).Scan(&separate); err != nil || separate != 0 {
		t.Fatal("Overture library was not folded into nearby OSM place")
	}
	var raw []byte
	if err := r.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE osm_id=42`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var tags struct {
		Merged []struct {
			Source      string `json:"source"`
			External    string `json:"external_id"`
			License     string `json:"license_name"`
			Attribution string `json:"attribution"`
			Provenance  string `json:"provenance_url"`
		} `json:"merged_sources"`
	}
	if json.Unmarshal(raw, &tags) != nil || len(tags.Merged) != 1 || tags.Merged[0].Source != "overture" || tags.Merged[0].External != "ov-lib" || tags.Merged[0].License != "CDLA-Permissive-2.0" || tags.Merged[0].Attribution != "Reviewed Overture source" || tags.Merged[0].Provenance != "https://example.invalid/overture/ov-lib" {
		t.Fatal("merge lost old required identity pair or mandatory license/provenance extension")
	}
	var games int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN places_place p ON p.id=pa.place_id WHERE p.external_id='ov-cafe' AND t.slug='board_games'`).Scan(&games); err != nil || games != 1 {
		t.Fatal("remaining cafe mapping missing")
	}
	if err := r.DB.QueryRow(ctx, `SELECT raw_tags FROM places_place WHERE external_id='ov-cafe'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var cafe map[string]any
	if json.Unmarshal(raw, &cafe) != nil || cafe["overture:website"] != "https://joc.example.ro" {
		t.Fatal("cafe website source tag lost")
	}
}
