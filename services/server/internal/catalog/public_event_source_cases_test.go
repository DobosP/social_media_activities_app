package catalog_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/discovery"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func eventCaseStore(t *testing.T) *catalog.Service {
	t.Helper()
	f := flag.Lookup("catalog-test-dsn")
	if f == nil {
		t.Fatal("catalog fixture flag missing")
	}
	return catalog.New(testdb.New(t, f.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) }))
}

func eventCaseCreate(t *testing.T, s *catalog.Service, title, lifecycle string, place *int64, starts time.Time) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRow(context.Background(), `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,activity_type_id,source_category,source_confidence,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_price_min,source_price_max,source_currency,source_is_free,source_availability) VALUES($1,'',$2,NULL,'','manual','','','','',now(),now(),$3,NULL,'',NULL,false,$4,false,'','','','','','','',NULL,NULL,'',NULL,'') RETURNING id`, title, starts, place, lifecycle).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func eventCaseRead(t *testing.T, s *catalog.Service, path string, actor *platform.Actor) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	discovery.New(s.DB, s, nil, nil).Register(mux)
	r := httptest.NewRequest("GET", "https://fixture.local"+path, nil)
	if actor != nil {
		r = platform.WithActor(r, *actor)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal("invalid JSON", path, w.Code, err)
	}
	return w, body
}

func eventCaseList(t *testing.T, s *catalog.Service, path string, actor *platform.Actor) (map[string]any, []map[string]any) {
	t.Helper()
	w, body := eventCaseRead(t, s, path, actor)
	if w.Code != 200 {
		t.Fatal("event list status", path, w.Code, body)
	}
	var rows []map[string]any
	for _, row := range body["results"].([]any) {
		rows = append(rows, row.(map[string]any))
	}
	return body, rows
}

func eventCaseTitles(t *testing.T, s *catalog.Service, path string, actor *platform.Actor) []string {
	t.Helper()
	_, rows := eventCaseList(t, s, path, actor)
	out := []string{}
	for _, row := range rows {
		out = append(out, row["title"].(string))
	}
	return out
}

func TestEventSourceCaseAnonymousUpcomingListAndDetail(t *testing.T) {
	for _, name := range []string{"list", "detail"} {
		t.Run(name, func(t *testing.T) {
			s := eventCaseStore(t)
			place := testdb.Place(t, s.DB, "City Library", "osm")
			title := "Chess club night"
			if name == "detail" {
				title = "Reading circle"
			}
			id := eventCaseCreate(t, s, title, "scheduled", &place, time.Now().Add(48*time.Hour))
			if name == "list" {
				if got := eventCaseTitles(t, s, "/api/v1/events/", nil); !reflect.DeepEqual(got, []string{title}) {
					t.Fatal("anonymous upcoming event missing", got)
				}
			} else {
				w, body := eventCaseRead(t, s, fmt.Sprintf("/api/v1/events/%d/", id), nil)
				if w.Code != 200 || body["title"] != title {
					t.Fatal("anonymous detail source title/status", w.Code, body)
				}
			}
		})
	}
}

