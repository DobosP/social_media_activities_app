package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func bookingAdapterCall(mux *http.ServeMux, actor platform.Actor, method, path, body string) *httptest.ResponseRecorder {
	r := platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(body)), actor)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func bookingAdapterJSON(t *testing.T, w *httptest.ResponseRecorder, code int, target any) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("HTTP status: got %d want %d: %s", w.Code, code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
}

func bookingAdapterRequestObject(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	defer r.Body.Close()
	var object map[string]any
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&object); err != nil || object == nil {
		t.Fatalf("provider body must be a JSON object: %v", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatalf("provider body has trailing data: %v", err)
	}
	return object
}

func bookingAdapterCount(t *testing.T, db *pgxpool.Pool, user int64) int {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM booking_booking WHERE user_id=$1`, user).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func bookingAdapterRefusal(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]string
	bookingAdapterJSON(t, w, 502, &body)
	if len(body) != 1 || body["detail"] != "Booking provider unavailable." {
		t.Fatalf("provider refusal must contain only the fixed detail: %+v", body)
	}
}

func TestBookingAdapterAssertionDemoCreateAndCancel(t *testing.T) {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	calls := 0
	provider := &RESTProvider{BaseURL: "https://fixture-provider.invalid", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Host != "fixture-provider.invalid" || r.URL.Scheme != "https" || r.Header.Get("Content-Type") != "application/json" {
			t.Fatal("provider method, origin or content type changed")
		}
		body := bookingAdapterRequestObject(t, r)
		switch calls {
		case 1:
			if r.URL.Path != "/bookings" || len(body) != 5 || body["venue"] != "venue-9" || body["customer_ref"] != "user-uuid" || body["party_size"] != float64(4) || body["start"] != "2026-01-01T10:00:00Z" || body["end"] != "2026-01-01T11:00:00Z" {
				t.Fatalf("create wire fixture: path=%q body=%+v", r.URL.Path, body)
			}
			return &http.Response{StatusCode: 201, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"bk-1","status":"confirmed"}`))}, nil
		case 2:
			if r.URL.Path != "/bookings/bk-1/cancel" || len(body) != 0 {
				t.Fatalf("cancel wire must be exact POST path and empty object: path=%q body=%+v", r.URL.Path, body)
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		default:
			t.Fatalf("provider POST replayed: %d calls", calls)
			return nil, errProvider
		}
	})}}
	result, err := provider.Create(context.Background(), "venue-9", "user-uuid", Request{StartsAt: start, EndsAt: &end, PartySize: 4})
	if err != nil || result.Reference != "bk-1" || !result.Confirmed || calls != 1 {
		t.Fatalf("confirmed adapter result: result=%+v error=%v calls=%d", result, err, calls)
	}
	if err := provider.Cancel(context.Background(), "bk-1"); err != nil || calls != 2 {
		t.Fatalf("successful adapter cancellation: error=%v calls=%d", err, calls)
	}
}

func TestBookingAdapterAssertionConnectionFailureFixedError(t *testing.T) {
	for _, operation := range []string{"create", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			calls := 0
			provider := &RESTProvider{BaseURL: "https://fixture-provider.invalid", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				// A lost response is ambiguous for a non-idempotent POST. This is
				// deliberately a read failure, not the retryable safe dial failure.
				return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("synthetic private provider failure")}
			})}}
			var err error
			if operation == "create" {
				var result Result
				result, err = provider.Create(context.Background(), "v1", "u1", Request{StartsAt: time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC), PartySize: 2})
				if result != (Result{}) {
					t.Fatalf("failed create fabricated a result: %+v", result)
				}
			} else {
				err = provider.Cancel(context.Background(), "bk-1")
			}
			if err != errProvider || err.Error() != "booking provider unavailable" || calls != 1 {
				t.Fatalf("connection failure must be fixed and one-shot: error=%v calls=%d", err, calls)
			}
		})
	}
}

