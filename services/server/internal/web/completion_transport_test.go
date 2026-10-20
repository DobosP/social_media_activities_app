package web

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/discovery"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/flosch/pongo2/v6"
)

// A successful request cannot silently reach its old Pongo template. This is
// only a test loader: the registered mux, producers, authorizers, HTML renderer
// and private PostgreSQL fixture remain the actual current implementations.
type completionRejectLegacyLoader struct{ delegate pongo2.TemplateLoader }

func (l *completionRejectLegacyLoader) Abs(base, name string) string {
	return l.delegate.Abs(base, name)
}
func (l *completionRejectLegacyLoader) Get(name string) (io.Reader, error) {
	if completionReferenceNames[name] {
		return nil, fmt.Errorf("selected current route fell back to legacy template: %s", name)
	}
	return l.delegate.Get(name)
}

func TestNativeCompletionRegisteredCurrentRoutesUseActualProducerContexts(t *testing.T) {
	s, actor := accountWebFixture(t)
	ctx := context.Background()
	s.Donations = donations.New(s.DB, donations.Config{})
	s.Discovery = discovery.New(s.DB, s.Catalog, s.Social, s.Recommendations)
	s.Social.BodyMarkup = BodyMarkup
	// Use the actual media read routes required by the activity detail producer.
	s.Media = media.NewService(s.DB, nil, nil, media.TokenCodec{Key: bytes.Repeat([]byte("x"), 32)}, s.Social)
	place := testdb.Place(t, s.DB, "Current completion venue", "osm")
	var typ int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity := socialLegacyActivity(t, s, actor, place, typ, "Current completion meetup")
	series, err := s.Social.CreateSeries(ctx, actor, social.SeriesInput{ActivityInput: social.ActivityInput{Place: place, ActivityType: typ, Title: "Current recurring meetup", CostBand: "free"}, Cadence: "weekly", FirstStartsAt: time.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	gauge, err := s.Social.ProposeGauge(ctx, actor, social.GaugeInput{Place: place, ActivityType: typ, CoarseWindow: "weekend_daytime"})
	if err != nil {
		t.Fatal(err)
	}
	event := publicEventFixture(t, s, "Current completion concert", place, typ)
	peer := testdb.Actor(t, s.DB, "completion-connected-peer", "adult")
	if _, err = s.DB.Exec(ctx, `INSERT INTO connections_connection(requester_id,addressee_id,status,created_at,decided_at) VALUES($1,$2,'accepted',now(),now())`, actor.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	member, err := s.Social.Join(ctx, peer, activity)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Social.Vote(ctx, actor, member, true, false); err != nil {
		t.Fatal(err)
	}
	loader := &completionRejectLegacyLoader{delegate: s.Renderer.loader}
	s.Renderer.loader, s.Renderer.set = loader, pongo2.NewSet("actual-completion-current-only", loader)
	mux := http.NewServeMux()
	s.Accounts.Register(mux)
	s.Social.Register(mux)
	s.Media.Register(mux)
	s.Safety.Register(mux)
	s.Messaging.Register(mux)
	s.Register(mux)
	s.API = mux
	type route struct {
		name      string
		args      []any
		query     string
		anonymous bool
	}
	routes := []route{
		{"my_meetups", nil, "", false}, {"my_venues", nil, "", false},
		{"donate", nil, "", true}, {"transparency", nil, "", true}, {"campaigns", nil, "", true}, {"partners", nil, "", true}, {"my_donations", nil, "", false},
		{"series_list", nil, "", false}, {"series_detail", []any{series}, "", false}, {"series_create", nil, "", false}, {"gauges", nil, "", false}, {"gauge_detail", []any{gauge}, "", false}, {"gauge_create", nil, "", false}, {"gauge_convert", []any{gauge}, "", false},
		{"person", []any{peer.PublicID}, "", false}, {"person_card", []any{peer.PublicID}, "", false}, {"activity_log", nil, "", false}, {"safety_record", nil, "", false}, {"my_privacy", nil, "", false}, {"verify_age", nil, "", false}, {"account_delete", nil, "", false}, {"report", nil, fmt.Sprintf("?type=activity&id=%d", activity), false}, {"privacy", nil, "", true}, {"terms", nil, "", true},
		{"home", nil, "", true}, {"community_graph", nil, "", false}, {"discover", nil, "", true}, {"places_map", nil, "", true}, {"place_detail", []any{place}, "", true}, {"event_detail", []any{event}, "", true}, {"open_data", nil, "", true},
		{"activity_create", nil, "", false}, {"activity_edit", []any{activity}, "", false}, {"activity_detail", []any{activity}, "", false}, {"place_propose", nil, "", false}, {"place_claim", []any{place}, "", false}, {"places_pending", nil, "", false}, {"messages", nil, "", false},
	}
	if len(routes) != 38 {
		t.Fatal("38 normal views plus blocked-member safe exit required")
	}
	read := func(t *testing.T, path, language string, viewer *platform.Actor) string {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+path, nil)
		request.AddCookie(&http.Cookie{Name: "django_language", Value: language})
		request.AddCookie(&http.Cookie{Name: "csrftoken", Value: strings.Repeat("c", 52)})
		if viewer != nil {
			request = platform.WithActor(request, *viewer)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Cache-Control") != "private, no-cache" {
			t.Fatalf("actual registered current %s returned %d: %.400s", path, response.Code, response.Body.String())
		}
		return response.Body.String()
	}
	for _, item := range routes {
		for _, language := range []string{"en", "ro"} {
			t.Run(item.name+"/"+language, func(t *testing.T) {
				var viewer *platform.Actor
				if !item.anonymous {
					viewer = &actor
				}
				body := read(t, routeURL(item.name, item.args...)+item.query, language, viewer)
				if item.name != "person_card" && (!strings.Contains(body, `<main`) || !strings.Contains(body, `lang="`+language+`"`)) {
					t.Fatal("actual Go shell/language unavailable")
				}
				if item.name == "person_card" && (strings.Contains(body, "<html") || !strings.Contains(body, "person-card")) {
					t.Fatal("person card stopped being a routed fragment")
				}
			})
		}
	}
	// These global aliases are existing Go public producers, distinct from
	// named web-template destinations. Exercise their actual registered mux
	// responses without a session; an unset IndexNow key remains unavailable.
	for _, test := range []struct {
		path   string
		status int
		kind   string
	}{
		{"/robots.txt", http.StatusOK, "text/plain; charset=utf-8"},
		{"/llms.txt", http.StatusOK, "text/markdown; charset=utf-8"},
		{"/indexnow.txt", http.StatusNotFound, ""},
		{"/sitemap.xml", http.StatusOK, "application/xml; charset=utf-8"},
	} {
		t.Run("public-download"+test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, s.Config.PublicURL+test.path, nil)
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("registered public download returned %d: %.300s", response.Code, response.Body.String())
			}
			if test.status != http.StatusOK {
				return
			}
			if response.Header().Get("Content-Type") != test.kind || response.Header().Get("Cache-Control") != "public, max-age=3600" {
				t.Fatalf("registered public download headers changed: %v", response.Header())
			}
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("registered public download created account/CSRF cookies")
			}
			if test.path != "/sitemap.xml" {
				return
			}
			var sitemap struct {
				Locations []string `xml:"url>loc"`
			}
			if err := xml.Unmarshal(response.Body.Bytes(), &sitemap); err != nil || len(sitemap.Locations) == 0 {
				t.Fatalf("registered sitemap was not a populated XML urlset: %v", err)
			}
			placeFound, eventFound := false, false
			for _, location := range sitemap.Locations {
				target, err := url.Parse(location)
				if err != nil || target.Scheme == "" || target.Host == "" {
					t.Fatalf("sitemap location invalid: %q", location)
				}
				placeFound = placeFound || strings.HasPrefix(target.Path, fmt.Sprintf("/places/%d/", place))
				eventFound = eventFound || strings.HasPrefix(target.Path, fmt.Sprintf("/events/%d/", event))
				for _, private := range []string{"/activities/", "/people/", "/profile/", "/account/", "/wards/", "/guardianship/", "/my-privacy/", "/my-safety-record/", "/messages/", "/notifications/", "/groups/", "/communities/"} {
					if strings.HasPrefix(target.Path, private) {
						t.Fatalf("sitemap exposed private/cohort route %q", location)
					}
				}
			}
			if !placeFound || !eventFound {
				t.Fatal("sitemap omitted the actual public venue/event fixtures")
			}
		})
	}
	// Blocking preserves the existing member's minimal escape path and removes
	// private thread, venue and logistics. It must also use the current Go view.
	if _, err = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, actor.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	for _, language := range []string{"en", "ro"} {
		t.Run("blocked-member-safe-exit/"+language, func(t *testing.T) {
			body := read(t, routeURL("activity_detail", activity), language, &peer)
			if !strings.Contains(body, routeURL("activity_leave", activity)) || !strings.Contains(body, routeURL("activity_unsafe", activity)) {
				t.Fatal("current safe-exit actions unavailable")
			}
			for _, private := range []string{"Member-only north gate", "Member-only water", "Member-only welcome", "Current completion venue"} {
				if strings.Contains(body, private) {
					t.Fatal("safe exit disclosed private activity details")
				}
			}
		})
	}
}
