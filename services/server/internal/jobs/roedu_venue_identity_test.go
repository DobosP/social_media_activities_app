package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// The real opera_cluj_events legal basis the producer serves as the item
// license: 131 runes, refused by the former 120-rune bound.
const operaClujLicense = "Law 8/1996 art. 9: factual event listings (title/date/venue) of a public national institution (Opera Nationala Romana Cluj-Napoca)."

func cloneRoeduItem(t *testing.T, item map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var out map[string]any
	if err = decoder.Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func fixtureVenueAndEvent(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	var venue, event map[string]any
	for _, item := range readPackFixture(t).Items {
		switch item["kind"] {
		case "venue":
			venue = item
		case "event":
			event = item
		}
	}
	if venue == nil || event == nil {
		t.Fatal("fixture page lost its venue or event")
	}
	return venue, event
}

// sameBuildingVenue clones the fixture venue and its event under a new venue
// id and name at the fixture venue's exact coordinates.
func sameBuildingVenue(t *testing.T, venueID, title, eventID, license string) (map[string]any, map[string]any) {
	t.Helper()
	baseVenue, baseEvent := fixtureVenueAndEvent(t)
	venue, event := cloneRoeduItem(t, baseVenue), cloneRoeduItem(t, baseEvent)
	venue["id"], venue["title"], venue["license"] = venueID, title, license
	event["id"], event["title"], event["license"] = eventID, title+" event", license
	event["venue_id"], event["place_id"] = venueID, venueID
	facets := event["facets"].(map[string]any)
	facets["venue_id"], facets["place_id"] = venueID, venueID
	if !ValidatePackItem(venue) || !ValidatePackItem(event) {
		t.Fatal("same-building fixture items are not valid served items")
	}
	return venue, event
}

func TestRoeduLicenseBoundMatchesServer(t *testing.T) {
	venue, _ := fixtureVenueAndEvent(t)
	for _, tc := range []struct {
		license string
		valid   bool
	}{{operaClujLicense, true}, {strings.Repeat("ă", 500), true}, {strings.Repeat("ă", 501), false}, {"", false}} {
		item := cloneRoeduItem(t, venue)
		item["license"] = tc.license
		if got := ValidatePackItem(item); got != tc.valid {
			t.Fatalf("license of %d runes: valid=%v want %v", len([]rune(tc.license)), got, tc.valid)
		}
	}
	if len([]rune(operaClujLicense)) != 131 {
		t.Fatal("real license text changed length")
	}
}

func TestRoeduVenuesConflict(t *testing.T) {
	for _, tc := range []struct {
		left, right []string
		conflict    bool
	}{{nil, nil, false}, {[]string{"venue-1"}, nil, false}, {nil, []string{"venue-1"}, false}, {[]string{"venue-1"}, []string{"venue-1"}, false}, {[]string{"venue-1"}, []string{"venue-2"}, true}, {[]string{"venue-1", "venue-2"}, nil, true}} {
		if got := RoeduVenuesConflict(tc.left, tc.right); got != tc.conflict {
			t.Fatal(tc.left, tc.right, got)
		}
	}
}

// ADR-0058: RO-EDU venue identity wins over location. Venues sharing one
// building (Opera Națională Română Cluj and Teatrul Național Cluj) or even one
// name, with different venue ids, stay distinct places that each keep their
// own events; only one venue may ever claim a nearby OSM place.
func TestPostgresRoeduSameLocationVenuesStayDistinct(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	var width int
	if err := r.DB.QueryRow(ctx, `SELECT min(atttypmod-4) FROM pg_attribute WHERE attrelid IN('places_place'::regclass,'events_event'::regclass) AND attname='license_name'`).Scan(&width); err != nil || width != 500 {
		t.Fatal("license credit columns not widened", width, err)
	}
	pack := readPackFixture(t)
	baseVenue, _ := fixtureVenueAndEvent(t)
	loc := baseVenue["location"].(map[string]any)
	// An OSM place named exactly like the first and third RO-EDU venue.
	var osm int64
	if err := r.DB.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Teatrul Național Cluj','osm','node',7001,'',ST_SetSRID(ST_MakePoint($1,$2),4326),'{"amenity":"theatre"}','','','Cluj-Napoca','','RO','',NULL,'','',now(),now(),'OpenStreetMap contributors','ODbL','') RETURNING id`, numberValue(loc, "lon"), numberValue(loc, "lat")).Scan(&osm); err != nil {
		t.Fatal(err)
	}
	opera, operaEvent := sameBuildingVenue(t, "venue-2", "Opera Națională Română Cluj", "event-2", operaClujLicense)
	hall, hallEvent := sameBuildingVenue(t, "venue-3", "Teatrul Național Cluj", "event-3", "Law 8/1996 art. 9")
	pack.Items = append(pack.Items, opera, operaEvent, hall, hallEvent)
	want := map[string]string{"event-1": "venue-1", "event-2": "venue-2", "event-3": "venue-3"}
	var places map[string]int64
	for run := 0; run < 2; run++ {
		summary, err := r.ApplyRoedu(ctx, pack, "Cluj-Napoca")
		if err != nil || summary["venues"] != 3 || summary["events"] != 3 {
			t.Fatal(run, summary, err)
		}
		got := map[string]int64{}
		for event, venue := range want {
			var place int64
			var license string
			if err = r.DB.QueryRow(ctx, `SELECT place_id,license_name FROM events_event WHERE source='roedu' AND external_id=$1 AND source_venue_id=$2`, event, venue).Scan(&place, &license); err != nil {
				t.Fatal(run, event, err)
			}
			if event == "event-2" && license != operaClujLicense {
				t.Fatal("131-rune license truncated or lost", license)
			}
			got[venue] = place
		}
		if got["venue-1"] != osm {
			t.Fatal(run, "exact-name venue did not merge into the OSM place", got, osm)
		}
		if got["venue-1"] == got["venue-2"] || got["venue-1"] == got["venue-3"] || got["venue-2"] == got["venue-3"] {
			t.Fatal(run, "same-location RO-EDU venues share a place", got)
		}
		for _, venue := range []string{"venue-2", "venue-3"} {
			var source, external string
			if err = r.DB.QueryRow(ctx, `SELECT source,external_id FROM places_place WHERE id=$1`, got[venue]).Scan(&source, &external); err != nil || source != "roedu" || external != venue {
				t.Fatal(run, venue, source, external, err)
			}
		}
		var claims []string
		if err = r.DB.QueryRow(ctx, `SELECT `+RoeduVenueIdentitiesSQL("p")+` FROM places_place p WHERE id=$1`, osm).Scan(&claims); err != nil || len(claims) != 1 || claims[0] != "venue-1" {
			t.Fatal(run, "OSM place carries other RO-EDU venues", claims, err)
		}
		var events, placesCount int
		if err = r.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM events_event WHERE source='roedu'),(SELECT count(*) FROM places_place)`).Scan(&events, &placesCount); err != nil || events != 3 || placesCount != 3 {
			t.Fatal(run, "re-sync changed the venue mapping", events, placesCount, err)
		}
		if places != nil && (places["venue-1"] != got["venue-1"] || places["venue-2"] != got["venue-2"] || places["venue-3"] != got["venue-3"]) {
			t.Fatal("re-sync moved a venue", places, got)
		}
		places = got
	}
}

// Syncs for different city facets ("Bucuresti" and "București") hold
// different advisory locks, so two can race for one OSM place. The second
// waits on the row lock and must see the first venue's committed claim.
func TestPostgresRoeduConcurrentClaimNeverSharesAPlace(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	var osm int64
	if err := r.DB.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Sala Palatului','osm','node',7002,'',ST_SetSRID(ST_MakePoint(26.0996,44.4362),4326),'{"amenity":"theatre"}','','','Bucuresti','','RO','',NULL,'','',now(),now(),'','','') RETURNING id`).Scan(&osm); err != nil {
		t.Fatal(err)
	}
	first, err := r.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx)
	if _, err = first.Exec(ctx, `UPDATE places_place SET raw_tags=raw_tags||'{"merged_sources":[{"source":"roedu","external_id":"venue-a"}]}'::jsonb WHERE id=$1`, osm); err != nil {
		t.Fatal(err)
	}
	type result struct {
		id  int64
		err error
	}
	done := make(chan result, 1)
	second, err := r.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback(ctx)
	var pid int
	if err = second.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	go func() {
		id, err := findRoeduDuplicate(ctx, second, map[string]any{"id": "venue-b", "title": "Sala Palatului", "location": map[string]any{"lon": 26.0996, "lat": 44.4362}})
		done <- result{id, err}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		if err = r.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE pid=$1 AND NOT granted)`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case res := <-done:
			t.Fatal("second claim did not wait for the first", res.id, res.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("second claim never reached the row lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err = first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	res := <-done
	if res.err != nil || res.id != 0 {
		t.Fatal("a second RO-EDU venue claimed an already-claimed place", res.id, res.err)
	}
}