func TestEventSourceCaseAnonymousPublicationAndPII(t *testing.T) {
	t.Run("unpublished_user_place", func(t *testing.T) {
		s := eventCaseStore(t)
		ctx := context.Background()
		pending := testdb.Place(t, s.DB, "Pending backyard court", "user")
		public := testdb.Place(t, s.DB, "City Library", "osm")
		proposer := testdb.Actor(t, s.DB, "event-case-proposer", "adult")
		if _, err := s.DB.Exec(ctx, `INSERT INTO social_userplaceproposal(place_id,proposer_id,required_confirmations,status,created_at,published_at) VALUES($1,$2,3,'pending',now(),NULL)`, pending, proposer.ID); err != nil {
			t.Fatal(err)
		}
		id := eventCaseCreate(t, s, "At pending place", "scheduled", &pending, time.Now().Add(48*time.Hour))
		eventCaseCreate(t, s, "At public place", "scheduled", &public, time.Now().Add(48*time.Hour))
		if got := eventCaseTitles(t, s, "/api/v1/events/", nil); !reflect.DeepEqual(got, []string{"At public place"}) {
			t.Fatal("unpublished user place leaked", got)
		}
		for _, suffix := range []string{"", "?include_past=true"} {
			w, _ := eventCaseRead(t, s, fmt.Sprintf("/api/v1/events/%d/%s", id, suffix), nil)
			if w.Code != 404 {
				t.Fatal("pending venue detail/history leaked", w.Code)
			}
		}
	})
	t.Run("serializer_key_subset", func(t *testing.T) {
		s := eventCaseStore(t)
		place := testdb.Place(t, s.DB, "City Library", "osm")
		eventCaseCreate(t, s, "Public event", "scheduled", &place, time.Now().Add(48*time.Hour))
		_, rows := eventCaseList(t, s, "/api/v1/events/", nil)
		// Exact field allowlist from the preserved EventSerializer.Meta.fields.
		allowed := map[string]bool{}
		for _, key := range []string{"id", "title", "description", "starts_at", "ends_at", "url", "source", "source_category", "lifecycle_status", "source_confidence", "source_recurrence", "source_timezone", "source_price_min", "source_price_max", "source_currency", "source_is_free", "source_availability", "attribution", "license_name", "provenance_url", "attribution_credit", "place", "place_name", "activity"} {
			allowed[key] = true
		}
		if len(rows) != 1 {
			t.Fatal("PII proof did not exercise an event")
		}
		for key := range rows[0] {
			if !allowed[key] {
				t.Fatal("anonymous event exposed undeclared/relational field", key)
			}
		}
	})
}

