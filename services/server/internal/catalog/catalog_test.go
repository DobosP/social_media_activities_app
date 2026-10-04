package catalog

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

var catalogTestDSN = flag.String("catalog-test-dsn", "", "Explicit disposable PostgreSQL fixture server")

func fixture(t *testing.T, seed bool) *Service {
	var callback testdb.Seed
	if seed {
		callback = func(ctx context.Context, db *pgxpool.Pool) error { return New(db).Migrate(ctx) }
	}
	return New(testdb.New(t, *catalogTestDSN, callback))
}
func user(t *testing.T, s *Service, name, cohort string) platform.Actor {
	t.Helper()
	var a platform.Actor
	a.Username = name
	a.DisplayName = name
	a.Cohort = cohort
	a.AgeBand = "adult"
	a.IsActive = true
	a.IdentityVerified = true
	err := s.DB.QueryRow(context.Background(), `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,'adult',$2,true,now(),'user',true,false,now()) RETURNING id,public_id::text`, name, cohort).Scan(&a.ID, &a.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func placeFixture(t *testing.T, s *Service, name, source string, lon, lat float64) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRow(context.Background(), `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES($1,$2,'',NULL,'',ST_SetSRID(ST_MakePoint($3,$4),4326),'{"amenity":"library"}','Strada Test','2','Cluj-Napoca','','RO','24/7','{"mo":[[0,1440]],"tu":[[0,1440]],"we":[[0,1440]],"th":[[0,1440]],"fr":[[0,1440]],"sa":[[0,1440]],"su":[[0,1440]]}','','https://example.org',now(),now(),'Public attribution','CC-BY-4.0','https://example.org/source') RETURNING id`, name, source, lon, lat).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func eventFixture(t *testing.T, s *Service, title, status string, place *int64, when time.Time) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRow(context.Background(), `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,attribution,license_name,provenance_url,created_at,updated_at,place_id,activity_type_id,source_category,source_confidence,is_import_held,lifecycle_status,is_tombstone,source_venue_id,source_city,source_pack_id,source_snapshot_id,source_release_id,source_recurrence,source_timezone,source_price_min,source_price_max,source_currency,source_is_free,source_availability) VALUES($1,'Description keyword',$2,NULL,'https://example.org/event','manual','','Credit','CC-BY-4.0','https://example.org/facts',now(),now(),$3,NULL,'',NULL,false,$4,false,'','','','','','','Europe/Bucharest',20.00,50.00,'RON',false,'limited') RETURNING id`, title, when, place, status).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func request(t *testing.T, s *Service, path string, a *platform.Actor) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	s.Register(mux)
	r := httptest.NewRequest("GET", "http://example.org"+path, nil)
	if a != nil {
		r = platform.WithActor(r, *a)
	}
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	return out
}
func jsonObject(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatal(err, string(raw))
	}
	return obj
}

