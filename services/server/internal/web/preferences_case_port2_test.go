package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func webCasePort2Post(t *testing.T, mux *http.ServeMux, actor platform.Actor, form, target string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	seed := webCasePortRead(mux, actor, form)
	var csrf *http.Cookie
	for _, cookie := range seed.Result().Cookies() {
		if cookie.Name == "csrftoken" {
			csrf = cookie
		}
	}
	if seed.Code != 200 || csrf == nil {
		t.Fatalf("form CSRF %s: status%d", form, seed.Code)
	}
	copy := url.Values{}
	for key, items := range values {
		copy[key] = append([]string{}, items...)
	}
	values = copy
	values.Set("csrfmiddlewaretoken", csrf.Value)
	r := httptest.NewRequest("POST", "https://fixture.local"+target, strings.NewReader(values.Encode()))
	if actor.ID != 0 {
		r = platform.WithActor(r, actor)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://fixture.local")
	r.AddCookie(csrf)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestWebCasePort2DisplayFunctionalCookiesAndRenderedDefaults(t *testing.T) {
	_, _, _, _, mux := webCasePortFixture(t)
	t.Run("public_options", func(t *testing.T) {
		body := webCasePortHTML(t, mux, platform.Actor{}, "/display/")
		webCasePortContains(t, body, "Display settings")
		for _, choice := range []string{"auto", "light", "dark", "contrast", "large", "larger", "reduce", "full"} {
			webCasePortContains(t, body, `value="`+choice+`"`)
		}
	})
	t.Run("validated_post_and_garbage", func(t *testing.T) {
		w := webCasePort2Post(t, mux, platform.Actor{}, "/display/", "/display/", url.Values{"display_theme": {"dark"}, "display_text": {"large"}, "display_motion": {"reduce"}})
		if w.Code != 302 {
			t.Fatal("functional preference redirect", w.Code)
		}
		cookies := map[string]*http.Cookie{}
		for _, cookie := range w.Result().Cookies() {
			cookies[cookie.Name] = cookie
		}
		for name, want := range map[string]string{"display_theme": "dark", "display_text": "large", "display_motion": "reduce"} {
			if cookies[name] == nil || cookies[name].Value != want {
				t.Fatal("validated functional cookie", name)
			}
		}
		if cookies["display_theme"].SameSite != http.SameSiteLaxMode {
			t.Fatal("functional cookie lost SameSite Lax")
		}
		w = webCasePort2Post(t, mux, platform.Actor{}, "/display/", "/display/", url.Values{"display_theme": {"rainbow"}, "display_text": {"huge"}, "display_motion": {"spin"}})
		if w.Code != 302 {
			t.Fatal("garbage functional preference redirect", w.Code)
		}
		for _, cookie := range w.Result().Cookies() {
			if strings.HasPrefix(cookie.Name, "display_") {
				t.Fatal("off-allowlist functional cookie persisted")
			}
		}
	})
	for _, test := range []struct {
		name                string
		cookies             map[string]string
		theme, text, motion string
	}{
		{"defaults", nil, "auto", "normal", "auto"},
		{"selected", map[string]string{"display_theme": "dark", "display_text": "larger", "display_motion": "reduce"}, "dark", "larger", "reduce"},
		{"tampered", map[string]string{"display_theme": "evil", "display_text": "1e9"}, "auto", "normal", "auto"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "https://fixture.local/display/", nil)
			for name, value := range test.cookies {
				r.AddCookie(&http.Cookie{Name: name, Value: value})
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != 200 {
				t.Fatal("display GET", w.Code)
			}
			body := w.Body.String()
			webCasePortContains(t, body, `data-theme="`+test.theme+`"`, `data-text="`+test.text+`"`, `data-motion="`+test.motion+`"`)
			webCasePortAbsent(t, body, `style="--scale`)
		})
	}
}

func TestWebCasePort2SelfPrivacyCategoriesCountsAndGuardianLegibility(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	s.Accounts.Config.GuardianInviteTTL = 7 * 24 * time.Hour
	t.Run("login_wall", func(t *testing.T) {
		w := webCasePortRead(mux, platform.Actor{}, "/my-privacy/")
		if w.Code != 302 || !strings.Contains(w.Header().Get("Location"), "/login") {
			t.Fatal("self privacy login wall", w.Code)
		}
	})
	t.Run("categories_negative_space_age_band_and_retention", func(t *testing.T) {
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'Synthetic source proof','openid4vp','adult',now(),now()+interval '1 year','{}','')`, actor.ID); err != nil {
			t.Fatal(err)
		}
		body := webCasePortHTML(t, mux, actor, "/my-privacy/")
		webCasePortContains(t, body, "/verify-age/", "/interests/", "/access/", "/my-safety-record/", "/my-donations/", "/account/export/", "/account/delete/", "never store your location", "age band", "No public photo feeds", "proven age band", "date of birth", "How long your data is kept", "Guardian invitations", "7 days")
	})
	t.Run("strictly_self_counts_and_param_ignored", func(t *testing.T) {
		if _, err := s.Recommendations.SetInterests(ctx, actor, []string{"basketball", "reading"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(ctx, `INSERT INTO donations_donation(donor_id,amount_cents,currency,recurring,campaign_id,provider,status,external_ref,created_at,completed_at) VALUES($1,500,'EUR',false,NULL,'dev','pending','privacy-case-fixture',now(),NULL)`, actor.ID); err != nil {
			t.Fatal(err)
		}
		other := testdb.Actor(t, s.DB, "privacy-case-other-subject", "adult")
		webCasePortContains(t, webCasePortHTML(t, mux, actor, "/my-privacy/"), "chosen: 2", "Donations on record: 1")
		webCasePortContains(t, webCasePortHTML(t, mux, other, fmt.Sprintf("/my-privacy/?user_id=%d", actor.ID)), "chosen: 0", "Donations on record: 0")
	})
	t.Run("guardian_link_only_for_active_relationship", func(t *testing.T) {
		ward := testdb.Actor(t, s.DB, "privacy-case-child", "child")
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, actor.ID, ward.ID); err != nil {
			t.Fatal(err)
		}
		webCasePortContains(t, webCasePortHTML(t, mux, ward, "/my-privacy/"), "/guardianship/")
		webCasePortAbsent(t, webCasePortHTML(t, mux, actor, "/my-privacy/"), "/guardianship/")
		if _, err := s.DB.Exec(ctx, `UPDATE accounts_guardianrelationship SET status='revoked' WHERE guardian_id=$1 AND ward_id=$2`, actor.ID, ward.ID); err != nil {
			t.Fatal(err)
		}
		webCasePortAbsent(t, webCasePortHTML(t, mux, ward, "/my-privacy/"), "/guardianship/")
	})
}

func TestWebCasePort2NotificationAndAccessPreferenceRoundTrips(t *testing.T) {
	s, actor, place, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	t.Run("mutable_only_checkbox_choices_and_stored_mute", func(t *testing.T) {
		body := webCasePortHTML(t, mux, actor, "/notifications/preferences/")
		webCasePortContains(t, body, `value="event_reminder"`)
		webCasePortAbsent(t, body, `value="moderation"`, `value="system"`)
		w := webCasePort2Post(t, mux, actor, "/notifications/preferences/", "/notifications/preferences/", url.Values{"muted": {"event_reminder"}})
		if w.Code != 302 {
			t.Fatal("notification preference redirect", w.Code)
		}
		var muted []string
		if err := s.DB.QueryRow(ctx, `SELECT muted_kinds FROM notifications_notificationpreference WHERE user_id=$1`, actor.ID).Scan(&muted); err != nil || !reflect.DeepEqual(muted, []string{"event_reminder"}) {
			t.Fatal("own mutable notification preference not persisted", err, muted)
		}
	})
	t.Run("notice_explains_delivery", func(t *testing.T) {
		if _, err := s.DB.Exec(ctx, `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,created_at,read_at) VALUES($1,'join_approved','In!','','/activities/',now(),NULL)`, actor.ID); err != nil {
			t.Fatal(err)
		}
		webCasePortContains(t, webCasePortHTML(t, mux, actor, "/notifications/"), "Why you got this")
	})
	t.Run("own_access_preference_and_matching_venue", func(t *testing.T) {
		webCasePortHTML(t, mux, actor, "/access/")
		w := webCasePort2Post(t, mux, actor, "/access/", "/access/", url.Values{"needs_step_free": {"on"}})
		if w.Code != 302 {
			t.Fatal("access preference redirect", w.Code)
		}
		var chosen bool
		if err := s.DB.QueryRow(ctx, `SELECT needs_step_free FROM places_accesspreference WHERE user_id=$1`, actor.ID).Scan(&chosen); err != nil || !chosen {
			t.Fatal("own declared access need not stored", err)
		}
		if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags='{"wheelchair":"yes"}' WHERE id=$1`, place); err != nil {
			t.Fatal(err)
		}
		webCasePortContains(t, webCasePortHTML(t, mux, actor, fmt.Sprintf("/places/%d/", place)), "Matches your access needs")
	})
	for _, test := range []struct {
		name, tags string
		known      bool
	}{{"recorded_access_fact", `{"wheelchair":"yes"}`, true}, {"unknown_access_fact", `{}`, false}} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := s.DB.Exec(ctx, `UPDATE places_place SET raw_tags=$2::jsonb,opening_hours_raw='',opening_hours='{}' WHERE id=$1`, place, test.tags); err != nil {
				t.Fatal(err)
			}
			body := webCasePortHTML(t, mux, actor, fmt.Sprintf("/places/%d/", place))
			marker := strings.Index(body, "Community facts")
			if marker < 0 {
				t.Fatal("missing honest unknown-fact disclosure")
			}
			webCasePortContains(t, body[marker:], "not recorded")
			webCasePortAbsent(t, body[:marker], "not recorded")
			if test.known {
				webCasePortContains(t, body, "Step-free access")
			} else {
				webCasePortAbsent(t, body[:marker], "fact--true")
			}
		})
	}
}