func TestEventSourceCaseLifecycleHistoryAndRetractions(t *testing.T) {
	for _, lifecycle := range []string{"cancelled", "postponed", "removed", "moved_online", "expired", "unknown"} {
		t.Run(lifecycle, func(t *testing.T) {
			s := eventCaseStore(t)
			place := testdb.Place(t, s.DB, "City Library", "osm")
			id := eventCaseCreate(t, s, "Cancelled source event", lifecycle, &place, time.Now().Add(48*time.Hour))
			body, _ := eventCaseList(t, s, "/api/v1/events/", nil)
			if body["count"] != float64(0) {
				t.Fatal("non-discoverable source lifecycle became public upcoming", lifecycle, body)
			}
			history, rows := eventCaseList(t, s, "/api/v1/events/?include_past=true", nil)
			if history["count"] != float64(1) || len(rows) != 1 || rows[0]["lifecycle_status"] != lifecycle {
				t.Fatal("history lost upstream lifecycle truth", lifecycle, history)
			}
			w, detail := eventCaseRead(t, s, fmt.Sprintf("/api/v1/events/%d/?include_past=true", id), nil)
			if w.Code != 200 || detail["lifecycle_status"] != lifecycle {
				t.Fatal("history detail lost lifecycle", w.Code, detail)
			}
		})
	}
	for _, flag := range []string{"is_tombstone", "is_import_held"} {
		t.Run(flag, func(t *testing.T) {
			s := eventCaseStore(t)
			place := testdb.Place(t, s.DB, "City Library", "osm")
			id := eventCaseCreate(t, s, "Retracted event", "scheduled", &place, time.Now().Add(48*time.Hour))
			// flag is a fixed literal from the two source parameter values.
			if _, err := s.DB.Exec(context.Background(), `UPDATE events_event SET `+flag+`=true WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			body, rows := eventCaseList(t, s, "/api/v1/events/?include_past=true", nil)
			if body["count"] != float64(0) || len(rows) != 0 {
				t.Fatal("retracted/held history event leaked", flag)
			}
			w, _ := eventCaseRead(t, s, fmt.Sprintf("/api/v1/events/%d/?include_past=true", id), nil)
			if w.Code != 404 {
				t.Fatal("retracted/held history detail leaked", flag, w.Code)
			}
		})
	}
}

func TestEventSourceCasePastDefaultAndHistory(t *testing.T) {
	s := eventCaseStore(t)
	place := testdb.Place(t, s.DB, "City Library", "osm")
	eventCaseCreate(t, s, "Upcoming", "scheduled", &place, time.Now().Add(48*time.Hour))
	eventCaseCreate(t, s, "Old one", "scheduled", nil, time.Now().Add(-24*time.Hour))
	if got := eventCaseTitles(t, s, "/api/v1/events/", nil); !reflect.DeepEqual(got, []string{"Upcoming"}) {
		t.Fatal("default returned past event", got)
	}
	if got := eventCaseTitles(t, s, "/api/v1/events/?include_past=true", nil); !reflect.DeepEqual(got, []string{"Old one", "Upcoming"}) {
		t.Fatal("history omitted past event", got)
	}
}

func TestEventSourceCaseExactPublicSourceFacts(t *testing.T) {
	s := eventCaseStore(t)
	place := testdb.Place(t, s.DB, "City Library", "osm")
	id := eventCaseCreate(t, s, "Public RO-EDU facts", "rescheduled", &place, time.Now().Add(48*time.Hour))
	if _, err := s.DB.Exec(context.Background(), `UPDATE events_event SET source='scraper',source_category='concert',source_confidence=0.98,source_recurrence='FREQ=WEEKLY',source_timezone='Europe/Bucharest',source_price_min=20.00,source_price_max=50.00,source_currency='RON',source_is_free=false,source_availability='limited' WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	_, rows := eventCaseList(t, s, "/api/v1/events/", nil)
	if len(rows) != 1 {
		t.Fatal("source facts fixture not returned")
	}
	for key, value := range map[string]any{"source_category": "concert", "lifecycle_status": "rescheduled", "source_confidence": 0.98, "source_recurrence": "FREQ=WEEKLY", "source_timezone": "Europe/Bucharest", "source_price_min": "20.00", "source_price_max": "50.00", "source_currency": "RON", "source_is_free": false, "source_availability": "limited"} {
		if rows[0][key] != value {
			t.Fatal("exact public source fact changed", key, rows[0][key], value)
		}
	}
}

func TestEventSourceCaseDateWindowAndWholeBareDay(t *testing.T) {
	t.Run("from_to_window", func(t *testing.T) {
		s := eventCaseStore(t)
		place := testdb.Place(t, s.DB, "City Library", "osm")
		now := time.Now()
		for _, e := range []struct {
			title string
			days  int
		}{{"Tomorrow", 1}, {"Next week", 6}, {"Far out", 20}} {
			eventCaseCreate(t, s, e.title, "scheduled", &place, now.AddDate(0, 0, e.days))
		}
		path := "/api/v1/events/?from=" + now.AddDate(0, 0, 3).Format("2006-01-02") + "&to=" + now.AddDate(0, 0, 10).Format("2006-01-02")
		if got := eventCaseTitles(t, s, path, nil); !reflect.DeepEqual(got, []string{"Next week"}) {
			t.Fatal("start window widened or narrowed", got)
		}
	})
	t.Run("to_includes_evening", func(t *testing.T) {
		s := eventCaseStore(t)
		place := testdb.Place(t, s.DB, "City Library", "osm")
		loc, err := time.LoadLocation("Europe/Bucharest")
		if err != nil {
			t.Fatal(err)
		}
		day := time.Now().In(loc).AddDate(0, 0, 4)
		evening := time.Date(day.Year(), day.Month(), day.Day(), 18, 0, 0, 0, loc)
		eventCaseCreate(t, s, "Evening session", "scheduled", &place, evening)
		if got := eventCaseTitles(t, s, "/api/v1/events/?to="+evening.Format("2006-01-02"), nil); !reflect.DeepEqual(got, []string{"Evening session"}) {
			t.Fatal("bare date omitted same-day evening", got)
		}
	})
}

func TestEventSourceCaseInvalidDatesAndGeoAreNamed400(t *testing.T) {
	s := eventCaseStore(t)
	place := testdb.Place(t, s.DB, "City Library", "osm")
	eventCaseCreate(t, s, "Somewhere", "scheduled", &place, time.Now().Add(48*time.Hour))
	for _, raw := range []string{"next-friday", "2026-13-01", "2026-02-30", "2026-07-16T25:00", "2026-07-16T12:60"} {
		t.Run(raw, func(t *testing.T) {
			w, body := eventCaseRead(t, s, "/api/v1/events/?from="+url.QueryEscape(raw), nil)
			if _, keyed := body["from"]; w.Code != 400 || !keyed {
				t.Fatal("invalid from must be clear named400", raw, w.Code, body)
			}
		})
	}
	t.Run("invalid_to", func(t *testing.T) {
		w, body := eventCaseRead(t, s, "/api/v1/events/?to=2026-13-01", nil)
		if _, keyed := body["to"]; w.Code != 400 || !keyed {
			t.Fatal("invalid to must be named400", w.Code, body)
		}
	})
	t.Run("public_activities", func(t *testing.T) {
		w, _ := eventCaseRead(t, s, "/api/v1/discovery/public/activities/?to=2026-13-01", nil)
		if w.Code != 400 {
			t.Fatal("public activity invalid bound", w.Code)
		}
	})
	t.Run("bad_near", func(t *testing.T) {
		w, _ := eventCaseRead(t, s, "/api/v1/events/?near_lat=abc&near_lon=23.6", nil)
		if w.Code != 400 {
			t.Fatal("invalid coordinates silently widened", w.Code)
		}
	})
	t.Run("bad_radius", func(t *testing.T) {
		w, body := eventCaseRead(t, s, "/api/v1/events/?near_lat=46.77&near_lon=23.6&radius_m=abc", nil)
		if _, keyed := body["radius_m"]; w.Code != 400 || !keyed {
			t.Fatal("invalid radius must be named400", w.Code, body)
		}
	})
}

func TestEventSourceCaseQueryErrorCorrectionHasFixedKeysAndPreservesStatuses(t *testing.T) {
	s := eventCaseStore(t)
	place := testdb.Place(t, s.DB, "Correction regression venue", "osm")
	id := eventCaseCreate(t, s, "Correction regression event", "scheduled", &place, time.Now().Add(48*time.Hour))
	for _, prefix := range []string{"/api/events/", "/api/v1/events/", fmt.Sprintf("/api/v1/events/%d/", id)} {
		for _, c := range []struct{ query, key string }{
			{"from=synthetic-invalid-marker", "from"},
			{"to=synthetic-invalid-marker", "to"},
			{"near_lon=synthetic-invalid-marker&near_lat=46.77", "near_lon"},
			{"near_lon=23.6&near_lat=synthetic-invalid-marker", "near_lat"},
			{"near_lon=23.6&near_lat=46.77&radius_m=synthetic-invalid-marker", "radius_m"},
		} {
			w, body := eventCaseRead(t, s, prefix+"?"+c.query, nil)
			_, keyed := body[c.key]
			if w.Code != 400 || !keyed || len(body) != 1 || strings.Contains(w.Body.String(), "synthetic-invalid-marker") {
				t.Fatal("query correction leaked input or changed field/status", prefix, c.key, w.Code, body)
			}
		}
	}
	for _, path := range []string{"/api/v1/events/not-an-id/", "/api/v1/events/0/", "/api/v1/events/999999999/"} {
		w, _ := eventCaseRead(t, s, path, nil)
		if w.Code != 404 {
			t.Fatal("query correction changed missing-detail status", path, w.Code)
		}
	}
	w, body := eventCaseRead(t, s, "/api/v1/events/?from=x&from=y", nil)
	if w.Code != 400 || body["detail"] != "Invalid request." {
		t.Fatal("generic bounded-query error changed", w.Code, body)
	}
	s.DB.Close()
	w, body = eventCaseRead(t, s, "/api/v1/events/", nil)
	if w.Code != 503 || body["detail"] != "Service unavailable." {
		t.Fatal("database failure became a keyed query error", w.Code, body)
	}
}

func TestEventSourceCaseSearchCityAndNearQueries(t *testing.T) {
	t.Run("search_all_three_fields_and_short_query", func(t *testing.T) {
		s := eventCaseStore(t)
		city := testdb.Place(t, s.DB, "City Library", "osm")
		palace := testdb.Place(t, s.DB, "Chess Palace", "osm")
		gym := testdb.Place(t, s.DB, "Gym", "osm")
		a := eventCaseCreate(t, s, "Open practice", "scheduled", &city, time.Now().Add(48*time.Hour))
		eventCaseCreate(t, s, "Quiet reading", "scheduled", &palace, time.Now().Add(48*time.Hour))
		eventCaseCreate(t, s, "Unrelated", "scheduled", &gym, time.Now().Add(48*time.Hour))
		if _, err := s.DB.Exec(context.Background(), `UPDATE events_event SET description='bring a chess clock' WHERE id=$1`, a); err != nil {
			t.Fatal(err)
		}
		got := eventCaseTitles(t, s, "/api/v1/events/?q=chess", nil)
		sort.Strings(got)
		if !reflect.DeepEqual(got, []string{"Open practice", "Quiet reading"}) {
			t.Fatal("description/venue search changed", got)
		}
		if got := eventCaseTitles(t, s, "/api/v1/events/?q=c", nil); len(got) != 3 {
			t.Fatal("one-character query must be ignored", got)
		}
		// The source declaration names title in its contract; exercise it independently too.
		if got := eventCaseTitles(t, s, "/api/v1/events/?q=unrelated", nil); !reflect.DeepEqual(got, []string{"Unrelated"}) {
			t.Fatal("title search changed", got)
		}
	})
	t.Run("case_insensitive_city", func(t *testing.T) {
		s := eventCaseStore(t)
		cluj := testdb.Place(t, s.DB, "City Library", "osm")
		other := testdb.Place(t, s.DB, "Other hall", "osm")
		if _, err := s.DB.Exec(context.Background(), `UPDATE places_place SET address_city='Bucharest' WHERE id=$1`, other); err != nil {
			t.Fatal(err)
		}
		eventCaseCreate(t, s, "In Cluj", "scheduled", &cluj, time.Now().Add(48*time.Hour))
		eventCaseCreate(t, s, "Elsewhere", "scheduled", &other, time.Now().Add(48*time.Hour))
		if got := eventCaseTitles(t, s, "/api/v1/events/?city=cluj-napoca", nil); !reflect.DeepEqual(got, []string{"In Cluj"}) {
			t.Fatal("city filter changed", got)
		}
	})
	t.Run("nearest_order_then_radius", func(t *testing.T) {
		s := eventCaseStore(t)
		near := testdb.Place(t, s.DB, "Near venue", "osm")
		far := testdb.Place(t, s.DB, "Far venue", "osm")
		if _, err := s.DB.Exec(context.Background(), `UPDATE places_place SET location=ST_SetSRID(ST_MakePoint(23.6236,CASE WHEN id=$1 THEN 46.7712 ELSE 46.8702 END),4326) WHERE id IN($1,$2)`, near, far); err != nil {
			t.Fatal(err)
		}
		eventCaseCreate(t, s, "Far event", "scheduled", &far, time.Now().Add(24*time.Hour))
		eventCaseCreate(t, s, "Near event", "scheduled", &near, time.Now().Add(48*time.Hour))
		base := "/api/v1/events/?near_lat=46.7712&near_lon=23.6236"
		if got := eventCaseTitles(t, s, base, nil); !reflect.DeepEqual(got, []string{"Near event", "Far event"}) {
			t.Fatal("nearest ordering ignored geometry", got)
		}
		if got := eventCaseTitles(t, s, base+"&radius_m=2000", nil); !reflect.DeepEqual(got, []string{"Near event"}) {
			t.Fatal("radius filter ignored geography", got)
		}
	})
}

func eventCaseReliability(t *testing.T, s *catalog.Service, id int64, want any) {
	t.Helper()
	got, err := s.EventReliability(context.Background(), id)
	if err != nil || got != want {
		t.Fatal("counts-only event reliability", got, want, err)
	}
}

func eventCaseReport(t *testing.T, s *catalog.Service, id int64, actor platform.Actor, kind string, want bool) {
	t.Helper()
	got, err := s.ReportEvent(context.Background(), actor, id, kind)
	if err != nil || got != want {
		t.Fatal("event report created state", got, want, err)
	}
}

func TestEventSourceCaseReportQuorumDecayReplayAndCohorts(t *testing.T) {
	for _, mode := range []string{"mixed_kind_quorum", "decay", "idempotent", "shared_child_adult"} {
		t.Run(mode, func(t *testing.T) {
			s := eventCaseStore(t)
			ctx := context.Background()
			id := eventCaseCreate(t, s, "Chess night", "scheduled", nil, time.Now().Add(72*time.Hour))
			eventCaseReliability(t, s, id, nil)
			a := testdb.Actor(t, s.DB, "event-report-a", "adult")
			eventCaseReport(t, s, id, a, "cancelled", true)
			if mode == "idempotent" {
				eventCaseReport(t, s, id, a, "moved", false)
				var count int
				if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM events_eventreport WHERE event_id=$1 AND reporter_id=$2`, id, a.ID).Scan(&count); err != nil || count != 1 {
					t.Fatal("same-window duplicate row", count, err)
				}
				return
			}
			b := testdb.Actor(t, s.DB, "event-report-b", "adult")
			eventCaseReport(t, s, id, b, "cancelled", true)
			eventCaseReliability(t, s, id, nil)
			cohort, kind := "adult", "moved"
			if mode == "shared_child_adult" {
				cohort, kind = "child", "cancelled"
			}
			c := testdb.Actor(t, s.DB, "event-report-c", cohort)
			eventCaseReport(t, s, id, c, kind, true)
			eventCaseReliability(t, s, id, "unverified")
			var count int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM events_eventreport WHERE event_id=$1`, id).Scan(&count); err != nil || count != 3 {
				t.Fatal("mixed reporters/kinds not same tally", count, err)
			}
			_, rows := eventCaseList(t, s, "/api/v1/events/", nil)
			for _, key := range []string{"reports", "reporter", "reporter_id", "reporters", "cohort", "age_band", "recent_report_n"} {
				if _, exists := rows[0][key]; exists {
					t.Fatal("counts overlay exposed reporter/cohort identity", key)
				}
			}
			if mode == "decay" {
				if _, err := s.DB.Exec(ctx, `UPDATE events_eventreport SET created_at=now()-interval '30 days' WHERE event_id=$1`, id); err != nil {
					t.Fatal(err)
				}
				eventCaseReliability(t, s, id, nil)
			}
		})
	}
}

func TestEventSourceCaseReportParticipationKindAndCrossEventRate(t *testing.T) {
	t.Run("participation_and_known_kind", func(t *testing.T) {
		s := eventCaseStore(t)
		ctx := context.Background()
		id := eventCaseCreate(t, s, "Chess night", "scheduled", nil, time.Now().Add(72*time.Hour))
		u := testdb.Actor(t, s.DB, "event-unverified", "adult")
		if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, u.ID); err != nil {
			t.Fatal(err)
		}
		u.IdentityVerified = false
		if created, err := s.ReportEvent(ctx, u, id, "cancelled"); created || !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("unverified event reporter admitted", created, err)
		}
		good := testdb.Actor(t, s.DB, "event-badkind", "adult")
		if created, err := s.ReportEvent(ctx, good, id, "exploded"); created || !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("unknown report kind admitted", created, err)
		}
		var count int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM events_eventreport WHERE event_id=$1`, id).Scan(&count); err != nil || count != 0 {
			t.Fatal("rejected report wrote state", count, err)
		}
	})
	t.Run("cross_event_limit_one", func(t *testing.T) {
		s := eventCaseStore(t)
		s.RatePolicies = map[string]budgets.Policy{"event_report": {Limit: 1, Window: time.Hour}}
		u := testdb.Actor(t, s.DB, "event-rate-limited", "adult")
		a := eventCaseCreate(t, s, "a", "scheduled", nil, time.Now().Add(72*time.Hour))
		b := eventCaseCreate(t, s, "b", "scheduled", nil, time.Now().Add(72*time.Hour))
		eventCaseReport(t, s, a, u, "cancelled", true)
		eventCaseReport(t, s, b, u, "cancelled", false)
		var first, second int
		if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE event_id=$1),count(*) FILTER(WHERE event_id=$2) FROM events_eventreport WHERE reporter_id=$3`, a, b, u.ID).Scan(&first, &second); err != nil || first != 1 || second != 0 {
			t.Fatal("cross-event limit did not suppress second row", first, second, err)
		}
	})
}

