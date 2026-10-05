package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/notifications"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// These cases use registered HTML routes and the real native view/template
// adapters. Actors, public source facts and all database rows are synthetic.
func webCasePortFixture(t *testing.T) (*Server, platform.Actor, int64, int64, *http.ServeMux) {
	t.Helper()
	s, actor, place, typ := socialLegacyFixture(t)
	store := accounts.NewStore(s.DB)
	auth, err := authcore.New(authcore.Config{PublicURL: "https://fixture.local"}, store)
	if err != nil {
		t.Fatal(err)
	}
	s.Auth = auth
	s.Accounts = accounts.New(s.DB, auth, "synthetic-web-case-port-binding", accounts.Config{})
	if err := s.Accounts.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s.Notifications = notifications.New(s.DB, platform.CursorCodec{Key: []byte("synthetic-case-port-cursor-key-32bytes")})
	s.Donations = donations.New(s.DB, donations.Config{Provider: "dev"})
	api := http.NewServeMux()
	s.Social.Register(api)
	s.Catalog.Register(api)
	s.Media.Register(api)
	s.Accounts.Register(api)
	s.Notifications.Register(api)
	s.Donations.Register(api)
	s.Messaging.Register(api)
	s.Recommendations.Register(api)
	s.Safety.Register(api)
	s.API = api
	mux := http.NewServeMux()
	s.Register(mux)
	return s, actor, place, typ, mux
}

func webCasePortRead(mux *http.ServeMux, actor platform.Actor, path string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "https://fixture.local"+path, nil)
	if actor.ID != 0 {
		r = platform.WithActor(r, actor)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func webCasePortHTML(t *testing.T, mux *http.ServeMux, actor platform.Actor, path string) string {
	t.Helper()
	w := webCasePortRead(mux, actor, path)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("HTML route %s: status%d body=%s", path, w.Code, w.Body.String())
	}
	return w.Body.String()
}

func webCasePortContains(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, literal := range want {
		if !strings.Contains(body, literal) {
			t.Fatal("missing original HTML assertion", literal)
		}
	}
}

func webCasePortAbsent(t *testing.T, body string, absent ...string) {
	t.Helper()
	for _, literal := range absent {
		if strings.Contains(body, literal) {
			t.Fatal("unexpected original HTML assertion", literal)
		}
	}
}

