package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func webCasePort3Payload(t *testing.T, mux *http.ServeMux, actor platform.Actor, path string) map[string]any {
	t.Helper()
	separator := "?"
	if strings.Contains(path, "?") {
		separator = "&"
	}
	w := webCasePortRead(mux, actor, path+separator+"_data=1")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("soft-navigation route %s status%d content-type%s body%s", path, w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	var payload map[string]any
	decoder := json.NewDecoder(strings.NewReader(w.Body.String()))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestWebCasePort3SPAFlagsPrivateScreensAndPopulatedSoftNavigation(t *testing.T) {
	s, actor, place, typ, mux := webCasePortFixture(t)
	activity := socialLegacyActivity(t, s, actor, place, typ, "Card contract")
	screens := []struct{ path, name, template, route string }{
		{"/", "home", "web/home.html", "home"},
		{"/activities/", "activity_list", "web/activities.html", "browse"},
		{"/organize/", "organize", "web/organize.html", "organize"},
		{"/you/", "you", "web/you.html", "you"},
		{"/settings/", "settings", "web/settings.html", "settings"},
		{"/profile/", "profile", "web/profile.html", "profile"},
		{"/interests/", "interests", "web/interests.html", "interests"},
		{"/topics/", "topic_preferences", "web/topic_preferences.html", "topics"},
		{"/access/", "access_preferences", "web/access_preferences.html", "access"},
		{"/notifications/", "notifications", "web/notifications.html", "notifications"},
		{"/notifications/preferences/", "notification_preferences", "web/notification_preferences.html", "notification-preferences"},
		{"/connections/", "connections", "web/connections.html", "connections"},
		{"/saved-searches/", "saved_searches", "web/saved_searches.html", "saved-searches"},
		{"/communities/", "communities", "web/communities.html", "communities"},
	}
	t.Run("flag_off_exact_legacy_templates", func(t *testing.T) {
		for _, screen := range screens {
			r := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local"+screen.path, nil), actor)
			_, template, err := s.view(r, actor, screen.name)
			if err != nil || template != screen.template {
				t.Fatal("source legacy template choice", screen.path, template, err)
			}
			webCasePortAbsent(t, webCasePortHTML(t, mux, actor, screen.path), `id="spa-bootstrap"`)
		}
	})
	s.Config.SPA = true
	t.Run("flag_on_exact_shell_route_and_csrf", func(t *testing.T) {
		for _, screen := range screens {
			webCasePortContains(t, webCasePortHTML(t, mux, actor, screen.path), `id="spa-bootstrap"`, `data-route="`+screen.route+`"`)
			payload := webCasePort3Payload(t, mux, actor, screen.path)
			if payload["route"] != screen.route || spaText(payload["csrf"]) == "" {
				t.Fatal("source private SPA route/CSRF", screen.path, payload["route"])
			}
		}
	})
	t.Run("anonymous_home_stays_landing", func(t *testing.T) {
		_, template, err := s.view(httptest.NewRequest("GET", "https://fixture.local/", nil), platform.Actor{}, "home")
		if err != nil || template != "web/landing.html" {
			t.Fatal("source anonymous landing template", template, err)
		}
		webCasePortAbsent(t, webCasePortHTML(t, mux, platform.Actor{}, "/"), `id="spa-bootstrap"`)
	})
	t.Run("populated_browse_home_organize_and_coverless_card", func(t *testing.T) {
		payload := webCasePort3Payload(t, mux, actor, "/activities/")
		cards := spaRows(spaMap(payload["data"])["cards"])
		if payload["route"] != "browse" || spaText(payload["title"]) == "" || len(cards) != 1 || spaID(cards[0]) != activity || spaInt(spaMap(spaMap(payload["data"])["page"])["count"]) != 1 {
			t.Fatal("source populated browse payload", payload)
		}
		card := cards[0]
		visual := spaMap(card["visual"])
		tags, _ := card["tags"].([]any)
		if card["url"] != fmt.Sprintf("/activities/%d/", activity) || card["title"] != "Card contract" || visual["kind"] != "accent" || !strings.HasPrefix(spaText(visual["svg"]), "<svg") || len(tags) == 0 || tags[0] != "Basketball" || !strings.Contains(spaText(card["meta"]), "·") || card["score"] != nil {
			t.Fatal("source coverless activity-card contract", card)
		}
		home := webCasePort3Payload(t, mux, actor, "/")
		if home["route"] != "home" {
			t.Fatal("source home payload route")
		}
		for _, key := range []string{"sections", "starterTypes", "events", "ui", "urls"} {
			if _, exists := spaMap(home["data"])[key]; !exists {
				t.Fatal("source home payload missing key", key)
			}
		}
		organize := webCasePort3Payload(t, mux, actor, "/organize/")
		owned := spaRows(spaMap(organize["data"])["activities"])
		if organize["route"] != "organize" || len(owned) != 1 || spaID(owned[0]) != activity {
			t.Fatal("source organizer activity binding", organize)
		}
	})
}

func TestWebCasePort3PublicSPAContainsCrawlerSnapshotAndTokenlessData(t *testing.T) {
	s, _, place, typ, mux := webCasePortFixture(t)
	s.Config.SPA = true
	event := publicEventFixture(t, s, "Saturday spa football", place, typ)
	t.Run("events_snapshot_jsonld_rss_filters_and_pk", func(t *testing.T) {
		body := webCasePortHTML(t, mux, platform.Actor{}, "/events/")
		webCasePortContains(t, body, `id="spa-bootstrap"`, "Saturday spa football", `type="application/ld+json"`, `"ItemList"`, `rel="alternate" type="application/rss+xml"`)
		payload := webCasePort3Payload(t, mux, platform.Actor{}, "/events/")
		events := spaRows(spaMap(payload["data"])["events"])
		if payload["route"] != "events" || payload["csrf"] != "" || len(events) != 1 || spaID(events[0]) != event {
			t.Fatal("source public event payload", payload)
		}
		webCasePortContains(t, webCasePortHTML(t, mux, platform.Actor{}, "/events/?q=football"), "noindex, follow")
	})
	t.Run("places_snapshot_and_public_payload", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, platform.Actor{}, "/places/list/"), `data-route="places"`, "Library &amp; Hall")
		payload := webCasePort3Payload(t, mux, platform.Actor{}, "/places/list/")
		places := spaRows(spaMap(payload["data"])["places"])
		if payload["route"] != "places" || len(places) != 1 || places[0]["name"] != "Library & Hall" {
			t.Fatal("source public place payload", payload)
		}
	})
	t.Run("things_index_shell_and_city_key", func(t *testing.T) {
		webCasePortContains(t, webCasePortHTML(t, mux, platform.Actor{}, "/things-to-do/"), `data-route="things-index"`)
		payload := webCasePort3Payload(t, mux, platform.Actor{}, "/things-to-do/")
		_, exists := spaMap(payload["data"])["cities"]
		if payload["route"] != "things-index" || !exists {
			t.Fatal("source things index payload", payload)
		}
	})
}