func TestEventSourceCaseReportOverlaySurvivesActualSyntheticReingest(t *testing.T) {
	s := eventCaseStore(t)
	ctx := context.Background()
	place := testdb.Place(t, s.DB, "Hall", "osm")
	var feed int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO events_eventfeed(name,url,is_active,last_status,created_at,place_id,activity_type_id) VALUES('Overlay fixture','https://fixture.invalid/overlay.ics',true,'',now(),$1,NULL) RETURNING id`, place).Scan(&feed); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	when := now.AddDate(0, 0, 5).UTC().Format("20060102T150405Z")
	config := jobs.DefaultConfig()
	config.Now = func() time.Time { return now }
	config.FetchFeed = func(context.Context, string) ([]byte, error) {
		return []byte("BEGIN:VCALENDAR\nBEGIN:VEVENT\nUID:talk-1\nSUMMARY:Recurring talk\nDTSTART:" + when + "\nEND:VEVENT\nEND:VCALENDAR\n"), nil
	}
	r := jobs.New(s.DB, config)
	if summary, err := r.SyncFeeds(ctx); err != nil || summary["events"] != 1 || summary["failed_feeds"] != 0 {
		t.Fatal("synthetic upsert did not create fixture", summary, err)
	}
	var id int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM events_event WHERE external_id=$1 AND source='ical'`, fmt.Sprintf("feed%d:talk-1", feed)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		eventCaseReport(t, s, id, testdb.Actor(t, s.DB, fmt.Sprintf("event-reingest-%d", i), "adult"), "cancelled", true)
	}
	eventCaseReliability(t, s, id, "unverified")
	if summary, err := r.SyncFeeds(ctx); err != nil || summary["events"] != 1 || summary["failed_feeds"] != 0 {
		t.Fatal("synthetic reingest failed", summary, err)
	}
	var got int64
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT id FROM events_event WHERE external_id=$1 AND source='ical'`, fmt.Sprintf("feed%d:talk-1", feed)).Scan(&got); err != nil || got != id {
		t.Fatal("reingest replaced event identity", got, id, err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM events_eventreport WHERE event_id=$1`, id).Scan(&count); err != nil || count != 3 {
		t.Fatal("reingest clobbered independent report rows", count, err)
	}
	eventCaseReliability(t, s, id, "unverified")
}