func TestBookingAdapterAssertionUnconfiguredBeforeTransport(t *testing.T) {
	calls := 0
	provider := &RESTProvider{BaseURL: "", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 201, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"unexpected","status":"confirmed"}`))}, nil
	})}}
	result, err := provider.Create(context.Background(), "v", "u", Request{StartsAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), PartySize: 1})
	if err != errProvider || result != (Result{}) || calls != 0 {
		t.Fatalf("empty base URL must refuse before transport: result=%+v error=%v calls=%d", result, err, calls)
	}
	if err := provider.Cancel(context.Background(), "bk-1"); err != errProvider || calls != 0 {
		t.Fatalf("empty base URL cancellation reached transport: error=%v calls=%d", err, calls)
	}
}

// Reviewed representation replacement: native deeplink is an implicit
// non-realtime capability; demo_rest retains its concrete configured adapter.
func TestBookingAdapterAssertionBuiltinRegistryCapabilities(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("registry discovery must not call a provider")
	})}
	for _, fixture := range []struct {
		name string
		service *Service
		base string
		client *http.Client
	}{{"default", New(nil), "", nil}, {"configured", NewConfigured(nil, Config{DemoBaseURL: "https://fixture-provider.invalid", Client: client}), "https://fixture-provider.invalid", client}} {
		t.Run(fixture.name, func(t *testing.T) {
			demo, ok := fixture.service.Providers["demo_rest"].(*RESTProvider)
			if !ok || demo == nil || demo.BaseURL != fixture.base || demo.Client != fixture.client || !realtime(demo) || realtime(fixture.service.Providers["deeplink"]) {
				t.Fatal("native builtin registry type/configuration/capabilities changed")
			}
			mux := http.NewServeMux()
			fixture.service.Register(mux)
			actor := platform.Actor{ID: 1, Cohort: "adult", IsActive: true, IdentityVerified: true}
			for _, prefix := range []string{"/api/booking", "/api/v1/booking"} {
				var rows []struct {
					Slug string `json:"slug"`
					Realtime *bool `json:"supports_realtime"`
				}
				bookingAdapterJSON(t, bookingAdapterCall(mux, actor, "GET", prefix+"/providers/", ""), 200, &rows)
				capabilities := map[string]*bool{}
				for _, row := range rows {
					capabilities[row.Slug] = row.Realtime
				}
				if capabilities["deeplink"] == nil || *capabilities["deeplink"] || capabilities["demo_rest"] == nil || !*capabilities["demo_rest"] {
					t.Fatalf("builtin endpoint capabilities missing or changed: %+v", rows)
				}
			}
		})
	}
	if calls != 0 {
		t.Fatalf("builtin registry discovery contacted provider %d times", calls)
	}
}

func TestBookingAdapterAssertionConnectionFailureHTTP(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	actor := testdb.Actor(t, db, "booking-adapter-failure", "adult")
	place := testdb.Place(t, db, "Synthetic failing booking venue", "osm")
	calls := 0
	service := NewConfigured(db, Config{DemoBaseURL: "https://fixture-provider.invalid", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("synthetic private provider failure")}
	})}})
	mux := http.NewServeMux()
	service.Register(mux)
	body := fmt.Sprintf(`{"place":%d,"provider":"demo_rest","starts_at":"2030-10-04T10:00:00Z","party_size":2}`, place)
	for _, prefix := range []string{"/api/booking", "/api/v1/booking"} {
		t.Run(prefix, func(t *testing.T) {
			before := calls
			bookingAdapterRefusal(t, bookingAdapterCall(mux, actor, "POST", prefix+"/bookings/", body))
			if calls != before+1 || bookingAdapterCount(t, db, actor.ID) != 0 {
				t.Fatalf("failed provider request replayed or persisted: calls=%d before=%d", calls, before)
			}
		})
	}
}

// Reviewed replacement for Python KeyError: the existing governed handler
// exposes only a fixed502 and commits no receipt for an unknown provider.
func TestBookingAdapterAssertionUnknownRegistryRefusal(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	actor := testdb.Actor(t, db, "booking-adapter-unknown", "adult")
	unknown := testdb.Place(t, db, "Synthetic unknown-provider venue", "osm")
	known := testdb.Place(t, db, "Synthetic default-provider venue", "osm")
	if _, err := db.Exec(ctx, `INSERT INTO booking_placebookinginfo(place_id,provider,deep_link,instructions,provider_place_ref,created_at,updated_at) VALUES($1,'nope','','','',now(),now())`, unknown); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := NewConfigured(db, Config{DemoBaseURL: "https://fixture-provider.invalid", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("unknown registry must not reach transport")
	})}})
	if service.Providers["nope"] != nil {
		t.Fatal("unknown provider unexpectedly registered")
	}
	mux := http.NewServeMux()
	service.Register(mux)
	for _, prefix := range []string{"/api/booking", "/api/v1/booking"} {
		t.Run(prefix, func(t *testing.T) {
			before := bookingAdapterCount(t, db, actor.ID)
			bookingAdapterRefusal(t, bookingAdapterCall(mux, actor, "GET", fmt.Sprintf("%s/options/?place=%d", prefix, unknown), ""))
			for _, selection := range []struct {
				place int64
				provider string
			}{{unknown, ""}, {known, "nope"}} {
				body := fmt.Sprintf(`{"place":%d,"provider":%q,"starts_at":"2030-10-04T10:00:00Z","party_size":1}`, selection.place, selection.provider)
				bookingAdapterRefusal(t, bookingAdapterCall(mux, actor, "POST", prefix+"/bookings/", body))
				if bookingAdapterCount(t, db, actor.ID) != before || calls != 0 {
					t.Fatal("unknown registry refusal wrote a receipt or contacted provider")
				}
			}
			body := fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z","party_size":1}`, known)
			var positive struct { ID int64 `json:"id"` }
			bookingAdapterJSON(t, bookingAdapterCall(mux, actor, "POST", prefix+"/bookings/", body), 201, &positive)
			if positive.ID <= 0 || bookingAdapterCount(t, db, actor.ID) != before+1 || calls != 0 {
				t.Fatal("known default-provider positive control failed")
			}
		})
	}
}