func TestWebCasePort3SPAAccountNavigationAndExistingMutationServices(t *testing.T) {
	s, actor, place, _, mux := webCasePortFixture(t)
	s.Config.SPA = true
	ctx := context.Background()
	t.Run("one_account_nav_and_retired_inbox_tabs", func(t *testing.T) {
		you := spaMap(webCasePort3Payload(t, mux, actor, "/you/")["data"])
		settings := spaMap(webCasePort3Payload(t, mux, actor, "/settings/")["data"])
		titles := func(data map[string]any) []string {
			out := []string{}
			for _, group := range spaRows(spaMap(data["nav"])["groups"]) {
				out = append(out, spaText(group["title"]))
			}
			return out
		}
		if !reflect.DeepEqual(titles(you), titles(settings)) || !reflect.DeepEqual(you["tabs"], settings["tabs"]) || spaText(spaMap(you["nav"])["logoutAction"]) == "" {
			t.Fatal("source account navigation single source")
		}
		labels := map[string]bool{}
		for _, group := range spaRows(spaMap(you["nav"])["groups"]) {
			if group["title"] == "Inbox" {
				t.Fatal("source retired Inbox navigation group")
			}
			for _, link := range spaRows(group["links"]) {
				labels[spaText(link["label"])] = true
			}
		}
		for _, label := range []string{"Notifications", "Messages", "Connections"} {
			if !labels[label] {
				t.Fatal("source account navigation missing label", label)
			}
		}
		for _, path := range []string{"/notifications/", "/connections/"} {
			if _, exists := spaMap(webCasePort3Payload(t, mux, actor, path)["data"])["tabs"]; exists {
				t.Fatal("source retired inbox payload tabs", path)
			}
		}
	})
	t.Run("interests_topics_access_notices_and_saved_search_roundtrip", func(t *testing.T) {
		// The original fixture's activity type belongs to a top-level category.
		// The seeded basketball category is nested and intentionally cannot be
		// selected as a self-steering topic.
		var categoryID, typ int64
		if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES('case-port3-spa-sport','Case port3 SPA sport',NULL,'',now(),now()) RETURNING id`).Scan(&categoryID); err != nil {
			t.Fatal(err)
		}
		if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,category_id,parent_id,aliases,is_active,wellness,family_friendly,created_at,updated_at) VALUES('case-port3-spa-bball','Case port3 SPA basketball',$1,NULL,'[]',true,false,false,now(),now()) RETURNING id`, categoryID).Scan(&typ); err != nil {
			t.Fatal(err)
		}
		socialLegacyActivity(t, s, actor, place, typ, "P3 mutation seed")
		var typeSlug, category string
		if err := s.DB.QueryRow(ctx, `SELECT t.slug,c.slug FROM taxonomy_activitytype t JOIN taxonomy_activitycategory c ON c.id=t.category_id WHERE t.id=$1`, typ).Scan(&typeSlug, &category); err != nil {
			t.Fatal(err)
		}
		for _, step := range []struct {
			path   string
			values url.Values
		}{
			{"/interests/", url.Values{"interests": {typeSlug}}},
			{"/topics/", url.Values{"topics": {category}}},
			{"/access/", url.Values{"needs_step_free": {"on"}, "prefers_quiet": {"on"}}},
			{"/notifications/preferences/", url.Values{"muted": {"activity_match"}}},
		} {
			if w := webCasePort2Post(t, mux, actor, step.path, step.path, step.values); w.Code != 302 {
				t.Fatal("source SPA form mutation redirect", step.path, w.Code, w.Body.String())
			}
		}
		interests, err := s.Recommendations.InterestSlugs(ctx, actor)
		if err != nil || !reflect.DeepEqual(interests, []string{typeSlug}) {
			t.Fatal("source exact stored interests", err, interests)
		}
		topics, err := s.Recommendations.TopicSlugs(ctx, actor)
		if err != nil || !reflect.DeepEqual(topics, []string{category}) {
			t.Fatal("source exact stored topics", err, topics)
		}
		var stepFree, quiet, hearing bool
		if err := s.DB.QueryRow(ctx, `SELECT needs_step_free,prefers_quiet,needs_hearing_loop FROM places_accesspreference WHERE user_id=$1`, actor.ID).Scan(&stepFree, &quiet, &hearing); err != nil || !stepFree || !quiet || hearing {
			t.Fatal("source typed access checkboxes", err, stepFree, quiet, hearing)
		}
		var muted []string
		if err := s.DB.QueryRow(ctx, `SELECT muted_kinds FROM notifications_notificationpreference WHERE user_id=$1`, actor.ID).Scan(&muted); err != nil || !reflect.DeepEqual(muted, []string{"activity_match"}) {
			t.Fatal("source exact stored notice mute", err, muted)
		}
		w := webCasePort2Post(t, mux, actor, "/saved-searches/", "/saved-searches/create/", url.Values{"activity_type": {typeSlug}, "next": {"/saved-searches/"}})
		if w.Code != 302 {
			t.Fatal("source saved search creation redirect", w.Code, w.Body.String())
		}
		var search, savedType int64
		if err := s.DB.QueryRow(ctx, `SELECT id,activity_type_id FROM saved_searches_savedsearch WHERE user_id=$1`, actor.ID).Scan(&search, &savedType); err != nil || savedType != typ {
			t.Fatal("source saved type binding", err, search, savedType)
		}
		w = webCasePort2Post(t, mux, actor, "/saved-searches/", fmt.Sprintf("/saved-searches/%d/delete/", search), url.Values{"next": {"/saved-searches/"}})
		var remains bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saved_searches_savedsearch WHERE id=$1)`, search).Scan(&remains); err != nil || remains || w.Code != 302 {
			t.Fatal("source saved search deletion", err, remains, w.Code)
		}
	})
}

func TestWebCasePort3SPACommunityDetailBindsPublishedName(t *testing.T) {
	s, actor, _, typ, mux := webCasePortFixture(t)
	s.Config.SPA = true
	ctx := context.Background()
	var area, category int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO communities_area(city,slug,name,derive_method,min_radius_m,is_active,created_at) VALUES('Cluj-Napoca','cluj-napoca','Cluj-Napoca','city',0,true,now()) RETURNING id`).Scan(&area); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT category_id FROM taxonomy_activitytype WHERE id=$1`, typ).Scan(&category); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO communities_community(cohort,area_id,category_id,activity_type_id,tier,slug,name,is_published,last_evaluated_at,created_at) VALUES('adult',$1,$2,$3,'type','basketball-cluj','Basketball in Cluj',true,now(),now())`, area, category, typ); err != nil {
		t.Fatal(err)
	}
	payload := webCasePort3Payload(t, mux, actor, "/communities/basketball-cluj/")
	if payload["route"] != "community-detail" || spaMap(payload["data"])["name"] != "Basketball in Cluj" {
		t.Fatal("source SPA community detail route/name", payload)
	}
}
