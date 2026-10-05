package commands

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
)

const operatorOverpassSample2 = `{"elements":[{"type":"node","id":1,"lat":46.77,"lon":23.59,"tags":{"amenity":"library","name":"Central Library","addr:city":"Cluj-Napoca"}},{"type":"way","id":2,"center":{"lat":46.78,"lon":23.60},"tags":{"leisure":"pitch","sport":"basketball","name":"Court"}},{"type":"node","id":3,"lat":46.76,"lon":23.58,"tags":{"amenity":"bank"}},{"type":"way","id":4,"tags":{"leisure":"pitch","sport":"tennis"}}]}`

func TestOperatorCase2OverpassSelectorsContactsAndCategoryConfidence(t *testing.T) {
	query, err := OverpassQuery("Cluj-Napoca", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{`"park"`, `"library"`, `"fitness_centre"`, `"arts_centre"`} {
		if !strings.Contains(query, needle) {
			t.Fatal("frozen public-place selector missing", needle)
		}
	}
	rows, err := ParseOverpass([]byte(`{"elements":[{"type":"node","id":1,"lat":46.77,"lon":23.6,"tags":{"name":"City Sports Hall","leisure":"sports_hall","website":"https://example.ro/book","phone":"+40 264 000000"}},{"type":"node","id":2,"lat":46,"lon":23,"tags":{"name":"Park Cafe","contact:website":"https://cafe.example.ro"}}]}`))
	if err != nil || len(rows) != 2 || rows[0].Website != "https://example.ro/book" || rows[0].Phone != "+40 264 000000" || rows[1].Website != "https://cafe.example.ro" {
		t.Fatal("website/phone/contact fallback contract differs")
	}
	park := map[string]bool{}
	for _, match := range jobs.MatchPlaceTags(map[string]any{"leisure": "park", "name": "Central Park"}) {
		park[match.Slug] = true
	}
	for _, slug := range []string{"football", "basketball", "chess"} {
		if !park[slug] {
			t.Fatal("park outdoor activity missing")
		}
	}
	library := MatchOverture("library", nil)
	if len(library) != 1 || library[0].Slug != "reading" || library[0].Confidence != .95 {
		t.Fatal("Overture primary confidence differs")
	}
	bySlug := map[string]float64{}
	for _, match := range MatchOverture("recreation_center", []string{"basketball_court"}) {
		bySlug[match.Slug] = match.Confidence
	}
	if math.Abs(bySlug["basketball"]-.63) > 1e-9 || bySlug["football"] != .2 {
		t.Fatal("alternate scaled highest confidence lost")
	}
	if len(MatchOverture("bank", nil)) != 0 || len(MatchOverture("", nil)) != 0 {
		t.Fatal("unknown category invented activity")
	}
	for category, slug := range map[string]string{"gym": "group_fitness", "art_museum": "museum_visit", "hiking_trail": "hiking", "primary_school": "reading"} {
		found := false
		for _, match := range MatchOverture(category, nil) {
			found = found || match.Slug == slug
		}
		if !found {
			t.Fatal("reviewed new Overture category missing")
		}
	}
	school := map[string]bool{}
	for _, match := range jobs.MatchPlaceTags(map[string]any{"amenity": "school", "name": "Liceul Teoretic"}) {
		school[match.Slug] = true
	}
	for _, slug := range []string{"football", "basketball", "running", "reading"} {
		if !school[slug] {
			t.Fatal("school activity mapping missing")
		}
	}
}

func TestPostgresOperatorCase2OSMExactCreateReplayAndProtectedEdges(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	s.Config.FetchOverpass = func(context.Context, string, string) ([]byte, error) { return []byte(operatorOverpassSample2), nil }
	if _, err := s.ingestPlaces(ctx, args(map[string]any{"source": "osm", "city": "Cluj-Napoca"})); err != nil {
		t.Fatal(err)
	}
	var places, edges int
	if err := r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM places_place),(SELECT count(*) FROM places_placeactivity)`).Scan(&places, &edges); err != nil || places != 3 {
		t.Fatal("mapped/unmapped/missing-centroid place count differs")
	}
	var city string
	var edge int64
	var conf float64
	var rule string
	if err := r.DB.QueryRow(ctx, `SELECT address_city FROM places_place WHERE osm_type='node' AND osm_id=1`).Scan(&city); err != nil || city != "Cluj-Napoca" {
		t.Fatal("library city not retained")
	}
	var reading int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN places_place p ON p.id=pa.place_id WHERE p.osm_id=1 AND t.slug='reading'`).Scan(&reading); err != nil || reading != 1 {
		t.Fatal("library reading edge missing")
	}
	if err := r.DB.QueryRow(ctx, `SELECT pa.id,pa.confidence,pa.mapping_rule FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id JOIN places_place p ON p.id=pa.place_id WHERE p.osm_type='way' AND p.osm_id=2 AND t.slug='basketball'`).Scan(&edge, &conf, &rule); err != nil || conf != .9 || rule != "bball_pitch" {
		t.Fatal("court confidence/rule changed")
	}
	var bankEdges int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_placeactivity pa JOIN places_place p ON p.id=pa.place_id WHERE p.osm_id=3`).Scan(&bankEdges); err != nil || bankEdges != 0 {
		t.Fatal("unmapped bank invented edges")
	}
	if _, err := r.DB.Exec(ctx, `UPDATE places_placeactivity SET origin='confirmed',confidence=1 WHERE id=$1`, edge); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ingestPlaces(ctx, args(map[string]any{"source": "osm", "city": "Cluj-Napoca"})); err != nil {
		t.Fatal(err)
	}
	var nextPlaces, nextEdges int
	var origin string
	if err := r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM places_place),(SELECT count(*) FROM places_placeactivity)`).Scan(&nextPlaces, &nextEdges); err != nil || places != nextPlaces || edges != nextEdges {
		t.Fatal("OSM replay grew places/edges")
	}
	if err := r.DB.QueryRow(ctx, `SELECT origin,confidence FROM places_placeactivity WHERE id=$1`, edge).Scan(&origin, &conf); err != nil || origin != "confirmed" || conf != 1 {
		t.Fatal("reingest overwrote confirmed edge")
	}
}

