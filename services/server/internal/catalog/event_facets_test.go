package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"testing"
	"time"
)

func readEventFacets(t *testing.T, s *Service, path string) eventFacetResponse {
	t.Helper()
	w := request(t, s, path, nil)
	var body eventFacetResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal("facet status/shape", w.Code, w.Body.String())
	}
	if body.Cities.Results == nil || body.Places.Results == nil {
		t.Fatal("empty facet pages must be arrays")
	}
	return body
}

func TestEventFacetsFullAggregationAndBoundedPages(t *testing.T) {
	s := fixture(t, false)
	ctx := context.Background()
	future := time.Now().Add(48 * time.Hour)
	var first int64
	for n := 0; n < 205; n++ {
		// Equal coordinates and names never collapse separate venue IDs.
		place := placeFixture(t, s, "Same Hall", "osm", 23.6, 46.77)
		if n == 0 {
			first = place
		}
		if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_city=$2 WHERE id=$1`, place, fmt.Sprintf("City %03d", n)); err != nil {
			t.Fatal(err)
		}
		_ = eventFixture(t, s, "Future fixture", "scheduled", &place, future)
	}
	for n := 0; n < 5; n++ {
		_ = eventFixture(t, s, "Additional fixture", "scheduled", &first, future)
	}
	for _, base := range []string{"/api/events/", "/api/v1/events/"} {
		body := readEventFacets(t, s, base+"facets/?limit=1000000")
		if body.Count != 210 || body.Cities.Count != 205 || body.Places.Count != 205 || len(body.Cities.Results) != 200 || len(body.Places.Results) != 200 {
			t.Fatal("counts came from a bounded event/bucket page", body.Count, body.Cities.Count, body.Places.Count)
		}
		if body.Cities.Limit != 200 || body.Places.Limit != 200 || body.Cities.Complete || !body.Cities.Truncated || body.Places.Complete || !body.Places.Truncated {
			t.Fatal("hard cap/completeness", body.Cities, body.Places)
		}
		if body.Cities.Results[0].Key != "city 000" || body.Cities.Results[0].Count != 6 || body.Places.Results[0].ID != first || body.Places.Results[0].Count != 6 {
			t.Fatal("aggregate count/order lost", body.Cities.Results[0], body.Places.Results[0])
		}
		next := readEventFacets(t, s, base+"facets/?limit=200&offset=200")
		if next.Count != 210 || next.Cities.Offset != 200 || len(next.Cities.Results) != 5 || len(next.Places.Results) != 5 || !next.Cities.Truncated || next.Cities.Complete {
			t.Fatal("last bucket page falsely claimed all buckets", next.Cities)
		}
		cities, places := map[string]bool{}, map[int64]bool{}
		for _, page := range []eventFacetResponse{body, next} {
			for _, city := range page.Cities.Results {
				if cities[city.Key] {
					t.Fatal("duplicate city across stable pages", city.Key)
				}
				cities[city.Key] = true
			}
			for _, place := range page.Places.Results {
				if places[place.ID] {
					t.Fatal("duplicate place across stable pages", place.ID)
				}
				places[place.ID] = true
			}
		}
		if len(cities) != 205 || len(places) != 205 {
			t.Fatal("continuation lost buckets", len(cities), len(places))
		}
	}
	for _, tc := range []struct {
		query         string
		limit, offset int
		returned      int
	}{
		{"", 50, 0, 50}, {"?limit=0&offset=-1", 1, 0, 1},
		{"?limit=bad&offset=bad", 50, 0, 50}, {"?limit=1&offset=1", 1, 1, 1},
		{"?limit=200&offset=2000000", 200, 1000000, 0},
	} {
		body := readEventFacets(t, s, "/api/v1/events/facets/"+tc.query)
		if body.Cities.Limit != tc.limit || body.Places.Limit != tc.limit || body.Cities.Offset != tc.offset || body.Places.Offset != tc.offset || len(body.Cities.Results) != tc.returned || len(body.Places.Results) != tc.returned || body.Count != 210 {
			t.Fatal("existing pagination bounds not preserved", tc.query)
		}
	}
}

func TestEventFacetsCityAliasesStoredLabelsAndMissingFacts(t *testing.T) {
	s := fixture(t, false)
	ctx := context.Background()
	future := time.Now().Add(48 * time.Hour)
	aliases := []string{"București", "Bucuresti", "Bucharest", "\u00a0BUCUREŞTI\t", "Bucures\u0326ti", "Bucures\u0327ti"}
	stored := map[int64]string{}
	for _, city := range append(append([]string{}, aliases...), "Cluj-Napoca", "", " \u00a0\t") {
		place := placeFixture(t, s, "Same Building", "osm", 26.1, 44.4)
		stored[place] = city
		if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_city=$2 WHERE id=$1`, place, city); err != nil {
			t.Fatal(err)
		}
		_ = eventFixture(t, s, "Alias fixture", "scheduled", &place, future)
	}
	_ = eventFixture(t, s, "No venue fixture", "scheduled", nil, future)
	body := readEventFacets(t, s, "/api/v1/events/facets/?limit=200")
	sort.Strings(aliases)
	if body.Count != 10 || body.MissingCityCount != 3 || body.MissingPlaceCount != 1 || body.Cities.Count != 2 || body.Places.Count != 9 || !body.Cities.Complete || body.Cities.Truncated || !body.Places.Complete {
		t.Fatal("missing facts or grouping", body)
	}
	if body.Cities.Results[0] != (eventCityFacet{Key: "bucuresti", Label: aliases[0], Count: 6}) || body.Cities.Results[1].Key != "cluj-napoca" {
		t.Fatal("alias grouping invented/normalized a display fact", body.Cities.Results)
	}
	for _, place := range body.Places.Results {
		var city string
		if err := s.DB.QueryRow(ctx, `SELECT address_city FROM places_place WHERE id=$1`, place.ID).Scan(&city); err != nil || city != stored[place.ID] || place.City != city || place.CityKey != CityKey(city) {
			t.Fatal("stored city changed or SQL/Go keys differ", place.ID, err)
		}
	}
	for _, alias := range aliases {
		body := readEventFacets(t, s, "/api/events/facets/?city="+url.QueryEscape(alias))
		if body.Count != 6 || body.Cities.Count != 1 || body.Places.Count != 6 || body.MissingCityCount != 0 {
			t.Fatal("city filter and city grouping disagree", alias, body)
		}
	}
	empty := readEventFacets(t, s, "/api/v1/events/facets/?city=Unknown&offset=20")
	if empty.Count != 0 || empty.Cities.Count != 0 || empty.Places.Count != 0 || !empty.Cities.Complete || empty.Cities.Truncated || !empty.Places.Complete || empty.LocalPOC || empty.PublicationNotice != nil {
		t.Fatal("unknown city widened scope or empty page misrepresented", empty)
	}
}