func TestEventSourceCaseAuthenticatedLegacyAPIVisibilityCreditAndFacts(t *testing.T) {
	for _, mode := range []string{"upcoming_only", "cancelled", "moved_online", "attribution_credit", "safe_facts"} {
		t.Run(mode, func(t *testing.T) {
			s := eventCaseStore(t)
			ctx := context.Background()
			u := testdb.Actor(t, s.DB, "event-legacy-actor", "adult")
			place := testdb.Place(t, s.DB, "Hall", "osm")
			status := "scheduled"
			if mode == "cancelled" || mode == "moved_online" {
				status = mode
			}
			id := eventCaseCreate(t, s, "RO-EDU event", status, &place, time.Now().Add(24*time.Hour))
			if mode == "upcoming_only" {
				if _, err := s.DB.Exec(ctx, `UPDATE events_event SET title='Chess club night' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				eventCaseCreate(t, s, "Old", "scheduled", nil, time.Now().Add(-24*time.Hour))
				if got := eventCaseTitles(t, s, "/api/events/", &u); !reflect.DeepEqual(got, []string{"Chess club night"}) {
					t.Fatal("authenticated source API upcoming-only contract", got)
				}
				body, _ := eventCaseList(t, s, "/api/events/?include_past=true", &u)
				if body["count"] != float64(2) {
					t.Fatal("authenticated history omitted old event", body)
				}
				return
			}
			if mode == "cancelled" || mode == "moved_online" {
				if _, err := s.DB.Exec(ctx, `UPDATE events_event SET source='scraper',external_id=$2 WHERE id=$1`, id, "roedu:"+mode+"-api"); err != nil {
					t.Fatal(err)
				}
				body, _ := eventCaseList(t, s, "/api/events/", &u)
				if body["count"] != float64(0) {
					t.Fatal("non-live authenticated source event became upcoming", mode, body)
				}
				body, rows := eventCaseList(t, s, "/api/events/?include_past=true", &u)
				if body["count"] != float64(1) || len(rows) != 1 || rows[0]["lifecycle_status"] != mode {
					t.Fatal("authenticated history lost source lifecycle", mode, body)
				}
				return
			}
			if mode == "attribution_credit" {
				if _, err := s.DB.Exec(ctx, `UPDATE places_place SET source='roedu' WHERE id=$1`, place); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB.Exec(ctx, `UPDATE events_event SET source='scraper',external_id='roedu:e-api',attribution='RO-EDU',license_name='CC BY 4.0',provenance_url='https://data.example/events/e-api' WHERE id=$1`, id); err != nil {
					t.Fatal(err)
				}
				_, rows := eventCaseList(t, s, "/api/events/", &u)
				want := map[string]any{"attribution": "RO-EDU", "license_name": "CC BY 4.0", "provenance_url": "https://data.example/events/e-api"}
				if len(rows) != 1 || !reflect.DeepEqual(rows[0]["attribution_credit"], want) {
					t.Fatal("exact authenticated source credit", rows)
				}
				return
			}
			if _, err := s.DB.Exec(ctx, `UPDATE events_event SET source='scraper',external_id='roedu:e-safe-facts',source_recurrence='FREQ=WEEKLY',source_timezone='Europe/Bucharest',source_price_min=20.00,source_price_max=50.00,source_currency='RON',source_is_free=false,source_availability='available' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
			_, rows := eventCaseList(t, s, "/api/events/", &u)
			if len(rows) != 1 {
				t.Fatal("safe facts fixture missing")
			}
			for key, want := range map[string]any{"source_recurrence": "FREQ=WEEKLY", "source_timezone": "Europe/Bucharest", "source_price_min": "20.00", "source_price_max": "50.00", "source_currency": "RON", "source_is_free": false, "source_availability": "available"} {
				if rows[0][key] != want {
					t.Fatal("authenticated safe source fact changed", key, rows[0][key], want)
				}
			}
		})
	}
}