func TestWebCasePortPublicAndAuthenticatedChrome(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	t.Run("anonymous_landing_navigation_footer_landmarks", func(t *testing.T) {
		body := webCasePortHTML(t, mux, platform.Actor{}, "/")
		webCasePortContains(t, body, `href="/login/"`, `href="/register/"`, `href="/donate/">Support the platform</a>`, `href="/privacy/"`, `href="/terms/"`, "Skip to main content", `id="main"`)
		webCasePortAbsent(t, body, `class="nav-link" href="/donate/"`)
	})
	t.Run("anonymous_draft_legal_pages", func(t *testing.T) {
		for _, path := range []string{"/privacy/", "/terms/"} {
			webCasePortContains(t, webCasePortHTML(t, mux, platform.Actor{}, path), "DRAFT")
		}
	})
	t.Run("private_settings_and_delete_require_login", func(t *testing.T) {
		w := webCasePortRead(mux, platform.Actor{}, "/settings/")
		if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "/login/") {
			t.Fatal("anonymous settings wall", w.Code)
		}
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "https://fixture.local/account/delete/", nil))
		if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "/login/") {
			t.Fatal("anonymous account erasure wall", w.Code)
		}
	})
	t.Run("authenticated_navigation_and_settings_chrome", func(t *testing.T) {
		if _, err := s.DB.Exec(context.Background(), `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,created_at,read_at) VALUES($1,'join_approved','Joined','','/notifications/',now(),NULL)`, actor.ID); err != nil {
			t.Fatal(err)
		}
		body := webCasePortHTML(t, mux, actor, "/")
		webCasePortContains(t, body, `href="/notifications/" aria-label="Notifications"`, `href="/messages/" aria-label="Messages"`, `<span class="pill">1</span>`, `href="/donate/">Support the platform</a>`, "nav-avatar", "/settings/")
		webCasePortAbsent(t, body, `class="nav-link" href="/donate/"`, "Signed in &middot; Adult")
		if strings.Count(body, "lang-switch") != 1 {
			t.Fatal("language switch must occur once in footer")
		}
		tab := regexp.MustCompile(`(?s)<nav class="tabbar"[^>]*>(.*?)</nav>`).FindStringSubmatch(body)
		if len(tab) != 2 {
			t.Fatal("missing navigation tabbar")
		}
		hrefs, labels := []string{}, []string{}
		for _, match := range regexp.MustCompile(`(?s)<a [^>]*href="([^"]+)"[^>]*>(.*?)</a>`).FindAllStringSubmatch(tab[1], -1) {
			hrefs = append(hrefs, match[1])
			label := regexp.MustCompile(`<[^>]+>`).ReplaceAllString(match[2], " ")
			labels = append(labels, strings.Join(strings.Fields(label), " "))
		}
		if !reflect.DeepEqual(hrefs, []string{"/", "/activities/", "/activities/new/", "/messages/", "/you/"}) || !reflect.DeepEqual(labels, []string{"Home", "Browse", "Organize", "Chat", "Profile"}) {
			t.Fatal("original tabbar order/labels", hrefs, labels)
		}
		webCasePortAbsent(t, tab[1], "/notifications/", `class="pill"`)
	})
	t.Run("settings_controls_delete_preview_and_profile_link", func(t *testing.T) {
		body := webCasePortHTML(t, mux, actor, "/settings/")
		webCasePortContains(t, body, "/i18n/setlang/", "/account/export/", `href="/account/delete/"`, "Delete my account", "/display/", "/notifications/preferences/", "/access/", "/my-privacy/")
		webCasePortContains(t, webCasePortHTML(t, mux, actor, "/account/delete/"), "What gets permanently deleted")
		webCasePortContains(t, webCasePortHTML(t, mux, actor, "/profile/"), "/settings/")
	})
	t.Run("standalone_inbox_subtabs_removed", func(t *testing.T) {
		for _, path := range []string{"/notifications/", "/messages/", "/connections/"} {
			webCasePortAbsent(t, webCasePortHTML(t, mux, actor, path), `<nav class="tabs" aria-label="Inbox"`, "web/_inbox_tabs.html")
		}
	})
	t.Run("core_pages_and_wards_render", func(t *testing.T) {
		for _, path := range []string{"/places/", "/activities/", "/interests/", "/profile/", "/notifications/", "/donate/", "/wards/"} {
			t.Run(path, func(t *testing.T) { webCasePortHTML(t, mux, actor, path) })
		}
	})
	t.Run("other_profile_progression_is_private", func(t *testing.T) {
		other := testdb.Actor(t, s.DB, "web-case-private-progression-other", "adult")
		path := fmt.Sprintf("/profile/%d/", other.ID)
		w := webCasePortRead(mux, actor, path)
		if w.Code != 404 {
			t.Fatal("other subject profile route exists", w.Code)
		}
		webCasePortAbsent(t, w.Body.String(), other.Username, "progression", "intensity")
		body := webCasePortHTML(t, mux, actor, "/profile/")
		webCasePortAbsent(t, body, other.Username, path, "progression_level", "progression_intensity", "intensity")
	})
}

