package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

var bookingAssertionPrefixes = []string{"/api/booking", "/api/v1/booking"}

func bookingAssertionCall(mux *http.ServeMux, actor *platform.Actor, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if actor != nil {
		r = platform.WithActor(r, *actor)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func bookingAssertionJSON(t *testing.T, w *httptest.ResponseRecorder, code int, target any) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("HTTP status: got %d want %d: %s", w.Code, code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), target); err != nil {
		t.Fatalf("invalid JSON: %v: %s", err, w.Body.String())
	}
}

// These are the literal registered endpoints. Anonymous options must be refused
// before any database access; builtin provider discovery needs no fixture DB.
func TestBookingRESTAssertionAuthenticationAndProviders(t *testing.T) {
	mux := http.NewServeMux()
	New(nil).Register(mux)
	actor := platform.Actor{ID: 1, IsActive: true, IdentityVerified: true, Cohort: "adult"}
	for _, prefix := range bookingAssertionPrefixes {
		t.Run(prefix, func(t *testing.T) {
			t.Run("anonymous_options", func(t *testing.T) {
				got := bookingAssertionCall(mux, nil, "GET", prefix+"/options/?place=1", "")
				if got.Code != http.StatusUnauthorized {
					t.Fatalf("anonymous options: got %d want 401: %s", got.Code, got.Body.String())
				}
			})
			t.Run("builtin_providers", func(t *testing.T) {
				var providers []struct {
					Slug string `json:"slug"`
				}
				bookingAssertionJSON(t, bookingAssertionCall(mux, &actor, "GET", prefix+"/providers/", ""), 200, &providers)
				slugs := map[string]bool{}
				for _, provider := range providers {
					slugs[provider.Slug] = true
				}
				if !slugs["deeplink"] || !slugs["demo_rest"] {
					t.Fatalf("missing builtin provider slugs: %v", slugs)
				}
			})
		})
	}
}

func TestBookingRESTAssertionDeeplinkOptions(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	actor := testdb.Actor(t, db, "booking-assertion-options", "adult")
	plain := testdb.Place(t, db, "Synthetic default booking venue", "osm")
	configured := testdb.Place(t, db, "Synthetic configured booking venue", "osm")
	if _, err := db.Exec(ctx, `INSERT INTO booking_placebookinginfo(place_id,provider,deep_link,instructions,provider_place_ref,created_at,updated_at) VALUES($1,'deeplink','https://book.example/hall','Call ahead','',now(),now())`, configured); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(db).Register(mux)
	for _, prefix := range bookingAssertionPrefixes {
		t.Run(prefix, func(t *testing.T) {
			for _, fixture := range []struct {
				name               string
				place              int64
				link, instructions string
			}{{"default_options", plain, "", ""}, {"configured_options", configured, "https://book.example/hall", "Call ahead"}} {
				t.Run(fixture.name, func(t *testing.T) {
					var options struct {
						Provider     string  `json:"provider"`
						Bookable     *bool   `json:"bookable_in_app"`
						DeepLink     *string `json:"deep_link"`
						Instructions *string `json:"instructions"`
					}
					path := fmt.Sprintf("%s/options/?place=%d", prefix, fixture.place)
					bookingAssertionJSON(t, bookingAssertionCall(mux, &actor, "GET", path, ""), 200, &options)
					if options.Provider != "deeplink" || options.Bookable == nil || *options.Bookable {
						t.Fatalf("deep-link options provider/capability: %+v", options)
					}
					if options.DeepLink == nil || *options.DeepLink != fixture.link || options.Instructions == nil || *options.Instructions != fixture.instructions {
						t.Fatalf("configured link/instructions did not roundtrip: %s", path)
					}
				})
			}
		})
	}
}

