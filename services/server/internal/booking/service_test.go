package booking

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var domainDSN = flag.String("domain-test-dsn", "", "explicit disposable native domain database")

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestRESTProviderDoesNotReplayAmbiguousBooking(t *testing.T) {
	calls := 0
	provider := &RESTProvider{BaseURL: "https://fixture-provider.invalid", APIKey: "synthetic-fixture", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Header.Get("Authorization") != "Bearer synthetic-fixture" {
			t.Fatal("provider header")
		}
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}}, nil
	})}}
	_, err := provider.Create(context.Background(), "venue", "opaque-user", Request{StartsAt: time.Now(), PartySize: 1})
	if err == nil || calls != 1 {
		t.Fatal("ambiguous POST replayed", calls)
	}
}
func TestRESTProviderShapeAndBoundaries(t *testing.T) {
	p := &RESTProvider{BaseURL: "https://fixture-provider.invalid", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/bookings" {
			t.Fatal("provider path")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["customer_ref"] != "opaque-user" || body["venue"] != "venue" {
			t.Fatal("identity boundary")
		}
		return &http.Response{StatusCode: 201, Body: io.NopCloser(strings.NewReader(`{"id":42,"status":"confirmed"}`)), Header: http.Header{}}, nil
	})}}
	result, err := p.Create(context.Background(), "venue", "opaque-user", Request{StartsAt: time.Now(), PartySize: 2})
	if err != nil || !result.Confirmed || result.Reference != "42" {
		t.Fatal("provider result")
	}
	if p.Cancel(context.Background(), "../escape") == nil {
		t.Fatal("unsafe provider reference")
	}
	if (&RESTProvider{BaseURL: "http://fixture.invalid"}).Cancel(context.Background(), "42") == nil {
		t.Fatal("insecure remote adapter")
	}
}
func TestNativeBookingOwnershipAndEligibility(t *testing.T) {
	if *domainDSN == "" {
		t.Skip("explicit fixture DSN required")
	}
	db := testdb.New(t, *domainDSN, nil)
	a := testdb.Actor(t, db, "booking-owner", "adult")
	b := testdb.Actor(t, db, "booking-other", "adult")
	place := testdb.Place(t, db, "Synthetic place", "osm")
	s := New(db)
	mux := http.NewServeMux()
	s.Register(mux)
	call := func(actor platform.Actor, method, path, body string) *httptest.ResponseRecorder {
		r := platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(body)), actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	response := call(a, "GET", fmt.Sprintf("/api/booking/options/?place=%d", place), "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"deep_link":""`) {
		t.Fatal("invented booking link", response.Code, response.Body.String())
	}
	response = call(a, "POST", "/api/booking/bookings/", fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z","party_size":2}`, place))
	if response.Code != 201 {
		t.Fatal(response.Code, response.Body.String())
	}
	var record struct {
		ID       int64
		Status   string
		DeepLink string `json:"deep_link"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &record)
	if record.Status != "pending" || record.DeepLink != "" {
		t.Fatal("deep link fabricated confirmation")
	}
	path := fmt.Sprintf("/api/booking/bookings/%d/cancel/", record.ID)
	if response = call(b, "POST", path, "{}"); response.Code != 404 {
		t.Fatal("foreign cancellation", response.Code)
	}
	if response = call(a, "POST", path, "{}"); response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"cancelled"`) {
		t.Fatal("cancel failed", response.Code)
	}
	if response = call(a, "POST", path, "{}"); response.Code != 200 {
		t.Fatal("cancel not idempotent")
	}
	a.Cohort = "unassigned"
	if response = call(a, "POST", "/api/booking/bookings/", fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z"}`, place)); response.Code != 403 {
		t.Fatal("pending account booked", response.Code)
	}
}
