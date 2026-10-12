package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/flosch/pongo2/v6"
)

func TestEventsTemplateLegacyContextDefaults(t *testing.T) {
	s := &Server{Renderer: NewRenderer("../../../..")}
	w := httptest.NewRecorder()
	// The shared offline Python template still receives its original context.
	data := pongo2.Context{"user": socialActor(platform.Actor{}), "query": "Music", "activity": "basketball", "events": []any{}, "areas": []any{}, "filtered": true, "csrf": "fixture"}
	if err := s.Renderer.Render(w, httptest.NewRequest("GET", "/events/?q=Music", nil), "web/events.html", data); err != nil {
		t.Fatal(err)
	}
	webCasePortContains(t, w.Body.String(), `href="`+routeURL("events_feed")+`"`, `href="`+routeURL("events_list")+`"`, "Clear filters")
	webCasePortAbsent(t, w.Body.String(), `href=""`)
}

func eventListNames(t *testing.T, body string) []string {
	t.Helper()
	names := []string{}
	for _, match := range regexp.MustCompile(`(?s)<script[^>]*type="application/ld\+json"[^>]*>(.*?)</script>`).FindAllStringSubmatch(body, -1) {
		var node map[string]any
		if err := json.Unmarshal([]byte(match[1]), &node); err != nil {
			t.Fatal(err)
		}
		if node["@type"] == "ItemList" {
			for _, item := range spaRows(node["itemListElement"]) {
				names = append(names, spaText(item["name"]))
			}
		}
	}
	return names
}