// Expectations come from the fixed fixture, not from another response. The
// returned ID locates the independently read row and must reappear in the list.
func TestBookingRESTAssertionDeeplinkCreateListAndCancel(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	place := testdb.Place(t, db, "Synthetic deep-link lifecycle venue", "osm")
	if _, err := db.Exec(ctx, `INSERT INTO booking_placebookinginfo(place_id,provider,deep_link,instructions,provider_place_ref,created_at,updated_at) VALUES($1,'deeplink','https://book.example/x','','',now(),now())`, place); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(db).Register(mux)
	for i, prefix := range bookingAssertionPrefixes {
		t.Run(prefix, func(t *testing.T) {
			actor := testdb.Actor(t, db, fmt.Sprintf("booking-assertion-lifecycle-%d", i), "adult")
			body := fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z"}`, place)
			var created struct {
				ID       int64  `json:"id"`
				Status   string `json:"status"`
				DeepLink string `json:"deep_link"`
			}
			bookingAssertionJSON(t, bookingAssertionCall(mux, &actor, "POST", prefix+"/bookings/", body), 201, &created)
			if created.ID <= 0 || created.Status != "pending" || created.DeepLink != "https://book.example/x" {
				t.Fatalf("created pending deep-link receipt: %+v", created)
			}
			var userID, placeID int64
			var provider, status, link, external string
			if err := db.QueryRow(ctx, `SELECT user_id,place_id,provider,status,deep_link,external_ref FROM booking_booking WHERE id=$1`, created.ID).Scan(&userID, &placeID, &provider, &status, &link, &external); err != nil {
				t.Fatal(err)
			}
			if userID != actor.ID || placeID != place || provider != "deeplink" || status != "pending" || link != "https://book.example/x" || external != "" {
				t.Fatalf("stored pending deep-link booking: user=%d place=%d provider=%q status=%q link=%q external=%q", userID, placeID, provider, status, link, external)
			}
			var listing struct {
				Results []struct {
					ID int64 `json:"id"`
				} `json:"results"`
			}
			bookingAssertionJSON(t, bookingAssertionCall(mux, &actor, "GET", prefix+"/bookings/", ""), 200, &listing)
			if len(listing.Results) != 1 || listing.Results[0].ID != created.ID {
				t.Fatalf("created ID missing from own list: created=%d results=%+v", created.ID, listing.Results)
			}
			var cancelled struct {
				ID     int64  `json:"id"`
				Status string `json:"status"`
			}
			path := fmt.Sprintf("%s/bookings/%d/cancel/", prefix, created.ID)
			bookingAssertionJSON(t, bookingAssertionCall(mux, &actor, "POST", path, ""), 200, &cancelled)
			if cancelled.ID != created.ID || cancelled.Status != "cancelled" {
				t.Fatalf("cancelled response: %+v", cancelled)
			}
			if err := db.QueryRow(ctx, `SELECT status FROM booking_booking WHERE id=$1`, created.ID).Scan(&status); err != nil {
				t.Fatal(err)
			}
			if status != "cancelled" {
				t.Fatalf("cancellation not persisted: %q", status)
			}
		})
	}
}

