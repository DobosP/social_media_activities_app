package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

type countingProvider struct{ cancellations atomic.Int64 }

func (p *countingProvider) Create(context.Context, string, string, Request) (Result, error) {
	return Result{Reference: "fixture-reservation", Confirmed: true}, nil
}
func (p *countingProvider) Cancel(context.Context, string) error { p.cancellations.Add(1); return nil }

func TestNativeBookingPaginationFreshEligibilityAndConcurrentCancel(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	a := testdb.Actor(t, db, "booking-parity", "adult")
	place := testdb.Place(t, db, "Fixture bookable venue", "osm")
	provider := &countingProvider{}
	s := NewConfigured(db, Config{Providers: map[string]Provider{"fixture": provider}})
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(body)), a)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	body := fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z","provider":"fixture"}`, place)
	var first int64
	for i := 0; i < 3; i++ {
		got := call("POST", "/api/booking/bookings/", body)
		if got.Code != 201 {
			t.Fatal("create", got.Code, got.Body.String())
		}
		var record struct{ ID int64 }
		if json.Unmarshal(got.Body.Bytes(), &record) != nil {
			t.Fatal("invalid receipt")
		}
		first = record.ID
	}
	got := call("GET", "/api/booking/bookings/?limit=1", "")
	var page struct {
		Count    int
		Next     *string
		Previous *string
		Results  []json.RawMessage
	}
	if err := json.Unmarshal(got.Body.Bytes(), &page); err != nil || got.Code != 200 || page.Count != 3 || len(page.Results) != 1 || page.Next == nil || page.Previous != nil {
		t.Fatal("pagination", got.Code, got.Body.String())
	}
	got = call("GET", *page.Next, "")
	if err := json.Unmarshal(got.Body.Bytes(), &page); err != nil || page.Previous == nil || page.Next == nil {
		t.Fatal("pagination next/previous", got.Body.String())
	}
	got = call("GET", *page.Next, "")
	if err := json.Unmarshal(got.Body.Bytes(), &page); err != nil || page.Next != nil {
		t.Fatal("last pagination", got.Body.String())
	}
	// A real row-lock serializes competing cancellation requests before provider work.
	var wg sync.WaitGroup
	codes := make(chan int, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- call("POST", fmt.Sprintf("/api/booking/bookings/%d/cancel/", first), "{}").Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatal("concurrent cancellation", code)
		}
	}
	if provider.cancellations.Load() != 1 {
		t.Fatal("provider cancellation replayed", provider.cancellations.Load())
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if got := call("POST", "/api/booking/bookings/", body); got.Code != 403 {
		t.Fatal("stale session assurance booked", got.Code, got.Body.String())
	}
	if got := call("POST", "/api/booking/bookings/", fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z","provider":%q}`, place, strings.Repeat("x", 33))); got.Code != 400 {
		t.Fatal("provider field bounds")
	}
}

func TestRESTProviderPreservesOpaqueNumericReferenceAndDoesNotRedirect(t *testing.T) {
	p := &RESTProvider{BaseURL: "https://fixture.invalid", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"id":9007199254740993,"status":"confirmed"}`)), Header: http.Header{}}, nil
	})}}
	result, err := p.Create(context.Background(), "venue", "public-id", Request{PartySize: 1})
	if err != nil || result.Reference != "9007199254740993" {
		t.Fatal("opaque provider id rounded", result.Reference, err)
	}
	p = &RESTProvider{BaseURL: "https://fixture.invalid", APIKey: "synthetic-only", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 302, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Location": []string{"https://other.invalid"}}}, nil
	})}}
	if _, err := p.Create(context.Background(), "venue", "public-id", Request{PartySize: 1}); err == nil {
		t.Fatal("provider redirect accepted")
	}
}

func TestNativeGuardianBookingRequiresCurrentSupervisoryBasis(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	guardian := testdb.Actor(t, db, "booking-current-guardian", "adult")
	ward := testdb.Actor(t, db, "booking-child-ward", "child")
	ordinary := testdb.Actor(t, db, "booking-cross-cohort-member", "adult")
	place := testdb.Place(t, db, "Synthetic supervised venue", "osm")
	var category, typeID, activity int64
	if err := db.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES('fixture','Fixture',NULL,'',now(),now()) RETURNING id`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,category_id,parent_id,aliases,is_active,wellness,family_friendly,created_at,updated_at) VALUES('fixture-reading','Fixture reading',$1,NULL,'[]',true,false,true,now(),now()) RETURNING id`, category).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO social_activity(owner_id,place_id,activity_type_id,title,description,starts_at,ends_at,cohort,join_threshold,capacity,min_to_go,guardian_accompanied,supervised,meeting_point,what_to_bring,organizer_note,first_time_note,cost_band,cost_amount,cost_note,difficulty,accessibility_notes,beginners_welcome,status,is_hidden,is_publicly_listed,owner_can_override,go_confirmed_at,series_id,created_at,updated_at) VALUES($1,$2,$3,'Synthetic child meetup','',now()+interval '1 day',NULL,'child',0.6666666667,NULL,NULL,true,true,'','','','','unspecified',NULL,'','unspecified','',true,'open',false,false,true,NULL,NULL,now(),now()) RETURNING id`, ward.ID, place, typeID).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		actor platform.Actor
		role  string
	}{{ward, "owner"}, {guardian, "guardian"}, {ordinary, "member"}} {
		if _, err := db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,$3,'member','unknown','none',false,now(),now(),now())`, activity, member.actor.ID, member.role); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(a platform.Actor) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"place":%d,"activity":%d,"starts_at":"2030-10-04T10:00:00"}`, place, activity)
		r := platform.WithActor(httptest.NewRequest("POST", "/api/booking/bookings/", strings.NewReader(body)), a)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	if got := call(guardian); got.Code != 201 {
		t.Fatal("legitimate guardian seat denied", got.Code, got.Body.String())
	}
	if got := call(ordinary); got.Code != 403 {
		t.Fatal("ordinary cross-cohort membership allowed", got.Code)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked' WHERE minor_id=$1`, ward.ID); err != nil {
		t.Fatal(err)
	}
	if got := call(guardian); got.Code != 403 {
		t.Fatal("withdrawn ward consent booked", got.Code)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_parentalconsent SET status='active' WHERE minor_id=$1`, ward.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_guardianrelationship SET status='revoked' WHERE guardian_id=$1 AND ward_id=$2`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if got := call(guardian); got.Code != 403 {
		t.Fatal("revoked guardian basis booked", got.Code)
	}
}

func TestBookingRequestSerializerDefaultsAndLocalTimes(t *testing.T) {
	var request Request
	if err := json.Unmarshal([]byte(`{"place":1,"starts_at":"2030-01-04T10:00:00"}`), &request); err != nil || request.PartySize != 1 || request.StartsAt.UTC().Hour() != 8 {
		t.Fatal("local date or omitted party default", err, request)
	}
	if err := json.Unmarshal([]byte(`{"place":1,"starts_at":"2030-01-04T10:00:00Z","party_size":0}`), &request); err != nil || request.PartySize != 0 {
		t.Fatal("explicit zero lost")
	}
	for _, raw := range []string{`{"place":1,"starts_at":"2030-01-04T10:00:00Z","party_size":null}`, `{"place":1,"starts_at":"2030-01-04T10:00:00Z","unknown":"ignored"}`, `{"place":1,"starts_at":"not a date"}`} {
		if json.Unmarshal([]byte(raw), &request) == nil {
			t.Fatal("invalid serializer accepted", raw)
		}
	}
}