func TestEventFacetsShareAllEventFilters(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	capital := placeFixture(t, s, "Capital Hall", "osm", 26.1, 44.4)
	reading := placeFixture(t, s, "Reading Venue", "osm", 26.1, 44.4)
	cluj := placeFixture(t, s, "Cluj Hall", "osm", 23.6, 46.77)
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_city='București' WHERE id IN($1,$2)`, capital, reading); err != nil {
		t.Fatal(err)
	}
	when := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Second)
	alpha := eventFixture(t, s, "Alpha basketball", "scheduled", &capital, when)
	beta := eventFixture(t, s, "Beta reading", "sold_out", &reading, when.Add(24*time.Hour))
	other := eventFixture(t, s, "Alpha elsewhere", "rescheduled", &cluj, when)
	literal := eventFixture(t, s, "Literal 100%_safe", "scheduled", &reading, when.Add(12*time.Hour))
	past := eventFixture(t, s, "Past fixture", "expired", &capital, time.Now().Add(-24*time.Hour))
	cancelled := eventFixture(t, s, "Cancelled fixture", "cancelled", &capital, when)
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET activity_type_id=(SELECT id FROM taxonomy_activitytype WHERE slug='basketball') WHERE id IN($1,$2)`, alpha, other); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET activity_type_id=(SELECT id FROM taxonomy_activitytype WHERE slug='reading') WHERE id=$1`, beta); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, query string
		ids         []int64
	}{
		{"upcoming", "", []int64{alpha, beta, other, literal}},
		{"city", "city=Bucharest", []int64{alpha, beta, literal}},
		{"activity", "activity=basketball", []int64{alpha, other}},
		{"place", "place=" + fmt.Sprint(reading), []int64{beta, literal}},
		{"from", "from=" + url.QueryEscape(when.Add(6*time.Hour).Format(time.RFC3339)), []int64{beta, literal}},
		{"to", "to=" + url.QueryEscape(when.Add(time.Hour).Format(time.RFC3339)), []int64{alpha, other}},
		{"date", "from=" + when.Format("2006-01-02") + "&to=" + when.Add(48*time.Hour).Format("2006-01-02"), []int64{alpha, beta, other, literal}},
		{"search_title", "q=Alpha", []int64{alpha, other}},
		{"search_venue", "q=Reading+Venue", []int64{beta, literal}},
		{"search_description", "q=keyword", []int64{alpha, beta, other, literal}},
		{"literal_wildcards", "q=" + url.QueryEscape("%_"), []int64{literal}},
		{"short_search_ignored", "q=A", []int64{alpha, beta, other, literal}},
		{"combined", "city=Bucuresti&activity=basketball&q=Alpha&near_lon=26.1&near_lat=44.4&radius_m=2000", []int64{alpha}},
		{"geo_radius", "near_lon=26.1&near_lat=44.4&radius_m=2000", []int64{alpha, beta, literal}},
		{"geo_order_only", "near_lon=26.1&near_lat=44.4", []int64{alpha, beta, other, literal}},
		{"history_existing_semantics", "include_past=true", []int64{alpha, beta, other, literal, past, cancelled}},
		{"past_only_remains_inert", "past_only=1", []int64{alpha, beta, other, literal}},
		{"unknown_place", "place=999999999", nil},
		{"unknown_activity", "activity=unknown", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			facet := readEventFacets(t, s, "/api/v1/events/facets/?limit=200&"+tc.query)
			api := request(t, s, "/api/v1/events/?limit=200&"+tc.query, nil)
			body := jsonObject(t, api.Body.Bytes())
			if api.Code != 200 || facet.Count != int64(len(tc.ids)) || body["count"] != float64(len(tc.ids)) {
				t.Fatal("facet/API filter scope differs", facet.Count, api.Code, body["count"], len(tc.ids))
			}
			got := []int64{}
			placeCounts := map[int64]int64{}
			for _, item := range body["results"].([]any) {
				row := item.(map[string]any)
				got = append(got, int64(row["id"].(float64)))
				placeCounts[int64(row["place"].(float64))]++
			}
			sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
			want := append([]int64{}, tc.ids...)
			sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
			if !reflect.DeepEqual(got, want) || int64(len(placeCounts)) != facet.Places.Count {
				t.Fatal("API identity scope or facet place identity lost", got, want)
			}
			for _, place := range facet.Places.Results {
				if place.Count != placeCounts[place.ID] {
					t.Fatal("filtered place tally lost", place)
				}
			}
		})
	}
}

func TestEventFacetsPublicationAndRuntimePolicy(t *testing.T) {
	s := fixture(t, false)
	ctx := context.Background()
	future := time.Now().Add(48 * time.Hour)
	public := placeFixture(t, s, "Public", "osm", 23.6, 46.77)
	pending := placeFixture(t, s, "Pending", "user", 23.6, 46.77)
	closed := placeFixture(t, s, "Closed", "osm", 23.6, 46.77)
	_ = eventFixture(t, s, "Public event", "scheduled", &public, future)
	_ = eventFixture(t, s, "No venue event", "scheduled", nil, future)
	_ = eventFixture(t, s, "Pending event", "scheduled", &pending, future)
	_ = eventFixture(t, s, "Closed event", "scheduled", &closed, future)
	held := eventFixture(t, s, "Held event", "scheduled", &public, future)
	tomb := eventFixture(t, s, "Tombstone event", "scheduled", &public, future)
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET is_import_held=true WHERE id=$1`, held); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET is_tombstone=true WHERE id=$1`, tomb); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 2; n++ {
		reporter := user(t, s, fmt.Sprintf("facet-closure-%d", n), "adult")
		if _, err := s.DB.Exec(ctx, `INSERT INTO places_placeclosurereport(place_id,reporter_id,created_at) VALUES($1,$2,now()-interval '20 days')`, closed, reporter.ID); err != nil {
			t.Fatal(err)
		}
	}
	s.Policy.ClosureReportThreshold, s.Policy.ClosureReportDecay = 2, 30*24*time.Hour
	for _, suffix := range []string{"", "?include_past=1"} {
		body := readEventFacets(t, s, "/api/v1/events/facets/"+suffix)
		if body.Count != 2 || body.MissingPlaceCount != 1 || body.Cities.Count != 1 || body.Places.Count != 1 || body.Places.Results[0].ID != public || body.Places.Results[0].Count != 1 {
			t.Fatal("facet bypassed runtime closure/import/proposal gate", body)
		}
	}
	s.Policy = DefaultPolicy()
	if body := readEventFacets(t, s, "/api/v1/events/facets/"); body.Count != 3 {
		t.Fatal("default policy disagreed with existing event scope", body.Count)
	}
	s.Policy.ClosureReportThreshold = 4 // invalid supplied policy fails closed.
	if body := readEventFacets(t, s, "/api/v1/events/facets/?include_past=1"); body.Count != 0 || body.Cities.Count != 0 || body.Places.Count != 0 {
		t.Fatal("invalid runtime policy admitted facet rows", body)
	}
}

func TestEventFacetsLocalPOCMarkerAndExactPublicShape(t *testing.T) {
	s := fixture(t, false)
	ctx := context.Background()
	place := placeFixture(t, s, "POC fixture", "osm", 23.6, 46.77)
	event := eventFixture(t, s, "POC fixture", "scheduled", &place, time.Now().Add(48*time.Hour))
	path := "/api/v1/events/facets/"
	production := readEventFacets(t, s, path)
	if production.LocalPOC || production.PublicationNotice != nil {
		t.Fatal("production acquired a POC label", production)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags=raw_tags||'{"roedu_local_poc":true}'::jsonb WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	venuePOC := readEventFacets(t, s, path)
	if !venuePOC.LocalPOC || venuePOC.PublicationNotice == nil || *venuePOC.PublicationNotice != LocalPOCMarker {
		t.Fatal("POC venue aggregate unlabeled", venuePOC)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags=raw_tags-'roedu_local_poc' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `ALTER TABLE events_event ADD COLUMN source_local_poc boolean NOT NULL DEFAULT false`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET source_local_poc=true WHERE id=$1`, event); err != nil {
		t.Fatal(err)
	}
	flagged := readEventFacets(t, s, path)
	if !flagged.LocalPOC || flagged.PublicationNotice == nil || *flagged.PublicationNotice != LocalPOCMarker {
		t.Fatal("POC event aggregate unlabeled", flagged)
	}
	body := jsonObject(t, request(t, s, path, nil).Body.Bytes())
	keys := func(v map[string]any) []string {
		out := make([]string, 0, len(v))
		for key := range v {
			out = append(out, key)
		}
		sort.Strings(out)
		return out
	}
	for _, tc := range []struct {
		value map[string]any
		want  []string
	}{
		{body, []string{"cities", "count", "local_poc", "missing_city_count", "missing_place_count", "places", "publication_notice"}},
		{body["cities"].(map[string]any), []string{"complete", "count", "limit", "offset", "results", "truncated"}},
		{body["places"].(map[string]any), []string{"complete", "count", "limit", "offset", "results", "truncated"}},
		{body["cities"].(map[string]any)["results"].([]any)[0].(map[string]any), []string{"count", "key", "label"}},
		{body["places"].(map[string]any)["results"].([]any)[0].(map[string]any), []string{"city", "city_key", "count", "id", "name"}},
	} {
		if !reflect.DeepEqual(keys(tc.value), tc.want) {
			t.Fatal("facet exposed noncontract fields", keys(tc.value), tc.want)
		}
	}
}

func TestEventFacetsInvalidQueriesFailBeforeDatabase(t *testing.T) {
	s := New(nil)
	for _, query := range []string{
		"from=bad", "to=2026-02-30", "place=bad", "near_lon=NaN&near_lat=46",
		"near_lon=23&near_lat=91", "near_lon=23&near_lat=46&radius_m=bad",
		"city=a&city=b", "q=%ZZ", "from=2026-10-13T25:00",
	} {
		for _, base := range []string{"/api/events/facets/", "/api/v1/events/facets/"} {
			if w := request(t, s, base+"?"+query, nil); w.Code != http.StatusBadRequest {
				t.Fatal("invalid facet query reached a database", query, w.Code)
			}
		}
	}
	if w := request(t, s, "/api/v1/events/0/", nil); w.Code != http.StatusNotFound {
		t.Fatal("static facet route changed detail dispatch", w.Code)
	}
}