func TestEventCityFiltersRegisteredRoutes(t *testing.T) {
	s, _, cluj, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	s.Catalog.Register(mux) // same outer API mounting as app.register.
	bucharest := testdb.Place(t, s.DB, "Capital Hall", "osm")
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_city=CASE WHEN id=$1 THEN 'Cluj-Napoca' ELSE E'\u00a0Bucureşti\t' END WHERE id IN($1,$2)`, cluj, bucharest); err != nil {
		t.Fatal(err)
	}
	_ = publicEventFixture(t, s, "Cluj Music Fixture", cluj, typ)
	capitalEvent := publicEventFixture(t, s, "Capital Music Fixture", bucharest, typ)
	_ = publicEventFixture(t, s, "Capital Other Fixture", bucharest, typ)
	var otherType int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,category_id,parent_id,aliases,is_active,wellness,family_friendly,created_at,updated_at) SELECT 'city-other','Other',category_id,NULL,'[]',true,false,false,now(),now() FROM taxonomy_activitytype WHERE id=$1 RETURNING id`, typ).Scan(&otherType); err != nil {
		t.Fatal(err)
	}
	_ = publicEventFixture(t, s, "Capital Music Other Type", bucharest, otherType)
	held := publicEventFixture(t, s, "Capital Music Held", bucharest, typ)
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET is_import_held=true WHERE id=$1`, held); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO communities_area(city,slug,name,derive_method,min_radius_m,is_active,created_at) VALUES('București','capital','Capital','city',0,true,now())`); err != nil {
		t.Fatal(err)
	}
	var slug string
	if err := s.DB.QueryRow(ctx, `SELECT slug FROM taxonomy_activitytype WHERE id=$1`, typ).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	for _, city := range []string{"București", "Bucuresti", "Bucharest", "  BUCUREŞTI  ", "Bucures\u0326ti", "Bucures\u0327ti"} {
		query := url.Values{"city": {city}, "q": {"Music"}, "activity": {slug}, "area": {"capital"}}
		path := "/events/?" + query.Encode()
		body := webCasePortHTML(t, mux, platform.Actor{}, path)
		if names := eventListNames(t, body); !reflect.DeepEqual(names, []string{"Capital Music Fixture"}) {
			t.Fatal(city, "visible/structured city intersection", names)
		}
		webCasePortAbsent(t, body, "Cluj Music Fixture", "Capital Other Fixture", "Capital Music Other Type", "Capital Music Held")
		webCasePortContains(t, body, `name="city" value="`+html.EscapeString(strings.TrimSpace(city))+`"`, "noindex, follow")
	}
	// Captured city facts may themselves be decomposed; comparisons normalize
	// spelling while leaving that stored fact unchanged.
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_city=$2 WHERE id=$1`, bucharest, "Bucures\u0326ti"); err != nil {
		t.Fatal(err)
	}
	body := webCasePortHTML(t, mux, platform.Actor{}, "/events/?city=Bucuresti&q=Music&activity="+slug)
	webCasePortContains(t, body, "Capital Music Fixture")
	webCasePortContains(t, webCasePortHTML(t, mux, platform.Actor{}, fmt.Sprintf("/events/%d/", capitalEvent)), "/things-to-do/capital/")
	places := webCasePortRead(mux, platform.Actor{}, "/api/v1/places/?city=Bucharest")
	if places.Code != 200 || !strings.Contains(places.Body.String(), "Capital Hall") || strings.Contains(places.Body.String(), "Cluj-Napoca") {
		t.Fatal("place API city alias", places.Code, places.Body.String())
	}
	for _, path := range []string{"/events/?city=Unknown", "/events/?city=Cluj-Napoca&area=capital"} {
		if names := eventListNames(t, webCasePortHTML(t, mux, platform.Actor{}, path)); len(names) != 0 {
			t.Fatal("unknown/incompatible city widened scope", path, names)
		}
	}
	// The same city comparison is shared by catalog APIs and RSS/Atom. Search and
	// history remain deliberately absent from subscriptions.
	for _, path := range []string{"/api/v1/events/?city=Bucharest&q=Music&activity=" + slug, "/events/feed/?city=Bucharest&activity=" + slug + "&q=ignored&past_only=1", routeURL("events_feed_atom") + "?city=Bucharest&activity=" + slug} {
		w := webCasePortRead(mux, platform.Actor{}, path)
		if w.Code != 200 || strings.Contains(w.Body.String(), "Cluj Music Fixture") || !strings.Contains(w.Body.String(), "Capital Music Fixture") {
			t.Fatal("catalog/feed city mismatch", path, w.Code, w.Body.String())
		}
	}
	s.Config.SPA = true
	path := "/events/?city=Bucuresti&q=Music&activity=" + slug + "&area=capital"
	body = webCasePortHTML(t, mux, platform.Actor{}, path)
	if names := eventListNames(t, body); !reflect.DeepEqual(names, []string{"Capital Music Fixture"}) {
		t.Fatal("SPA snapshot differs from filtered JSON-LD", names)
	}
	payload := webCasePort3Payload(t, mux, platform.Actor{}, path)
	data := spaMap(payload["data"])
	rows := spaRows(data["events"])
	if len(rows) != 1 || rows[0]["title"] != "Capital Music Fixture" || spaMap(data["filters"])["city"] != "Bucuresti" {
		t.Fatal("soft-navigation lost city scope", data)
	}
	feed, err := url.Parse(spaText(spaMap(data["urls"])["rss"]))
	if err != nil || feed.Query().Get("city") != "Bucuresti" || feed.Query().Get("area") != "capital" || feed.Query().Get("activity") != slug || feed.Query().Get("q") != "" {
		t.Fatal("subscription lost source filters", feed, err)
	}
}

func TestLocalPOCPastOnlyRegisteredRoutes(t *testing.T) {
	s, actor, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	other := testdb.Place(t, s.DB, "Elsewhere Hall", "osm")
	if _, err := s.DB.Exec(ctx, `UPDATE places_place SET address_city=CASE WHEN id=$1 THEN 'București' ELSE 'Cluj-Napoca' END WHERE id IN($1,$2)`, place, other); err != nil {
		t.Fatal(err)
	}
	// More upcoming rows than the public list cap: compatibility mode cannot
	// reach history, while the explicit past-only view must return it.
	_ = publicEventFixture(t, s, "Future Fixture 000", place, typ)
	for n := 1; n <= 100; n++ {
		_ = publicEventFixture(t, s, fmt.Sprintf("Future Fixture %03d", n), place, typ)
	}
	allowed := publicEventFixture(t, s, "Past Fixture Capital", place, typ)
	elsewhere := publicEventFixture(t, s, "Past Fixture Elsewhere", other, typ)
	production := publicEventFixture(t, s, "Past Fixture Production", place, typ)
	held := publicEventFixture(t, s, "Past Fixture Held", place, typ)
	cancelled := publicEventFixture(t, s, "Past Fixture Cancelled", place, typ)
	tombstone := publicEventFixture(t, s, "Past Fixture Tombstone", place, typ)
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET starts_at=now()-interval '2 days',lifecycle_status='expired' WHERE id=ANY($1)`, []int64{allowed, elsewhere, production, held, cancelled, tombstone}); err != nil {
		t.Fatal(err)
	}
	proposal, err := s.Social.ProposePlace(ctx, actor, social.PlaceProposalInput{Name: "Unpublished History Venue", Lon: 24.8, Lat: 46.2, ActivityType: typ, AllowNearby: true})
	if err != nil {
		t.Fatal(err)
	}
	var pendingPlace int64
	if err = s.DB.QueryRow(ctx, `SELECT place_id FROM social_userplaceproposal WHERE id=$1`, proposal).Scan(&pendingPlace); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE places_place SET address_city='București' WHERE id=$1`, pendingPlace); err != nil {
		t.Fatal(err)
	}
	pendingEvent := publicEventFixture(t, s, "Past Fixture Unpublished", pendingPlace, typ)
	if _, err = s.DB.Exec(ctx, `UPDATE events_event SET starts_at=now()-interval '2 days',lifecycle_status='expired' WHERE id=$1`, pendingEvent); err != nil {
		t.Fatal(err)
	}
	// Gate and column both absent: history query parameters never enable history.
	webCasePortAbsent(t, webCasePortHTML(t, mux, platform.Actor{}, "/events/?past_only=1&include_past=1"), "Past Fixture")
	if _, err := s.DB.Exec(ctx, `ALTER TABLE events_event ADD COLUMN source_local_poc boolean NOT NULL DEFAULT false`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET source_local_poc=true WHERE id<>$1`, production); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET is_import_held=true WHERE id=$1`, held); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET lifecycle_status='cancelled' WHERE id=$1`, cancelled); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE events_event SET is_tombstone=true WHERE id=$1`, tombstone); err != nil {
		t.Fatal(err)
	}
	webCasePortAbsent(t, webCasePortHTML(t, mux, platform.Actor{}, "/events/?past_only=1&include_past=1"), "Past Fixture")
	s.Config.LocalPOC = true
	if names := eventListNames(t, webCasePortHTML(t, mux, platform.Actor{}, "/events/?city=Bucuresti&include_past=1")); len(names) != 100 || strings.HasPrefix(names[0], "Past") {
		t.Fatal("compatibility mode/order/cap changed", len(names))
	}
	if names := eventListNames(t, webCasePortHTML(t, mux, platform.Actor{}, "/events/?city=Bucuresti&past_only=1")); !reflect.DeepEqual(names, []string{"Past Fixture Capital"}) {
		t.Fatal("upcoming rows hid history in past-only view", names)
	}
	query := url.Values{"city": {"  Bucuresti  "}, "q": {"Past Fixture"}, "activity": {"basketball"}, "past_only": {"1"}, "include_past": {"1"}}
	var slug string
	if err := s.DB.QueryRow(ctx, `SELECT slug FROM taxonomy_activitytype WHERE id=$1`, typ).Scan(&slug); err != nil {
		t.Fatal(err)
	}
	query.Set("activity", slug)
	path := "/events/?" + query.Encode()
	body := webCasePortHTML(t, mux, platform.Actor{}, path)
	if names := eventListNames(t, body); !reflect.DeepEqual(names, []string{"Past Fixture Capital"}) {
		t.Fatal("history gate/city/held/lifecycle/JSON-LD mismatch", names)
	}
	webCasePortContains(t, body, localPOCMarkerHTML, "Past event", "noindex, follow", `name="past_only" value="1"`)
	webCasePortAbsent(t, body, "Future Fixture", "Past Fixture Elsewhere", "Past Fixture Production", "Past Fixture Held", "Past Fixture Cancelled", "Past Fixture Tombstone", "Past Fixture Unpublished")
	modeLinks := 0
	for _, text := range regexp.MustCompile(`href="([^"]+)"`).FindAllStringSubmatch(body, -1) {
		link, err := url.Parse(html.UnescapeString(text[1]))
		if err != nil || link.Path != "/events/" || link.Query().Get("city") == "" {
			continue
		}
		if link.Query().Get("city") != "Bucuresti" || link.Query().Get("q") != "Past Fixture" || link.Query().Get("activity") != slug {
			t.Fatal("view navigation dropped filters", link)
		}
		modeLinks++
	}
	if modeLinks != 2 {
		t.Fatal("history navigation must offer both upcoming and compatibility views", modeLinks)
	}
	webCasePortContains(t, body, `href="/events/?past_only=1"`)
	s.Config.SPA = true // presentation tests do not alter the CLI's React+POC refusal.
	payload := webCasePort3Payload(t, mux, platform.Actor{}, path)
	data := spaMap(payload["data"])
	rows := spaRows(data["events"])
	if len(rows) != 1 || rows[0]["title"] != "Past Fixture Capital" || rows[0]["localPocMarker"] != localPOCMarkerHTML || rows[0]["pastLabel"] != "Past event" || spaMap(data["localPOC"])["pastOnly"] != true {
		t.Fatal("SPA lost history/provenance", data)
	}
	webCasePortContains(t, webCasePortHTML(t, mux, platform.Actor{}, path), localPOCMarkerHTML, "Past event")
	sitemap := httptest.NewRecorder()
	if !s.PublicDownload(sitemap, httptest.NewRequest("GET", "/sitemap.xml?past_only=1&include_past=1", nil), platform.Actor{}, "sitemap") || sitemap.Code != 200 || strings.Contains(sitemap.Body.String(), fmt.Sprintf("/events/%d/", allowed)) {
		t.Fatal("history escaped to sitemap publication adapter", sitemap.Code)
	}
	// Feeds and sitemap remain upcoming-only despite history parameters.
	for _, feed := range []string{"/events/feed/?past_only=1&include_past=1&city=Bucuresti"} {
		w := webCasePortRead(mux, platform.Actor{}, feed)
		if w.Code != 200 || strings.Contains(w.Body.String(), "Past Fixture Capital") || strings.Contains(w.Body.String(), fmt.Sprintf("/events/%d/", allowed)) {
			t.Fatal("history escaped to crawler/feed", feed, w.Code)
		}
	}
}
