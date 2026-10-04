package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func retirementPackPage(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/roedu-page.json")
	if err != nil {
		t.Fatal(err)
	}
	var page map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&page); err != nil {
		t.Fatal(err)
	}
	return page
}

func TestRetirementICSRecurrenceMatrix(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name, start, rule string
		days              []string
	}{
		{"weekly_count", "20260105T100000Z", "FREQ=WEEKLY;COUNT=3", []string{"20260105", "20260112", "20260119"}},
		{"weekly_until", "20260105T100000Z", "FREQ=WEEKLY;UNTIL=20260112T235959Z", []string{"20260105", "20260112"}},
		{"weekly_interval", "20260105T100000Z", "FREQ=WEEKLY;INTERVAL=2;COUNT=3", []string{"20260105", "20260119", "20260202"}},
		{"weekly_byday", "20260105T100000Z", "FREQ=WEEKLY;BYDAY=MO,WE;COUNT=4", []string{"20260105", "20260107", "20260112", "20260114"}},
		{"byday_case", "20260105T100000Z", "FREQ=WEEKLY;BYDAY=mo,we;COUNT=4", []string{"20260105", "20260107", "20260112", "20260114"}},
		{"daily_count", "20260105T100000Z", "FREQ=DAILY;COUNT=3", []string{"20260105", "20260106", "20260107"}},
		{"monthly_reanchor", "20260131T100000Z", "FREQ=MONTHLY;COUNT=3", []string{"20260131", "20260228", "20260331"}},
		{"monthly_ordinary_anchor", "20260116T100000Z", "FREQ=MONTHLY;COUNT=3", []string{"20260116", "20260216", "20260316"}},
		{"unsupported_yearly", "20260105T100000Z", "FREQ=YEARLY;COUNT=3", []string{"20260105"}},
		{"unsupported_monthly_byday", "20260105T100000Z", "FREQ=MONTHLY;BYDAY=MO;COUNT=3", []string{"20260105"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw := "BEGIN:VEVENT\nUID:retirement-calendar\nSUMMARY:Reviewed event\nDTSTART:" + c.start + "\nRRULE:" + c.rule + "\nEND:VEVENT"
			events, err := ParseICS(raw, now)
			if err != nil {
				t.Fatal(err)
			}
			days := []string{}
			ids := map[string]bool{}
			for _, event := range events {
				days = append(days, event.Starts.Format("20060102"))
				if ids[event.ExternalID] {
					t.Fatal("recurring UID collided")
				}
				ids[event.ExternalID] = true
				if c.rule != "FREQ=YEARLY;COUNT=3" && c.rule != "FREQ=MONTHLY;BYDAY=MO;COUNT=3" && !strings.HasPrefix(event.ExternalID, "retirement-calendar:") {
					t.Fatal("recurring UID lost its stable source prefix")
				}
				if len(event.ExternalID) > 170 {
					t.Fatal("occurrence UID consumed feed namespace headroom")
				}
			}
			if !reflect.DeepEqual(days, c.days) {
				t.Fatalf("calendar days got%v want%v", days, c.days)
			}
		})
	}
	t.Run("duration_preserved", func(t *testing.T) {
		events, err := ParseICS("BEGIN:VEVENT\nUID:duration\nSUMMARY:Reviewed event\nDTSTART:20260105T100000Z\nDTEND:20260105T113000Z\nRRULE:FREQ=DAILY;COUNT=3\nEND:VEVENT", now)
		if err != nil || len(events) != 3 {
			t.Fatal("duration fixture", err)
		}
		for _, event := range events {
			if event.Ends == nil || event.Ends.Sub(event.Starts) != 90*time.Minute {
				t.Fatal("recurrence changed duration")
			}
		}
	})
	t.Run("forever_bounded", func(t *testing.T) {
		events, err := ParseICS("BEGIN:VEVENT\nUID:forever\nSUMMARY:Reviewed event\nDTSTART:20260105T100000Z\nRRULE:FREQ=WEEKLY\nEND:VEVENT", now)
		if err != nil || len(events) < 10 || len(events) > 14 {
			t.Fatal("weekly horizon unbounded or empty", err)
		}
		for _, event := range events {
			if event.Starts.After(now.Add(91 * 24 * time.Hour)) {
				t.Fatal("event exceeded horizon")
			}
		}
	})
	t.Run("past_count_exhausted", func(t *testing.T) {
		events, err := ParseICS("BEGIN:VEVENT\nUID:past\nSUMMARY:Reviewed event\nDTSTART:20000105T100000Z\nRRULE:FREQ=DAILY;COUNT=3\nEND:VEVENT", now)
		if err != nil || len(events) != 0 {
			t.Fatal("exhausted old count resurfaced", err)
		}
	})
	t.Run("old_unbounded_series", func(t *testing.T) {
		events, err := ParseICS("BEGIN:VEVENT\nUID:old\nSUMMARY:Reviewed event\nDTSTART:20000105T100000Z\nRRULE:FREQ=DAILY\nEND:VEVENT", now)
		if err != nil || len(events) == 0 || len(events) > 120 {
			t.Fatal("old unbounded series failed finite recent expansion", err)
		}
		for _, event := range events {
			if event.Starts.Before(now.Add(-24 * time.Hour)) {
				t.Fatal("old series retained occurrences before the recent floor")
			}
		}
	})
	t.Run("bare_uid_and_blank_uid", func(t *testing.T) {
		for _, uid := range []string{"bare-id", ""} {
			events, err := ParseICS("BEGIN:VEVENT\nUID:"+uid+"\nSUMMARY:Reviewed event\nDTSTART:20260105T100000Z\nEND:VEVENT", now)
			if err != nil || len(events) != 1 || events[0].ExternalID != uid {
				t.Fatal("nonrecurring UID contract", err)
			}
		}
	})
	t.Run("recurring_long_and_blank_uid", func(t *testing.T) {
		for _, uid := range []string{strings.Repeat("x", 200), ""} {
			events, err := ParseICS("BEGIN:VEVENT\nUID:"+uid+"\nSUMMARY:Reviewed event\nDTSTART:20260105T100000Z\nRRULE:FREQ=WEEKLY;COUNT=3\nEND:VEVENT", now)
			if err != nil || len(events) != 3 {
				t.Fatal("recurrence UID fixture", err)
			}
			ids, dates := map[string]bool{}, map[time.Time]bool{}
			for _, event := range events {
				dates[event.Starts] = true
				ids[event.ExternalID] = true
				if len(event.ExternalID) > 159 || len("feed999999:"+event.ExternalID) > 200 || uid == "" && event.ExternalID != "" {
					t.Fatal("recurring UID length/blank/namespace contract")
				}
			}
			if len(dates) != 3 || uid != "" && len(ids) != 3 {
				t.Fatal("recurring UID or dates collided")
			}
		}
	})
}

