package web

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func publicEventFixture(t *testing.T, s *Server, title string, place, typ int64) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRow(context.Background(), `INSERT INTO events_event(title,description,starts_at,ends_at,url,source,external_id,created_at,updated_at,activity_type_id,place_id,attribution,license_name,provenance_url,is_import_held,is_tombstone,lifecycle_status,source_category,source_city,source_confidence,source_first_seen_at,source_last_seen_at,source_pack_id,source_release_id,source_snapshot_generated_at,source_snapshot_id,source_updated_at,source_venue_id,source_availability,source_currency,source_is_free,source_price_max,source_price_min,source_recurrence,source_timezone) VALUES($1::text,'Synthetic description',now()+interval '1 day',NULL,'https://venue.fixture/event','ical','fixture-'||$1::text,now(),now(),$2,$3,'Licensed fixture','CC BY 4.0','https://venue.fixture/source',false,false,'scheduled','','Cluj-Napoca',1,NULL,NULL,'','',NULL,'',NULL,'','available','RON',false,15,10,'','Europe/Bucharest') RETURNING id`, title, typ, place).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func publicViewFixture(t *testing.T, s *Server, a platform.Actor, name string, pk int64, path string) (map[string]any, string) {
	t.Helper()
	r := socialLegacyRequest("GET", path, a, pk, nil)
	data, template, handled, err := s.PublicView(r, a, name)
	if err != nil || !handled {
		t.Fatal(name, handled, err)
	}
	w := httptest.NewRecorder()
	data["csrf"] = "fixture"
	if err = s.Renderer.Render(w, r, template, data); err != nil {
		t.Fatal(name, err)
	}
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	return data, w.Body.String()
}
func TestPublicLegacyPlacesEventsLandingsAndPendingPrivacy(t *testing.T) {
	s, a, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,updated_at,created_at) VALUES($1,$2,'inferred',0.9,'osm','fixture',false,now(),now())`, place, typ); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO communities_area(city,slug,name,derive_method,min_radius_m,is_active,created_at) VALUES('Cluj-Napoca','cluj-napoca','Cluj-Napoca','city',0,true,now())`); err != nil {
		t.Fatal(err)
	}
	event := publicEventFixture(t, s, "Public licensed concert", place, typ)
	data, html := publicViewFixture(t, s, platform.Actor{}, "place_detail", place, "/places/"+fmt.Sprint(place)+"/")
	if !strings.Contains(html, "Library &amp; Hall") || data["structured_data"] == nil || data["attribution_credit"] != nil {
		t.Fatal("venue projection", html)
	}
	edata, ehtml := publicViewFixture(t, s, platform.Actor{}, "event_detail", event, "/events/"+fmt.Sprint(event)+"/")
	if edata["structured_data"] == nil || !strings.Contains(ehtml, "CC BY 4.0") {
		t.Fatal("event credit lost")
	}
	node := s.publicEventLD(httptest.NewRequest("GET", "https://fixture.local/", nil), spaMap(edata["event"]))
	offer := spaMap(node["offers"])
	if offer["price"] != "10.00" || offer["priceCurrency"] != "RON" {
		t.Fatal(offer)
	}
	for _, name := range []string{"places_list", "places_map", "events_list", "things_to_do_index"} {
		publicViewFixture(t, s, platform.Actor{}, name, 0, "/"+name+"/")
	}
	r := httptest.NewRequest("GET", "/things-to-do/cluj-napoca/basketball/", nil)
	r.SetPathValue("area_slug", "cluj-napoca")
	r.SetPathValue("activity_slug", "basketball")
	data, _, ok, err := s.PublicView(r, platform.Actor{}, "things_to_do")
	if err != nil || !ok || len(spaRows(data["places"])) != 1 {
		t.Fatal(data, err)
	}
	proposal, err := s.Social.ProposePlace(ctx, a, social.PlaceProposalInput{Name: "Secret pending venue", Lon: 23.8, Lat: 46.8, ActivityType: typ, AllowNearby: true})
	if err != nil {
		t.Fatal(err)
	}
	var pending int64
	if err = s.DB.QueryRow(ctx, `SELECT place_id FROM social_userplaceproposal WHERE id=$1`, proposal).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	request := socialLegacyRequest("GET", "/places/"+fmt.Sprint(pending)+"/", platform.Actor{}, pending, nil)
	_, _, _, err = s.PublicView(request, platform.Actor{}, "place_detail")
	if err == nil {
		t.Fatal("pending venue anonymous leak")
	}
	pdata, phtml := publicViewFixture(t, s, a, "place_detail", pending, "/places/"+fmt.Sprint(pending)+"/")
	if pdata["structured_data"] != nil || strings.Contains(fmt.Sprint(pdata["canonical_url"]), "secret") {
		t.Fatal("pending venue crawled", phtml)
	}
	r = httptest.NewRequest("GET", "/sitemap.xml?activity=nonexistent&q=private", nil)
	w := httptest.NewRecorder()
	s.PublicDownload(w, r, a, "sitemap")
	if w.Code != 200 || strings.Contains(w.Body.String(), "Secret pending") || strings.Contains(w.Body.String(), "/activities/") {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "public-licensed-concert") {
		t.Fatal("sitemap incorrectly accepts personalized query filters")
	}
}
func TestPublicLegacyMutationPathOwnershipAndPublicationGate(t *testing.T) {
	s, a, place, typ := socialLegacyFixture(t)
	ctx := context.Background()
	other := testdb.Place(t, s.DB, "Other fixture venue", "osm")
	var edge int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO places_placeactivity(place_id,activity_id,origin,confidence,source,mapping_rule,is_disputed,updated_at,created_at) VALUES($1,$2,'inferred',0.9,'osm','fixture',false,now(),now()) RETURNING id`, other, typ).Scan(&edge); err != nil {
		t.Fatal(err)
	}
	r := socialLegacyRequest("POST", "/places/"+fmt.Sprint(place)+"/edges/"+fmt.Sprint(edge)+"/vote/", a, place, url.Values{"vote": {"confirm"}})
	r.SetPathValue("edge_id", fmt.Sprint(edge))
	w := httptest.NewRecorder()
	if !s.PublicAction(w, r, a, "edge_vote") || w.Code != 404 {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_activityedgevote`).Scan(&n); err != nil || n != 0 {
		t.Fatal(n, err)
	}
	r = socialLegacyRequest("POST", "/places/"+fmt.Sprint(place)+"/facts/vote/", a, place, url.Values{"fact_key": {"drinking_water"}, "value": {"yes"}, "owner_id": {"999"}})
	w = httptest.NewRecorder()
	s.PublicAction(w, r, a, "fact_vote")
	if w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM places_placefactvote WHERE user_id=$1`, a.ID).Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	r = socialLegacyRequest("POST", "/places/"+fmt.Sprint(place)+"/hours-reset/", a, place, nil)
	w = httptest.NewRecorder()
	s.PublicAction(w, r, a, "place_open_now_reset")
	if w.Code != 404 {
		t.Fatal("staff gate", w.Code)
	}
}
func TestPublicCrawlerSnapshotAndStructuredEscapes(t *testing.T) {
	s := &Server{Config: Config{PublicURL: "https://fixture.local"}}
	r := httptest.NewRequest("GET", "https://fixture.local/robots.txt", nil)
	w := httptest.NewRecorder()
	if !s.PublicDownload(w, r, platform.Actor{}, "robots_txt") || w.Code != 200 {
		t.Fatal(w.Code)
	}
	if !strings.Contains(w.Body.String(), "Disallow: /activities/") || strings.Contains(w.Body.String(), "Allow: /api/\n") {
		t.Fatal(w.Body.String())
	}
	if w.Header().Get("Set-Cookie") != "" || w.Header().Get("Cache-Control") != "public, max-age=3600" {
		t.Fatal(w.Header())
	}
	unsafe := publicLD(map[string]any{"title": "</script><script>bad\u2028"}).String()
	if strings.Contains(unsafe, "</script>") || strings.ContainsRune(unsafe, '\u2028') {
		t.Fatal(unsafe)
	}
	var parsed any
	if json.Unmarshal([]byte(unsafe), &parsed) != nil {
		t.Fatal(unsafe)
	}
	root := t.TempDir()
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	s.Config.SnapshotDir = root
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(`{"schema_version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !s.publicSnapshotAvailable() {
		t.Fatal("missing manifest")
	}
	for _, file := range []string{"manifest.json", "../manifest.json", "places.json"} {
		r = httptest.NewRequest("GET", "/open-data/snapshot/fixture", nil)
		r.SetPathValue("name", file)
		w = httptest.NewRecorder()
		s.PublicDownload(w, r, platform.Actor{}, "open_data_snapshot")
		want := 404
		if file == "manifest.json" {
			want = 200
		}
		if w.Code != want {
			t.Fatal(file, w.Code)
		}
	}
	events := []map[string]any{{"id": 1, "title": "Event <safe>", "description": "<p>source text</p>", "created_at": time.Now(), "updated_at": time.Now()}}
	for _, atom := range []bool{false, true} {
		body, err := s.publicFeed(r, events, atom)
		if err != nil {
			t.Fatal(err)
		}
		var node any
		if err = xml.Unmarshal(body, &node); err != nil {
			t.Fatal(err, string(body))
		}
		if strings.Contains(string(body), "<safe>") {
			t.Fatal(string(body))
		}
	}
}