type bookingNonRealtimeAssertionProvider struct{ creates int }

func (p *bookingNonRealtimeAssertionProvider) SupportsRealtime() bool { return false }
func (p *bookingNonRealtimeAssertionProvider) Create(context.Context, string, string, Request) (Result, error) {
	p.creates++
	return Result{Reference: "forbidden-remote-confirmation", Confirmed: true}, nil
}
func (p *bookingNonRealtimeAssertionProvider) Cancel(context.Context, string) error { return nil }

// Reviewed replacement for direct BookingNotSupported: capability admission
// prevents adapter Create while preserving the pending local deep-link record.
func TestBookingAdapterAssertionNonRealtimeSkipsCreate(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	actor := testdb.Actor(t, db, "booking-adapter-deeplink", "adult")
	place := testdb.Place(t, db, "Synthetic non-realtime venue", "osm")
	if _, err := db.Exec(ctx, `INSERT INTO booking_placebookinginfo(place_id,provider,deep_link,instructions,provider_place_ref,created_at,updated_at) VALUES($1,'deeplink','https://book.example/capability','','',now(),now())`, place); err != nil {
		t.Fatal(err)
	}
	probe := &bookingNonRealtimeAssertionProvider{}
	service := NewConfigured(db, Config{Providers: map[string]Provider{"deeplink": probe}})
	mux := http.NewServeMux()
	service.Register(mux)
	for _, prefix := range []string{"/api/booking", "/api/v1/booking"} {
		t.Run(prefix, func(t *testing.T) {
			var options struct { Bookable *bool `json:"bookable_in_app"` }
			bookingAdapterJSON(t, bookingAdapterCall(mux, actor, "GET", fmt.Sprintf("%s/options/?place=%d", prefix, place), ""), 200, &options)
			if options.Bookable == nil || *options.Bookable {
				t.Fatal("non-realtime override advertised remote booking")
			}
			for _, selected := range []string{"", "deeplink"} {
				body := fmt.Sprintf(`{"place":%d,"provider":%q,"starts_at":"2030-10-04T10:00:00Z","party_size":1}`, place, selected)
				got := bookingAdapterCall(mux, actor, "POST", prefix+"/bookings/", body)
				if probe.creates != 0 {
					t.Fatalf("non-realtime adapter Create called %d times", probe.creates)
				}
				var receipt struct {
					ID int64 `json:"id"`
					Provider string `json:"provider"`
					Status string `json:"status"`
					External *string `json:"external_ref"`
					Link *string `json:"deep_link"`
				}
				bookingAdapterJSON(t, got, 201, &receipt)
				if receipt.ID <= 0 || receipt.Provider != "deeplink" || receipt.Status != "pending" || receipt.External == nil || *receipt.External != "" || receipt.Link == nil || *receipt.Link != "https://book.example/capability" {
					t.Fatalf("non-realtime local receipt: %+v", receipt)
				}
				var owner int64
				var provider, status, reference, link string
				if err := db.QueryRow(ctx, `SELECT user_id,provider,status,external_ref,deep_link FROM booking_booking WHERE id=$1`, receipt.ID).Scan(&owner, &provider, &status, &reference, &link); err != nil {
					t.Fatal(err)
				}
				if owner != actor.ID || provider != "deeplink" || status != "pending" || reference != "" || link != "https://book.example/capability" {
					t.Fatalf("stored non-realtime local record: owner=%d provider=%q status=%q reference=%q link=%q", owner, provider, status, reference, link)
				}
			}
		})
	}
}