func TestRetirementPostgresPlaceMappingTaxonomyClosure(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	// Every actual mapping result must resolve to a seeded primary key; aliases
	// are not activity slugs. This pins the historical streetball rule failure.
	for _, tags := range []map[string]any{{"leisure": "pitch", "sport": "basketball"}, {"leisure": "park"}, {"leisure": "sports_centre"}, {"amenity": "library"}, {"amenity": "school"}, {"tourism": "museum"}, {"shop": "books", "second_hand": "yes"}, {"amenity": "public_bookcase"}, {"leisure": "track", "sport": "running"}, {"sport": "swimming"}, {"sport": "climbing"}} {
		for _, match := range MatchPlaceTags(tags) {
			var id int64
			if err := r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug=$1`, match.Slug).Scan(&id); err != nil || id < 1 {
				t.Fatal("native mapping references an absent taxonomy primary key", match.Slug, err)
			}
		}
	}
}

func TestRetirementPlaceMappingRuleMatrix(t *testing.T) {
	for _, c := range []struct {
		name  string
		tags  map[string]any
		slugs map[string]float64
		exact bool
	}{
		{"basketball_pitch", map[string]any{"leisure": "pitch", "sport": "basketball"}, map[string]float64{"basketball": 0.9}, true},
		{"library", map[string]any{"amenity": "library", "name": "City Library"}, map[string]float64{"reading": 0.95}, true},
		{"sports_centre", map[string]any{"leisure": "sports_centre"}, map[string]float64{"basketball": 0.3, "football": 0.3, "table_tennis": 0.3, "tennis": 0.3}, true},
		{"bank", map[string]any{"amenity": "bank"}, map[string]float64{}, true},
		{"cafe_unmapped", map[string]any{"amenity": "cafe"}, map[string]float64{}, true},
		{"board_games_cafe", map[string]any{"amenity": "cafe", "board_games": "yes"}, map[string]float64{"board_games": 0.8}, true},
		{"archive", map[string]any{"amenity": "archive", "name": "National Archives"}, map[string]float64{"archive": 0.95}, true},
		{"secondhand_books", map[string]any{"shop": "books", "second_hand": "only"}, map[string]float64{"used_bookshop": 0.9, "reading": 0.6}, true},
		{"antiquarian_books", map[string]any{"shop": "books", "books": "antiquarian"}, map[string]float64{"used_bookshop": 0.9}, false},
		{"public_bookcase", map[string]any{"amenity": "public_bookcase"}, map[string]float64{"reading": 0.4}, true},
		{"running_track", map[string]any{"leisure": "track"}, map[string]float64{"running": 0.7}, false},
		{"running_sport", map[string]any{"sport": "running"}, map[string]float64{"running": 0.8}, false},
		{"cycling", map[string]any{"sport": "cycling"}, map[string]float64{"cycling": 0.7}, false},
		{"hiking", map[string]any{"route": "hiking"}, map[string]float64{"hiking": 0.8}, false},
		{"pool", map[string]any{"leisure": "swimming_pool"}, map[string]float64{"swimming": 0.7}, false},
		{"climbing", map[string]any{"sport": "climbing"}, map[string]float64{"climbing": 0.85}, false},
		{"volleyball_pitch", map[string]any{"leisure": "pitch", "sport": "volleyball"}, map[string]float64{"volleyball": 0.9}, false},
		{"handball_pitch", map[string]any{"leisure": "pitch", "sport": "handball"}, map[string]float64{"handball": 0.9}, false},
		{"museum", map[string]any{"tourism": "museum"}, map[string]float64{"museum_visit": 0.8}, false},
		{"theatre", map[string]any{"amenity": "theatre"}, map[string]float64{"theatre_show": 0.8}, false},
		{"park", map[string]any{"leisure": "park"}, map[string]float64{"running": 0.2, "cycling": 0.2}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := map[string]float64{}
			for _, match := range MatchPlaceTags(c.tags) {
				if _, present := got[match.Slug]; present {
					t.Fatal("mapping returned duplicate activity slug")
				}
				got[match.Slug] = match.Confidence
			}
			if c.exact && len(got) != len(c.slugs) {
				t.Fatal("mapping exact activity set changed")
			}
			for slug, want := range c.slugs {
				if got[slug] != want {
					t.Fatalf("mapping confidence %s got%v want%v", slug, got[slug], want)
				}
			}
		})
	}
}

func retirementReadPages(t *testing.T, pages []map[string]any) (PackRead, error) {
	t.Helper()
	calls := 0
	c := &RoeduClient{BaseURL: "https://producer.invalid", APIKey: "synthetic-contract-key", HTTP: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if calls >= len(pages) {
			t.Fatal("client fetched beyond the fixture page sequence")
		}
		if r.Method != "GET" || r.Header.Get("Accept") != "application/json" || r.Header.Get("X-API-Key") != "synthetic-contract-key" {
			t.Fatal("producer HTTP contract changed")
		}
		if !strings.HasSuffix(r.URL.Path, "/v1/app-packs/social_media_activities_app/"+SocialPack) || r.URL.Query().Get("layer") != "redistributable" || r.URL.Query().Get("limit") != "200" || r.URL.Query().Get("city") != "Cluj-Napoca" {
			t.Fatal("canonical product path/filter contract changed")
		}
		wanted := ""
		if calls > 0 {
			wanted, _ = pages[calls-1]["pagination"].(map[string]any)["next_cursor"].(string)
		}
		if r.URL.Query().Get("cursor") != wanted {
			t.Fatal("page cursor was not followed exactly")
		}
		raw, err := json.Marshal(pages[calls])
		if err != nil {
			t.Fatal(err)
		}
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
	})}}
	result, err := c.Read(context.Background(), "Cluj-Napoca")
	if err == nil && result.Pack != SocialPack {
		t.Fatal("read lost its canonical product identity")
	}
	return result, err
}

// Independent mutations exercise HTTP page/envelope contracts that item goldens
// cannot cover. No producer, provider, DNS lookup or Python process is started.
func TestRetirementROEDUEnvelopeMatrix(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"missing_identity", func(p map[string]any) { delete(p, "snapshot_id") }},
		{"unknown_envelope_field", func(p map[string]any) { p["private_extra"] = "rejected" }},
		{"wrong_pack", func(p map[string]any) { p["pack_id"] = "events_places" }},
		{"wrong_app", func(p map[string]any) { p["app"] = "another-app" }},
		{"nonredistributable", func(p map[string]any) { p["layer"] = "research" }},
		{"boolean_schema", func(p map[string]any) { p["schema_version"] = true }},
		{"unpromoted_snapshot", func(p map[string]any) { p["snapshot_id"] = "mutable-latest" }},
		{"release_snapshot_mismatch", func(p map[string]any) { p["release_id"] = "sha256-" + strings.Repeat("a", 64) }},
		{"naive_generation", func(p map[string]any) { p["snapshot_generated_at"] = "2026-07-12T08:00:00" }},
		{"invalid_generation", func(p map[string]any) { p["snapshot_generated_at"] = "2026-02-30T08:00:00Z" }},
		{"space_generation_separator", func(p map[string]any) { p["snapshot_generated_at"] = "2026-07-12 08:00:00+00:00" }},
		{"object_mode", func(p map[string]any) { p["snapshot_mode"] = map[string]any{} }},
		{"string_completeness", func(p map[string]any) { p["snapshot_complete"] = "true" }},
		{"mode_completeness_mismatch", func(p map[string]any) { p["snapshot_mode"] = "partial" }},
		{"missing_items", func(p map[string]any) { delete(p, "items") }},
		{"nonlist_items", func(p map[string]any) { p["items"] = map[string]any{} }},
		{"boolean_withheld", func(p map[string]any) { p["withheld"] = false }},
		{"negative_withheld", func(p map[string]any) { p["withheld"] = -1 }},
		{"fractional_withheld", func(p map[string]any) { p["withheld"] = 0.5 }},
		{"nonlist_errors", func(p map[string]any) { p["errors"] = "not ready" }},
		{"nonstring_error", func(p map[string]any) { p["errors"] = []any{map[string]any{}} }},
		{"missing_pagination", func(p map[string]any) { delete(p, "pagination") }},
		{"missing_cursor", func(p map[string]any) { p["pagination"] = map[string]any{} }},
		{"unknown_pagination_field", func(p map[string]any) { p["pagination"].(map[string]any)["offset"] = 1 }},
		{"empty_cursor", func(p map[string]any) { p["pagination"].(map[string]any)["next_cursor"] = "" }},
		{"numeric_cursor", func(p map[string]any) { p["pagination"].(map[string]any)["next_cursor"] = 0 }},
		{"boolean_cursor", func(p map[string]any) { p["pagination"].(map[string]any)["next_cursor"] = false }},
		{"array_cursor", func(p map[string]any) { p["pagination"].(map[string]any)["next_cursor"] = []any{} }},
		{"object_cursor", func(p map[string]any) { p["pagination"].(map[string]any)["next_cursor"] = map[string]any{} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := retirementPackPage(t)
			c.mutate(p)
			got, err := retirementReadPages(t, []map[string]any{p})
			if err == nil || got.Complete {
				t.Fatal("malformed producer envelope permitted complete reconciliation")
			}
		})
	}
}

func TestRetirementROEDUMultiplePagesAndCompleteness(t *testing.T) {
	for _, field := range []string{"snapshot_id", "release_id", "snapshot_generated_at", "snapshot_mode"} {
		t.Run("drift_"+field, func(t *testing.T) {
			a, b := retirementPackPage(t), retirementPackPage(t)
			a["pagination"].(map[string]any)["next_cursor"] = "second"
			switch field {
			case "snapshot_id", "release_id":
				b["snapshot_id"], b["release_id"] = "sha256-"+strings.Repeat("a", 64), "sha256-"+strings.Repeat("a", 64)
			case "snapshot_generated_at":
				b[field] = "2026-07-13T08:00:00+00:00"
			case "snapshot_mode":
				b[field], b["snapshot_complete"] = "partial", false
			}
			got, err := retirementReadPages(t, []map[string]any{a, b})
			if err == nil || got.Complete || !strings.Contains(err.Error(), "snapshot drift") {
				t.Fatal("page identity drift permitted reconciliation")
			}
		})
	}
	t.Run("event_before_venue", func(t *testing.T) {
		a, b := retirementPackPage(t), retirementPackPage(t)
		items := a["items"].([]any)
		a["items"], b["items"] = []any{items[1]}, []any{items[0]}
		a["pagination"].(map[string]any)["next_cursor"] = "later-venue"
		got, err := retirementReadPages(t, []map[string]any{a, b})
		if err != nil || !got.Complete || len(got.Items) != 2 || got.Items[0]["kind"] != "event" {
			t.Fatal("cross-page venue resolution changed", err)
		}
	})
	for _, c := range []struct {
		name              string
		mutate            func(map[string]any)
		count             int
		complete, refused bool
	}{
		{"clean_empty", func(p map[string]any) { p["items"] = []any{} }, 0, true, false},
		{"all_withheld", func(p map[string]any) { p["items"] = []any{}; p["withheld"] = 1 }, 0, false, true},
		{"all_producer_errors", func(p map[string]any) { p["items"] = []any{}; p["errors"] = []any{"source unavailable"} }, 0, false, true},
		{"withheld_with_valid_items", func(p map[string]any) { p["withheld"] = 1 }, 2, false, false},
		{"errors_with_valid_items", func(p map[string]any) { p["errors"] = []any{"source unavailable"} }, 2, false, false},
		{"partial_mode", func(p map[string]any) { p["snapshot_mode"], p["snapshot_complete"] = "partial", false }, 2, false, false},
		{"partial_withheld_and_errors", func(p map[string]any) {
			p["snapshot_mode"], p["snapshot_complete"], p["withheld"], p["errors"] = "partial", false, 1, []any{"producer declared partial snapshot"}
		}, 2, false, false},
		{"duplicate_item", func(p map[string]any) { items := p["items"].([]any); p["items"] = append(items, items[0]) }, 2, false, false},
		{"dangling_venue", func(p map[string]any) { p["items"] = []any{p["items"].([]any)[1]} }, 0, false, false},
		{"locally_invalid_event", func(p map[string]any) { p["items"].([]any)[1].(map[string]any)["private_person"] = "rejected" }, 1, false, false},
		{"locally_all_dropped", func(p map[string]any) { p["items"] = []any{map[string]any{"private_person": "rejected"}} }, 0, false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := retirementPackPage(t)
			c.mutate(p)
			got, err := retirementReadPages(t, []map[string]any{p})
			if errors.Is(err, ErrProductRefused) != c.refused || err != nil && !c.refused || got.Complete != c.complete || len(got.Items) != c.count {
				t.Fatalf("completion/refusal/count contract: complete=%v items=%d err=%v", got.Complete, len(got.Items), err)
			}
		})
	}
}

func TestRetirementROEDUHTTPResponseBounds(t *testing.T) {
	for _, status := range []int{301, 302, 400, 401, 403, 429, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := &RoeduClient{BaseURL: "https://producer.invalid", APIKey: "synthetic-contract-key", HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Location": []string{"https://elsewhere.invalid"}}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})}}
			got, err := c.Read(context.Background(), "Cluj-Napoca")
			if err == nil || got.Complete || calls != 1 {
				t.Fatal("failed/redirected producer response accepted or followed")
			}
		})
	}
	for _, body := range []string{"{} {}", "null", "[]", strings.Repeat(" ", (8<<20)+1)} {
		t.Run("invalid_body", func(t *testing.T) {
			c := &RoeduClient{BaseURL: "https://producer.invalid", HTTP: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
			})}}
			if got, err := c.Read(context.Background(), ""); err == nil || got.Complete {
				t.Fatal("trailing/nonobject/oversized body accepted")
			}
		})
	}
}
