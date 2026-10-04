package catalog

import (
	"context"
	"encoding/json"
	"net/url"
	"reflect"
	"testing"
	"time"
)

func TestPostgresNativeEventVenueSearchPreservesUnicodeEscapingAndPublication(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	venue := placeFixture(t, s, "Biblioteca Științei 100%_literal", "osm", 23.6, 46.77)
	pending := placeFixture(t, s, "Private Științei", "user", 23.6, 46.77)
	at := time.Now().Add(24 * time.Hour)
	visible := eventFixture(t, s, "Venue search positive", "scheduled", &venue, at)
	_ = eventFixture(t, s, "Venue search cancelled", "cancelled", &venue, at)
	_ = eventFixture(t, s, "Venue search pending", "scheduled", &pending, at)
	for _, column := range []string{"is_tombstone", "is_import_held"} {
		id := eventFixture(t, s, "Venue search withheld", "scheduled", &venue, at)
		if _, err := s.DB.Exec(ctx, `UPDATE events_event SET `+column+`=true WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
	}
	for query, want := range map[string][]int64{"Științei": {visible}, "100%_literal": {visible}, "Private Științei": {}, "No such venue": {}} {
		out := request(t, s, "/api/v1/events/?q="+url.QueryEscape(query), nil)
		var page struct {
			Count   int
			Results []struct {
				ID int64 `json:"id"`
			}
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &page) != nil {
			t.Fatal("native venue search response")
		}
		got := []int64{}
		for _, row := range page.Results {
			got = append(got, row.ID)
		}
		if page.Count != len(want) || !reflect.DeepEqual(got, want) {
			t.Fatalf("venue query %q: got IDs=%v/count=%d want=%v", query, got, page.Count, want)
		}
	}
}

func TestPostgresNativeEventVenueSearchUsesRawNameAndPreservesCorrectedDisplay(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	venue := placeFixture(t, s, "OldCourt", "osm", 23.6, 46.77)
	actor := user(t, s, "venue-correction-fixture", "adult")
	if _, err := s.DB.Exec(ctx, `INSERT INTO places_placecorrection(place_id,proposer_id,field,proposed_value,required_confirmations,status,created_at,published_at) VALUES($1,$2,'name','NewCourt',3,'published',now(),now())`, venue, actor.ID); err != nil {
		t.Fatal(err)
	}
	event := eventFixture(t, s, "Unrelated activity", "scheduled", &venue, time.Now().Add(24*time.Hour))
	query := func(text string, want int) {
		t.Helper()
		out := request(t, s, "/api/v1/events/?q="+url.QueryEscape(text), nil)
		var data struct {
			Count   int
			Results []struct{ ID int64 }
		}
		if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &data) != nil || data.Count != want || len(data.Results) != want || want == 1 && data.Results[0].ID != event {
			t.Fatal("raw venue search contract differs")
		}
	}
	query("OldCourt", 1)
	query("NewCourt", 0)
	out := request(t, s, "/api/v1/places/"+strconvInt(venue)+"/", nil)
	var display struct{ Properties struct{ Name string } }
	if out.Code != 200 || json.Unmarshal(out.Body.Bytes(), &display) != nil || display.Properties.Name != "NewCourt" {
		t.Fatal("search repair reverted approved venue display correction")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET title='NewCourt activity' WHERE id=$1`, event); err != nil {
		t.Fatal(err)
	}
	query("NewCourt", 1)
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET title='Unrelated activity',description='NewCourt description' WHERE id=$1`, event); err != nil {
		t.Fatal(err)
	}
	query("NewCourt", 1)
}