type operatorCaseSource2 []RawPlace

func (source operatorCaseSource2) Fetch(ctx context.Context, options PlaceOptions, emit func(RawPlace) error) error {
	for _, row := range source {
		if err := emit(row); err != nil {
			return err
		}
	}
	return nil
}

func TestPostgresOperatorCase2WebsiteFilteringLicenseAndBooking(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	id1, id2 := int64(101), int64(102)
	s.Config.Sources = map[string]PlaceSource{"osm": operatorCaseSource2{{Source: "osm", OSMType: "node", OSMID: &id1, Name: "Bookable Hall", Lon: 23.6, Lat: 46.77, Tags: map[string]any{"leisure": "sports_hall"}, Website: "https://venue.example.ro", Attribution: "OpenStreetMap contributors", License: "ODbL", Provenance: "https://www.openstreetmap.org/node/101"}, {Source: "osm", OSMType: "node", OSMID: &id2, Name: "No Site Park", Lon: 23.61, Lat: 46.78, Tags: map[string]any{"leisure": "park"}}}}
	if _, err := s.ingestPlaces(ctx, args(map[string]any{"source": "osm", "city": "Cluj", "with_website": true})); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM places_place`).Scan(&count); err != nil || count != 1 {
		t.Fatal("with-website filter accepted missing website")
	}
	var website, credit, license, provenance string
	if err := r.DB.QueryRow(ctx, `SELECT website,attribution,license_name,provenance_url FROM places_place WHERE osm_id=101`).Scan(&website, &credit, &license, &provenance); err != nil || website != "https://venue.example.ro" || credit != "OpenStreetMap contributors" || license != "ODbL" || provenance != "https://www.openstreetmap.org/node/101" {
		t.Fatal("website or source licensing facts lost")
	}
	if _, err := s.ingestPlaces(ctx, args(map[string]any{"source": "osm", "city": "Cluj"})); err != nil {
		t.Fatal(err)
	}
	if _, err := s.seedBooking(ctx, nil); err != nil {
		t.Fatal(err)
	}
	var deep string
	if err := r.DB.QueryRow(ctx, `SELECT deep_link FROM booking_placebookinginfo`).Scan(&deep); err != nil || deep != "https://venue.example.ro" {
		t.Fatal("website booking deep-link differs")
	}
	if err := r.DB.QueryRow(ctx, `SELECT count(*) FROM booking_placebookinginfo`).Scan(&count); err != nil || count != 1 {
		t.Fatal("nonwebsite venue got booking row")
	}
}

func TestPostgresOperatorCase2OvertureNoDedupReplayAndHours(t *testing.T) {
	r, s := commandFixture(t)
	ctx := context.Background()
	if _, err := r.DB.Exec(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Central Library','osm','node',42,'',ST_SetSRID(ST_MakePoint(23.59001,46.77001),4326),'{}','','','','','','',NULL,'','',now(),now(),'','','')`); err != nil {
		t.Fatal(err)
	}
	s.Config.Sources = map[string]PlaceSource{"overture": operatorCaseSource2{{Source: "overture", ExternalID: "ov-lib", Name: "Central Library", Lon: 23.59, Lat: 46.77, Tags: map[string]any{"overture:category": "library", "overture:alternate": []string{}}}, {Source: "overture", ExternalID: "ov-cafe", Name: "Joc Board Game Cafe", Lon: 23.7, Lat: 46.8, Tags: map[string]any{"overture:category": "board_game_store", "overture:alternate": []string{}, "overture:website": "https://joc.example.ro"}, Website: "https://joc.example.ro"}}}
	input := args(map[string]any{"source": "overture", "bbox": "23,46,24,47", "dedup": false})
	if _, err := s.ingestPlaces(ctx, input); err != nil {
		t.Fatal(err)
	}
	var places, edges int
	if err := r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM places_place),(SELECT count(*) FROM places_placeactivity)`).Scan(&places, &edges); err != nil || places != 3 {
		t.Fatal("explicit no-dedup did not create separate library")
	}
	if _, err := s.ingestPlaces(ctx, input); err != nil {
		t.Fatal(err)
	}
	var afterPlaces, afterEdges int
	if err := r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM places_place),(SELECT count(*) FROM places_placeactivity)`).Scan(&afterPlaces, &afterEdges); err != nil || afterPlaces != places || afterEdges != edges {
		t.Fatal("Overture replay grew places/edges")
	}
	if _, err := r.DB.Exec(ctx, `UPDATE places_place SET opening_hours_raw='Mo-Su 10:00-22:00' WHERE external_id='ov-cafe'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.enrichPlaces(ctx, args(map[string]any{"source": "overture"})); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := r.DB.QueryRow(ctx, `SELECT opening_hours FROM places_place WHERE external_id='ov-cafe'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var schedule map[string][][]int
	if json.Unmarshal(raw, &schedule) != nil || !reflect.DeepEqual(schedule["mo"], [][]int{{600, 1320}}) {
		t.Fatal("Overture hours enrichment differs")
	}
}