func TestOpeningHoursCommonGrammarAndUnknown(t *testing.T) {
	schedule := ParseOpeningHours("Mo-Fr 09:00-18:00; Sa 22:00-02:00; Su off")
	if schedule == nil || !reflect.DeepEqual(schedule["sa"], [][2]int{{1320, 1440}}) || !reflect.DeepEqual(schedule["su"], [][2]int{{0, 120}}) {
		t.Fatal(schedule)
	}
	when := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	if open := OpenAt(schedule, when); open == nil || !*open {
		t.Fatal(open)
	}
	for _, value := range []string{"", "unknown", "Mo 25:00-18:00", "bad-day 09:00-18:00", "Mo 10:15+"} {
		if ParseOpeningHours(value) != nil {
			t.Fatal("guessed unknown hours", value)
		}
	}
}
func TestEventBoundsUseLocalCalendarDays(t *testing.T) {
	from, err := Bound("2026-10-04", false)
	if err != nil {
		t.Fatal(err)
	}
	to, err := Bound("2026-10-04", true)
	if err != nil || to.Sub(*from) != 24*time.Hour || from.UTC().Hour() != 21 {
		t.Fatal(from, to, err)
	}
}
func TestPostgresNativeReferenceSeedIsIdempotent(t *testing.T) {
	s := fixture(t, false)
	ctx := context.Background()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM taxonomy_activitytype WHERE slug='basketball' AND is_active`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM django_content_type WHERE app_label='social' AND model='post'`).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE taxonomy_activitytype SET name='Operator edit' WHERE slug='basketball'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := s.DB.QueryRow(ctx, `SELECT name FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&name); err != nil || name != "Operator edit" {
		t.Fatal("seed overwrote operator data", name, err)
	}
}
func TestPostgresEventVisibilityAndCompleteQuery(t *testing.T) {
	s := fixture(t, true)
	p := placeFixture(t, s, "Public park", "osm", 23.6, 46.77)
	pending := placeFixture(t, s, "Pending venue", "user", 23.7, 46.8)
	future := time.Now().Add(time.Hour)
	live := eventFixture(t, s, "Live event", "scheduled", &p, future)
	sold := eventFixture(t, s, "Sold-out event", "sold_out", &p, future)
	past := eventFixture(t, s, "Old event", "scheduled", &p, time.Now().Add(-time.Hour))
	cancelled := eventFixture(t, s, "Cancelled event", "cancelled", &p, future)
	_ = eventFixture(t, s, "Hidden venue event", "scheduled", &pending, future)
	for _, path := range []string{"/api/v1/events/", "/api/events/?city=cluj-napoca&place=" + strconvInt(p), "/api/events/?near_lon=23.6&near_lat=46.77&radius_m=2000"} {
		out := request(t, s, path, nil)
		if out.Code != 200 {
			t.Fatal(path, out.Code, out.Body.String())
		}
		body := jsonObject(t, out.Body.Bytes())
		if body["count"].(float64) != 2 {
			t.Fatal(path, body)
		}
	}
	for _, id := range []int64{past, cancelled} {
		out := request(t, s, "/api/events/"+strconvInt(id)+"/", nil)
		if out.Code != 404 {
			t.Fatal("detail widened default queryset", out.Code, out.Body.String())
		}
		out = request(t, s, "/api/events/"+strconvInt(id)+"/?include_past=true", nil)
		if out.Code != 200 {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	for _, id := range []int64{live, sold} {
		out := request(t, s, "/api/events/"+strconvInt(id)+"/", nil)
		if out.Code != 200 {
			t.Fatal(out.Code, out.Body.String())
		}
		body := jsonObject(t, out.Body.Bytes())
		if body["source_price_min"] != "20.00" {
			t.Fatal("lost exact price", body)
		}
	}
	out := request(t, s, "/api/events/?q=%%25", nil)
	if out.Code != 400 {
		t.Fatal("malformed query accepted", out.Code)
	}
	out = request(t, s, "/api/events/?near_lon=not-a-number&near_lat=46", nil)
	if out.Code != 400 {
		t.Fatal(out.Code, out.Body.String())
	}
}
func strconvInt(id int64) string { return fmt.Sprint(id) }
func TestPostgresPlacesGeoJSONFiltersCorrectionsAndClosure(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	id := placeFixture(t, s, "Original library", "osm", 23.6, 46.77)
	_ = placeFixture(t, s, "Other place", "osm", 24.6, 47.7)
	_ = placeFixture(t, s, "Hidden private proposal", "user", 23.6, 46.77)
	a := user(t, s, "catalog-reporter", "adult")
	var typeID int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,created_at,updated_at) VALUES($1,$2,'manual',0.9,'user','',false,now(),now())`, id, typeID); err != nil {
		t.Fatal(err)
	}
	out := request(t, s, "/api/places/?activity=basketball&min_confidence=0.8&in_bbox=23,46,24,47", nil)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	obj := jsonObject(t, out.Body.Bytes())
	if obj["count"].(float64) != 1 {
		t.Fatal(obj)
	}
	features := obj["features"].([]any)
	props := features[0].(map[string]any)["properties"].(map[string]any)
	if props["display_address"] != "Strada Test 2, Cluj-Napoca" || props["is_bookable"] != true || props["open_now"] != true {
		t.Fatal(props)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO places_placecorrection(place_id,proposer_id,field,proposed_value,required_confirmations,status,created_at,published_at) VALUES($1,$2,'hours','Mo-Su off',3,'published',now(),now())`, id, a.ID); err != nil {
		t.Fatal(err)
	}
	out = request(t, s, "/api/places/"+strconvInt(id)+"/", nil)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	props = jsonObject(t, out.Body.Bytes())["properties"].(map[string]any)
	if props["open_now"] != false {
		t.Fatal("corrected schedule not reparsed", props)
	}
	for n := 0; n < 3; n++ {
		reporter := user(t, s, fmt.Sprintf("catalog-closure-%d", n), "adult")
		if _, err := s.DB.Exec(ctx, `INSERT INTO places_placeclosurereport(place_id,reporter_id,created_at) VALUES($1,$2,now())`, id, reporter.ID); err != nil {
			t.Fatal(err)
		}
	}
	out = request(t, s, "/api/places/"+strconvInt(id)+"/", nil)
	if out.Code != 404 {
		t.Fatal("closed venue was public", out.Code, out.Body.String())
	}
	out = request(t, s, "/api/places/?page_size=100000", nil)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body.String())
	}
	if strings.Contains(out.Body.String(), "Hidden private proposal") {
		t.Fatal("user venue without published proposal leaked")
	}
}

func TestPostgresVenueOverlaysAndClaimsTransactions(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	place := placeFixture(t, s, "Native overlays", "osm", 23.6, 46.77)
	author := user(t, s, "overlay-author", "adult")
	staff := user(t, s, "overlay-staff", "adult")
	staff.IsStaff = true
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_staff=true WHERE id=$1`, staff.ID); err != nil {
		t.Fatal(err)
	}
	correction, err := s.ProposeCorrection(ctx, author, place, "hours", "Mo-Su 08:00-12:00")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ConfirmCorrection(ctx, author, correction); err == nil {
		t.Fatal("proposer self-confirmed")
	}
	for i := 0; i < 3; i++ {
		peer := user(t, s, fmt.Sprintf("overlay-peer-%d", i), "adult")
		if created, err := s.ReportVenue(ctx, peer, place, false); err != nil || !created {
			t.Fatal(created, err)
		}
		if err := s.VoteFact(ctx, peer, place, "bus_tram_nearby", true); err != nil {
			t.Fatal(err)
		}
		if err := s.ConfirmCorrection(ctx, peer, correction); err != nil {
			t.Fatal(err)
		}
	}
	var reports int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_opennowreport WHERE place_id=$1`, place).Scan(&reports); err != nil || reports != 0 {
		t.Fatal("hours correction retained stale reports", reports, err)
	}
	facts, err := s.VenueFacts(ctx, author, place, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(facts) != 10 || facts[3]["key"] != "indoor_shelter" || facts[9]["state"] != "true" || len(facts[9]) != 8 {
		t.Fatal("facts source contract", facts)
	}
	claim, err := s.FileClaim(ctx, author, place, ClaimInput{OrgName: "Native institution", Kind: "business", OfficialWebsite: "https://venue.invalid", ContactEmail: "private@example.invalid", Evidence: "Private evidence"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecideClaim(ctx, author, claim, true, ""); err == nil {
		t.Fatal("nonstaff claim decision")
	}
	if err := s.DecideClaim(ctx, staff, claim, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideClaim(ctx, staff, claim, true, ""); err == nil {
		t.Fatal("repeated claim approval")
	}
	partners, err := s.Partners(ctx, &place, true)
	if err != nil || len(partners) != 1 {
		t.Fatal(partners, err)
	}
	for _, p := range partners {
		if p["contact_email"] != nil || p["evidence"] != nil {
			t.Fatal("claim PII public")
		}
	}
	pending := placeFixture(t, s, "Not published", "user", 23.6, 46.77)
	if err := s.VoteFact(ctx, author, pending, "shade", true); err == nil {
		t.Fatal("pending place vote bypass")
	}
	event := eventFixture(t, s, "Report event", "scheduled", &place, time.Now().Add(time.Hour))
	for i := 0; i < 3; i++ {
		peer := user(t, s, fmt.Sprintf("event-report-peer-%d", i), "adult")
		created, err := s.ReportEvent(ctx, peer, event, "wrong_time")
		if err != nil || !created {
			t.Fatal(created, err)
		}
		created, err = s.ReportEvent(ctx, peer, event, "cancelled")
		if err != nil || created {
			t.Fatal("duplicate independent report", created, err)
		}
	}
	reliability, err := s.EventReliability(ctx, event)
	if err != nil || reliability != "unverified" {
		t.Fatal(reliability, err)
	}
	if n, err := s.ClearEventReports(ctx, staff, event); err != nil || n != 3 {
		t.Fatal(n, err)
	}
}