func TestBookingAdapterAssertionConfiguredRealtimeTuple(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	actor := testdb.Actor(t, db, "booking-adapter-realtime", "adult")
	place := testdb.Place(t, db, "Synthetic realtime venue", "osm")
	if _, err := db.Exec(ctx, `INSERT INTO booking_placebookinginfo(place_id,provider,deep_link,instructions,provider_place_ref,created_at,updated_at) VALUES($1,'demo_rest','https://book.example/not-a-realtime-receipt','','venue-9',now(),now())`, place); err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := NewConfigured(db, Config{DemoBaseURL: "https://fixture-provider.invalid", Client: &http.Client{Transport: transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || r.URL.Host != "fixture-provider.invalid" || r.URL.Path != "/bookings" {
			t.Fatal("configured realtime adapter request changed")
		}
		body := bookingAdapterRequestObject(t, r)
		if body["venue"] != "venue-9" || body["customer_ref"] != actor.PublicID {
			t.Fatalf("configured venue or opaque actor reference lost: %+v", body)
		}
		return &http.Response{StatusCode: 201, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"bk-9","status":"confirmed"}`))}, nil
	})}})
	mux := http.NewServeMux()
	service.Register(mux)
	for _, prefix := range []string{"/api/booking", "/api/v1/booking"} {
		t.Run(prefix, func(t *testing.T) {
			before := calls
			body := fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z","party_size":2}`, place)
			var receipt struct {
				ID int64 `json:"id"`
				Provider string `json:"provider"`
				Status string `json:"status"`
				Reference string `json:"external_ref"`
				Link *string `json:"deep_link"`
			}
			bookingAdapterJSON(t, bookingAdapterCall(mux, actor, "POST", prefix+"/bookings/", body), 201, &receipt)
			if receipt.ID <= 0 || receipt.Provider != "demo_rest" || receipt.Status != "confirmed" || receipt.Reference != "bk-9" || receipt.Link == nil || *receipt.Link != "" || calls != before+1 {
				t.Fatalf("configured realtime HTTP tuple: %+v calls=%d before=%d", receipt, calls, before)
			}
			var owner, storedPlace int64
			var provider, status, reference, link string
			if err := db.QueryRow(ctx, `SELECT user_id,place_id,provider,status,external_ref,deep_link FROM booking_booking WHERE id=$1`, receipt.ID).Scan(&owner, &storedPlace, &provider, &status, &reference, &link); err != nil {
				t.Fatal(err)
			}
			if owner != actor.ID || storedPlace != place || provider != "demo_rest" || status != "confirmed" || reference != "bk-9" || link != "" {
				t.Fatalf("stored configured realtime tuple: owner=%d place=%d provider=%q status=%q reference=%q link=%q", owner, storedPlace, provider, status, reference, link)
			}
		})
	}
}