func TestWebCasePortPublicVenuePages(t *testing.T) {
	s, actor, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET name='Cluj Central Park' WHERE id=$1`, place); err != nil {
		t.Fatal(err)
	}
	venueIDs := map[string]int64{}
	for _, row := range []struct{ name, city, tags string }{{"Step-free Hall", "Cluj-Napoca", `{"wheelchair":"yes"}`}, {"Limited Hall", "Cluj-Napoca", `{"wheelchair":"limited"}`}, {"Elsewhere", "Bucharest", `{}`}, {"Local Spot", "Cluj-Napoca", `{}`}} {
		id := testdb.Place(t, s.DB, row.name, "osm")
		venueIDs[row.name] = id
		if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_city=$2,raw_tags=$3::jsonb WHERE id=$1`, id, row.city, row.tags); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("anonymous_place_detail_and_server_side_list", func(t *testing.T) {
		webCasePortHTML(t, mux, platform.Actor{}, fmt.Sprintf("/places/%d/", venueIDs["Step-free Hall"]))
		body := webCasePortHTML(t, mux, platform.Actor{}, "/places/list/")
		webCasePortContains(t, body, "Cluj Central Park", "Step-free Hall", "Limited Hall")
		webCasePortAbsent(t, body, "Step-free access", "(limited)")
	})
	t.Run("city_filter", func(t *testing.T) {
		body := webCasePortHTML(t, mux, platform.Actor{}, "/places/list/?city=Cluj-Napoca")
		webCasePortContains(t, body, "Local Spot")
		webCasePortAbsent(t, body, "Elsewhere")
	})
	t.Run("venue_source_credit", func(t *testing.T) {
		id := testdb.Place(t, s.DB, "RO-EDU Hall", "roedu")
		if _, err := s.DB.Exec(ctx, `UPDATE places_place SET attribution='RO-EDU',license_name='CC BY 4.0',provenance_url='https://data.invalid/venues/hall-1' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		webCasePortContains(t, webCasePortHTML(t, mux, actor, fmt.Sprintf("/places/%d/", id)), "Source credit:", "RO-EDU", "CC BY 4.0")
	})
	t.Run("event_list_and_detail", func(t *testing.T) {
		id := publicEventFixture(t, s, "City Festival", place, typ)
		webCasePortHTML(t, mux, actor, "/events/")
		webCasePortHTML(t, mux, actor, fmt.Sprintf("/events/%d/", id))
	})
	t.Run("cross_cohort_activity_detail_404", func(t *testing.T) {
		id := socialLegacyActivity(t, s, actor, place, typ, "Adult-only source case")
		child := testdb.Actor(t, s.DB, "web-case-child-viewer", "child")
		w := webCasePortRead(mux, child, fmt.Sprintf("/activities/%d/", id))
		if w.Code != 404 {
			t.Fatal("cross-cohort detail status", w.Code)
		}
	})
}

func webCasePortMessengerConfig(t *testing.T, body string) map[string]any {
	t.Helper()
	match := regexp.MustCompile(`(?s)<script[^>]*id="mz-config"[^>]*>(.*?)</script>`).FindStringSubmatch(body)
	if len(match) != 2 {
		t.Fatal("missing E2EE config island")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(match[1]), &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestWebCasePortProfilesAndMessengerConnections(t *testing.T) {
	s, actor, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	t.Run("empty_profile_and_messenger", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, actor, "/profile/"), "No connections yet")
		body := webCasePortHTML(t, mux, actor, "/messages/")
		webCasePortContains(t, body, "mz-config", "end-to-end encrypted", "Use Start a chat for a username")
		config := webCasePortMessengerConfig(t, body)
		if rows, ok := config["connections"].([]any); !ok || len(rows) != 0 {
			t.Fatal("empty messenger connections", config["connections"])
		}
	})
	t.Run("accepted_connection_profile_and_messenger", func(t *testing.T) {
		peer := testdb.Actor(t, s.DB, "web-case-chat-peer", "adult")
		activity := socialLegacyActivity(t, s, actor, place, typ, "Shared accepted connection context")
		membership, err := s.Social.Join(ctx, peer, activity)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Social.Vote(ctx, actor, membership, true, false); err != nil {
			t.Fatal(err)
		}
		connection, err := s.Social.RequestConnection(ctx, actor, peer.PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Social.RespondConnection(ctx, peer, connection, "accept"); err != nil {
			t.Fatal(err)
		}
		webCasePortContains(t, webCasePortHTML(t, mux, actor, "/profile/"), "Connections", peer.DisplayName, "/connections/message/")
		body := webCasePortHTML(t, mux, actor, "/messages/")
		webCasePortContains(t, body, "Start a chat", "Username or group", "They must accept before they can read your messages.", "Device and guardian options")
		config := webCasePortMessengerConfig(t, body)
		if config["me"].(map[string]any)["username"] != actor.Username {
			t.Fatal("messenger self identity changed")
		}
		found := false
		for _, row := range config["connections"].([]any) {
			if row.(map[string]any)["username"] == peer.Username {
				found = true
			}
		}
		if !found {
			t.Fatal("accepted peer absent from messenger config")
		}
	})
	t.Run("donation_posts_redirect_to_checkout", func(t *testing.T) {
		seed := webCasePortRead(mux, actor, "/donate/")
		var csrf *http.Cookie
		for _, cookie := range seed.Result().Cookies() {
			if cookie.Name == "csrftoken" {
				csrf = cookie
			}
		}
		if seed.Code != 200 || csrf == nil {
			t.Fatal("donation form CSRF setup unavailable")
		}
		values := url.Values{"amount": {"10"}, "csrfmiddlewaretoken": {csrf.Value}}
		r := platform.WithActor(httptest.NewRequest("POST", "https://fixture.local/donate/", strings.NewReader(values.Encode())), actor)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://fixture.local")
		r.AddCookie(csrf)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 302 || !strings.HasPrefix(w.Header().Get("Location"), "dev://checkout/") {
			t.Fatal("native synthetic checkout redirect", w.Code, w.Header().Get("Location"))
		}
	})
}
