package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func webCasePort3FetchForm(t *testing.T, mux *http.ServeMux, actor platform.Actor, target string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	seed := webCasePortRead(mux, actor, "/profile/")
	var csrf *http.Cookie
	for _, cookie := range seed.Result().Cookies() {
		if cookie.Name == "csrftoken" {
			csrf = cookie
		}
	}
	if seed.Code != 200 || csrf == nil {
		t.Fatal("source fetch form CSRF", seed.Code)
	}
	copy := url.Values{}
	for key, values := range values {
		copy[key] = append([]string{}, values...)
	}
	copy.Set("csrfmiddlewaretoken", csrf.Value)
	r := platform.WithActor(httptest.NewRequest("POST", "https://fixture.local"+target, strings.NewReader(copy.Encode())), actor)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://fixture.local")
	r.Header.Set("X-Requested-With", "fetch")
	r.AddCookie(csrf)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestWebCasePort3SavedSearchFormValidationCurrentAuthorityAndOwnership(t *testing.T) {
	s, actor, _, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	var typeSlug string
	var category int64
	if err := s.DB.QueryRow(ctx, `SELECT slug,category_id FROM taxonomy_activitytype WHERE id=$1`, typ).Scan(&typeSlug, &category); err != nil {
		t.Fatal(err)
	}
	other := testdb.Actor(t, s.DB, "case-port3-private-search-owner", "adult")
	foreign, err := s.Recommendations.CreateSavedSearch(ctx, other, recommendations.SavedSearchInput{ActivityType: &typ, City: "Other saved search private city"})
	if err != nil {
		t.Fatal(err)
	}
	var owned int64
	t.Run("accepted_slug_and_category_id_owner_scoped_render", func(t *testing.T) {
		w := webCasePort2Post(t, mux, actor, "/saved-searches/", "/saved-searches/create/", url.Values{"activity_type": {typeSlug}, "next": {"/saved-searches/"}, "beginners": {"on"}, "cost_band": {"free"}, "coarse_window": {"weekend_daytime"}})
		if w.Code != 302 || w.Header().Get("Location") != "/saved-searches/" {
			t.Fatal("source slug create redirect", w.Code, w.Header().Get("Location"))
		}
		if err := s.DB.QueryRow(ctx, `SELECT id FROM saved_searches_savedsearch WHERE user_id=$1`, actor.ID).Scan(&owned); err != nil {
			t.Fatal(err)
		}
		w = webCasePort2Post(t, mux, actor, "/saved-searches/", "/saved-searches/create/", url.Values{"category": {fmt.Sprint(category)}})
		if w.Code != 302 {
			t.Fatal("source category ID create redirect", w.Code)
		}
		body := webCasePortHTML(t, mux, actor, "/saved-searches/")
		webCasePortContains(t, body, "Saved searches", "Basketball", "Free", "Weekend daytime", fmt.Sprintf(`/saved-searches/%d/delete/`, owned))
		webCasePortAbsent(t, body, "Other saved search private city", fmt.Sprintf(`/saved-searches/%d/delete/`, foreign))
	})
	t.Run("invalid_missing_unknown_or_inactive_selection_redirect_no_city", func(t *testing.T) {
		if _, err := s.DB.Exec(ctx, `UPDATE taxonomy_activitytype SET is_active=false WHERE id=$1`, typ); err != nil {
			t.Fatal(err)
		}
		for _, selection := range []string{"", "unknown-case-port3", "9223372036854775808", "999999", typeSlug, fmt.Sprint(typ)} {
			w := webCasePort3FetchForm(t, mux, actor, "/saved-searches/create/", url.Values{"activity_type": {selection}, "city": {"Must not mint source city"}, "next": {"https://evil.invalid/steal"}, "user_id": {fmt.Sprint(other.ID)}, "cohort": {"child"}, "latitude": {"46.77"}})
			if w.Code != 302 || w.Header().Get("Location") != "/saved-searches/" {
				t.Fatal("source fetch refusal redirect", selection, w.Code, w.Header().Get("Location"))
			}
		}
		if _, err := s.DB.Exec(ctx, `UPDATE taxonomy_activitytype SET is_active=true WHERE id=$1`, typ); err != nil {
			t.Fatal(err)
		}
		var count int
		var minted bool
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM saved_searches_savedsearch WHERE user_id=$1`, actor.ID).Scan(&count); err != nil || count != 2 {
			t.Fatal("invalid selection created saved search", err, count)
		}
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM communities_area WHERE city='Must not mint source city')`).Scan(&minted); err != nil || minted {
			t.Fatal("invalid selection adopted city", err, minted)
		}
	})
	t.Run("stranger_delete_404_preserves_foreign_row", func(t *testing.T) {
		w := webCasePort2Post(t, mux, actor, "/saved-searches/", fmt.Sprintf("/saved-searches/%d/delete/", foreign), url.Values{})
		var remains bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saved_searches_savedsearch WHERE id=$1 AND user_id=$2)`, foreign, other.ID).Scan(&remains); err != nil || !remains || w.Code != 404 {
			t.Fatal("saved search deletion crossed owner wall", err, remains, w.Code)
		}
	})
	t.Run("current_identity_and_cohort_refusal_with_stale_actor", func(t *testing.T) {
		for _, change := range []string{"is_identity_verified=false", "cohort='unassigned'", "cohort='teen',age_band='16_17'"} {
			if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET `+change+` WHERE id=$1`, actor.ID); err != nil {
				t.Fatal(err)
			}
			page := webCasePortRead(mux, actor, "/saved-searches/")
			if page.Code != 302 || page.Header().Get("Location") != "/" {
				t.Fatal("current saved-search page eligibility", change, page.Code)
			}
			w := webCasePort2Post(t, mux, actor, "/profile/", "/saved-searches/create/", url.Values{"activity_type": {typeSlug}, "city": {"Withdrawn authority city"}})
			if w.Code != 302 {
				t.Fatal("source eligibility refusal redirect", change, w.Code)
			}
			if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=true,cohort='adult',age_band='adult' WHERE id=$1`, actor.ID); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		var minted bool
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM saved_searches_savedsearch WHERE user_id=$1`, actor.ID).Scan(&count); err != nil || count != 2 {
			t.Fatal("withdrawn authority created search", err, count)
		}
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM communities_area WHERE city='Withdrawn authority city')`).Scan(&minted); err != nil || minted {
			t.Fatal("withdrawn authority adopted city", err, minted)
		}
	})
	t.Run("identity_withdrawal_keeps_owner_delete_available", func(t *testing.T) {
		if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, actor.ID); err != nil {
			t.Fatal(err)
		}
		w := webCasePort2Post(t, mux, actor, "/profile/", fmt.Sprintf("/saved-searches/%d/delete/", owned), url.Values{"next": {"/profile/"}})
		var remains bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saved_searches_savedsearch WHERE id=$1)`, owned).Scan(&remains); err != nil || remains || w.Code != 302 || w.Header().Get("Location") != "/profile/" {
			t.Fatal("source own saved-search cleanup", err, remains, w.Code, w.Header().Get("Location"))
		}
	})
}