func TestBookingRESTAssertionFreshUnverifiedDenied(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	// Match a freshly registered account, rather than relabel an assured actor.
	fresh := platform.Actor{Username: "booking-assertion-fresh", AgeBand: "unknown", Cohort: "unassigned", Role: "user", IsActive: true}
	if err := db.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,'unknown','unassigned',false,NULL,'user',true,false,now()) RETURNING id,public_id::text`, fresh.Username).Scan(&fresh.ID, &fresh.PublicID); err != nil {
		t.Fatal(err)
	}
	eligible := testdb.Actor(t, db, "booking-assertion-eligible", "adult")
	place := testdb.Place(t, db, "Synthetic participation venue", "osm")
	mux := http.NewServeMux()
	New(db).Register(mux)
	body := fmt.Sprintf(`{"place":%d,"starts_at":"2030-10-04T10:00:00Z"}`, place)
	for _, prefix := range bookingAssertionPrefixes {
		t.Run(prefix, func(t *testing.T) {
			got := bookingAssertionCall(mux, &fresh, "POST", prefix+"/bookings/", body)
			if got.Code != 403 {
				t.Fatalf("fresh unverified account: got %d want 403: %s", got.Code, got.Body.String())
			}
			var count int
			if err := db.QueryRow(ctx, `SELECT count(*) FROM booking_booking WHERE user_id=$1`, fresh.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("denied fresh account wrote %d bookings", count)
			}
			var receipt struct {
				ID int64 `json:"id"`
			}
			bookingAssertionJSON(t, bookingAssertionCall(mux, &eligible, "POST", prefix+"/bookings/", body), 201, &receipt)
			if receipt.ID <= 0 {
				t.Fatal("eligible positive control has no booking ID")
			}
		})
	}
}

func TestBookingRESTAssertionActivityMembership(t *testing.T) {
	db := testdb.New(t, *domainDSN, nil)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "booking-assertion-activity-owner", "adult")
	outsider := testdb.Actor(t, db, "booking-assertion-activity-outsider", "adult")
	place := testdb.Place(t, db, "Synthetic activity booking venue", "osm")
	var category, activityType, activity int64
	if err := db.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES('booking-assertion','Fixture',NULL,'',now(),now()) RETURNING id`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,category_id,parent_id,aliases,is_active,wellness,family_friendly,created_at,updated_at) VALUES('booking-assertion-game','Fixture game',$1,NULL,'[]',true,false,true,now(),now()) RETURNING id`, category).Scan(&activityType); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO social_activity(owner_id,place_id,activity_type_id,title,description,starts_at,ends_at,cohort,join_threshold,capacity,min_to_go,guardian_accompanied,supervised,meeting_point,what_to_bring,organizer_note,first_time_note,cost_band,cost_amount,cost_note,difficulty,accessibility_notes,beginners_welcome,status,is_hidden,is_publicly_listed,owner_can_override,go_confirmed_at,series_id,created_at,updated_at) VALUES($1,$2,$3,'Synthetic game','',now()+interval '1 day',NULL,'adult',0.6666666667,NULL,NULL,false,false,'','','','','unspecified',NULL,'','unspecified','',true,'open',false,false,true,NULL,NULL,now(),now()) RETURNING id`, owner.ID, place, activityType).Scan(&activity); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'owner','member','unknown','none',false,now(),now(),now())`, activity, owner.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(db).Register(mux)
	body := fmt.Sprintf(`{"place":%d,"activity":%d,"starts_at":"2030-10-04T10:00:00Z"}`, place, activity)
	for _, prefix := range bookingAssertionPrefixes {
		t.Run(prefix, func(t *testing.T) {
			var created struct {
				ID       int64 `json:"id"`
				Activity int64 `json:"activity"`
			}
			bookingAssertionJSON(t, bookingAssertionCall(mux, &owner, "POST", prefix+"/bookings/", body), 201, &created)
			if created.ID <= 0 || created.Activity != activity {
				t.Fatalf("owner membership booking: %+v", created)
			}
			var storedOwner, storedActivity int64
			if err := db.QueryRow(ctx, `SELECT user_id,activity_id FROM booking_booking WHERE id=$1`, created.ID).Scan(&storedOwner, &storedActivity); err != nil {
				t.Fatal(err)
			}
			if storedOwner != owner.ID || storedActivity != activity {
				t.Fatalf("stored owner/activity: %d/%d", storedOwner, storedActivity)
			}
			got := bookingAssertionCall(mux, &outsider, "POST", prefix+"/bookings/", body)
			if got.Code != 403 {
				t.Fatalf("assured same-cohort outsider: got %d want 403: %s", got.Code, got.Body.String())
			}
			var count int
			if err := db.QueryRow(ctx, `SELECT count(*) FROM booking_booking WHERE user_id=$1`, outsider.ID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatalf("denied outsider wrote %d bookings", count)
			}
		})
	}
}
