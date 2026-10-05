package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestWebCasePortInterestPickerGroupsSelectionAndStarter(t *testing.T) {
	s, actor, place, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	category := func(slug, name string) int64 {
		var id int64
		if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES($1,$2,NULL,'',now(),now()) RETURNING id`, slug, name).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, last := category("case-port-first", "AA port category"), category("case-port-last", "ZZ port category")
	typeID := func(slug, name string, category int64, active bool) int64 {
		var id int64
		if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitytype(slug,name,category_id,parent_id,aliases,is_active,wellness,family_friendly,created_at,updated_at) VALUES($1,$2,$3,NULL,'[]',$4,false,false,now(),now()) RETURNING id`, slug, name, category, active).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	typeID("case-port-chosen", "Case selected choice", first, true)
	starterID := typeID("case-port-starter", "Case nearby starter", last, true)
	typeID("case-port-other", "Case ordinary choice", last, true)
	typeID("case-port-inactive", "Private inactive choice marker", last, false)
	if _, err := s.Recommendations.SetInterests(ctx, actor, []string{"case-port-chosen"}); err != nil {
		t.Fatal(err)
	}
	owner := testdb.Actor(t, s.DB, "web-case-starter-owner", "adult")
	if _, err := s.Social.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: starterID, Title: "Actual starter supply", StartsAt: time.Now().Add(24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	t.Run("grouped_selected_starter_context_and_html", func(t *testing.T) {
		r := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local/interests/", nil), actor)
		data, err := s.interestPage(r, actor)
		if err != nil {
			t.Fatal(err)
		}
		if data["chosen_count"] != 1 || !data["chosen"].(map[string]bool)["case-port-chosen"] {
			t.Fatal("selected context lost declared interest")
		}
		starter := data["starter"].([]map[string]any)
		if len(starter) != 1 || starter[0]["slug"] != "case-port-starter" {
			t.Fatal("starter does not reflect actual undeclared upcoming supply")
		}
		counts, groupPositions := map[string]int{}, map[string]int{}
		for i, group := range data["groups"].([][]any) {
			groupPositions[group[0].(map[string]any)["slug"].(string)] = i
			for _, option := range group[1].([]map[string]any) {
				counts[option["slug"].(string)]++
			}
		}
		if counts["case-port-chosen"] != 1 || counts["case-port-other"] != 1 || counts["case-port-starter"] != 0 || counts["case-port-inactive"] != 0 || groupPositions["case-port-first"] >= groupPositions["case-port-last"] {
			t.Fatal("category grouping/order/inactive/starter duplicate contract", counts)
		}
		body := webCasePortHTML(t, mux, actor, "/interests/")
		webCasePortContains(t, body, "AA port category", "ZZ port category", "1 selected.", "Popular near you right now", "Case nearby starter")
		webCasePortAbsent(t, body, "Private inactive choice marker")
		if strings.Count(body, `value="case-port-starter"`) != 1 || !regexp.MustCompile(`(?s)<input[^>]*value="case-port-chosen"[^>]*checked`).MatchString(body) {
			t.Fatal("HTML starter duplication or unchecked selected choice")
		}
		// Production page() seeds the legacy context with EnsureCSRF before
		// BuildSPA. Carry the real registered GET cookie into this direct adapter
		// assertion instead of bypassing the private payload's CSRF requirement.
		seed := webCasePortRead(mux, actor, "/interests/")
		for _, cookie := range seed.Result().Cookies() {
			if cookie.Name == "csrftoken" {
				data["csrf"] = cookie.Value
			}
		}
		if seed.Code != 200 || len(spaText(data["csrf"])) != 52 {
			t.Fatal("registered private picker did not seed CSRF")
		}
		payload, _, _, _, err := s.BuildSPA(r.Context(), r, actor, "interests", data)
		if err != nil || spaMap(payload["data"])["chosenCount"] != 1 {
			t.Fatal("SPA picker did not reuse selected legacy context", err)
		}
		spaStarter := spaRows(spaMap(payload["data"])["starter"])
		if len(spaStarter) != 1 || spaStarter[0]["slug"] != "case-port-starter" {
			t.Fatal("SPA picker lost actual starter supply")
		}
	})
	t.Run("singleton_post_persists_then_profile_renders", func(t *testing.T) {
		seed := webCasePortRead(mux, actor, "/interests/")
		var csrf *http.Cookie
		for _, cookie := range seed.Result().Cookies() {
			if cookie.Name == "csrftoken" {
				csrf = cookie
			}
		}
		if seed.Code != 200 || csrf == nil {
			t.Fatal("interest form CSRF unavailable", seed.Code)
		}
		values := url.Values{"interests": {"case-port-starter"}, "csrfmiddlewaretoken": {csrf.Value}}
		r := platform.WithActor(httptest.NewRequest("POST", "https://fixture.local/interests/", strings.NewReader(values.Encode())), actor)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://fixture.local")
		r.AddCookie(csrf)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 302 || w.Header().Get("Location") != "/" {
			t.Fatal("singleton interest POST did not preserve original redirect", w.Code, w.Body.String())
		}
		slugs, err := s.Recommendations.InterestSlugs(ctx, actor)
		if err != nil || !reflect.DeepEqual(slugs, []string{"case-port-starter"}) {
			t.Fatal("singleton selection was not stored for subject", err, slugs)
		}
		webCasePortHTML(t, mux, actor, "/profile/")
	})
	for _, test := range []struct {
		name         string
		values, want []string
	}{
		{"known_only_unknown_ignored", []string{"case-port-chosen", "not-a-taxonomy-choice"}, []string{"case-port-chosen"}},
		{"empty_selection_clears", []string{}, []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			seed := webCasePortRead(mux, actor, "/interests/")
			var csrf *http.Cookie
			for _, cookie := range seed.Result().Cookies() {
				if cookie.Name == "csrftoken" {
					csrf = cookie
				}
			}
			if seed.Code != 200 || csrf == nil {
				t.Fatal("interest form CSRF unavailable", seed.Code)
			}
			values := url.Values{"interests": test.values, "csrfmiddlewaretoken": {csrf.Value}}
			r := platform.WithActor(httptest.NewRequest("POST", "https://fixture.local/interests/", strings.NewReader(values.Encode())), actor)
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", "https://fixture.local")
			r.AddCookie(csrf)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 302 || w.Header().Get("Location") != "/" {
				t.Fatal("interest selection redirect", w.Code, w.Body.String())
			}
			slugs, err := s.Recommendations.InterestSlugs(ctx, actor)
			if err != nil || !reflect.DeepEqual(slugs, test.want) {
				t.Fatal("known-only/empty selection contract", err, slugs)
			}
		})
	}
}
