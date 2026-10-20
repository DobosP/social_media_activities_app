package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// The real assembled App, credentials, cookies, HTML and settings remain in
// memory. No response/request/body/SQL/identity value is printed or captured.
func displayAccessAppHTML(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") || w.Header().Get("X-Social-Runtime") != "go" || !strings.Contains(w.Header().Get("Content-Security-Policy-Report-Only"), "frame-ancestors 'none'") {
		t.Fatal("registered preference HTML/header contract unavailable", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `class="display-access-native`) || !strings.Contains(body, `name="csrfmiddlewaretoken"`) || strings.Contains(body, `id="spa-bootstrap"`) {
		t.Fatal("registered preference path did not use native rendering")
	}
	return body
}

func displayAccessAnonymous(t *testing.T, a *App) familyAppCredential {
	t.Helper()
	w := accountCenterAppRequest(t, a, familyAppCredential{}, http.MethodGet, "/display/", "", nil, false, nil)
	displayAccessAppHTML(t, w)
	for _, c := range w.Result().Cookies() {
		if c.Name == "csrftoken" {
			return familyAppCredential{CSRF: c}
		}
	}
	t.Fatal("public preference CSRF seed unavailable")
	return familyAppCredential{}
}

func displayAccessChoiceCookie(t *testing.T, w *httptest.ResponseRecorder, name, value string) *http.Cookie {
	t.Helper()
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/display/" {
		t.Fatal("display action did not retain canonical redirect", w.Code)
	}
	var result *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			if result != nil {
				t.Fatal("duplicate preference cookie")
			}
			result = c
		}
	}
	if result == nil || result.Value != value || result.Path != "/" || result.Domain != "" || result.MaxAge != 365*24*60*60 || result.SameSite != http.SameSiteLaxMode || !result.Secure || result.HttpOnly {
		t.Fatal("existing display cookie convention changed")
	}
	return result
}

func displayAccessStored(t *testing.T, f accountCenterAppFixture, id int64) [4]bool {
	t.Helper()
	var out [4]bool
	if f.app.DB.QueryRow(context.Background(), `SELECT COALESCE(p.needs_step_free,false),COALESCE(p.needs_accessible_toilet,false),COALESCE(p.needs_hearing_loop,false),COALESCE(p.prefers_quiet,false) FROM accounts_user u LEFT JOIN places_accesspreference p ON p.user_id=u.id WHERE u.id=$1`, id).Scan(&out[0], &out[1], &out[2], &out[3]) != nil {
		t.Fatal("self preference observation unavailable")
	}
	return out
}

func displayAccessAudit(t *testing.T, f accountCenterAppFixture) int {
	t.Helper()
	var count int
	if f.app.DB.QueryRow(context.Background(), `SELECT count(*) FROM safety_auditlog WHERE event='user.access_preference_updated' AND actor_id=$1`, f.owner.ID).Scan(&count) != nil {
		t.Fatal("preference audit observation unavailable")
	}
	return count
}

func TestNativeDisplayAssembledAnonymousAndSessionCookieRoundtrip(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	anonymous := displayAccessAnonymous(t, f.app)
	before := f.counts(t)
	for _, mode := range []string{"anonymous", "session"} {
		t.Run(mode, func(t *testing.T) {
			credential := anonymous
			if mode == "session" {
				credential = f.ownerCookie
			}
			get := accountCenterAppRequest(t, f.app, credential, http.MethodGet, "/display/", "", nil, false, nil)
			body := displayAccessAppHTML(t, get)
			if strings.Contains(body, `aria-label="Account"`) != (mode == "session") || strings.Contains(body, `action="/logout/"`) != (mode == "session") {
				t.Fatal("public/default account shell changed")
			}
			w := accountCenterAppRequest(t, f.app, credential, http.MethodPost, "/display/", "https://app.example", url.Values{"display_theme": {"contrast"}, "display_text": {"larger"}, "display_motion": {"reduce"}, "next": {"https://foreign.invalid/"}, "user_id": {"ignored"}}, true, nil)
			cookies := []*http.Cookie{displayAccessChoiceCookie(t, w, "display_theme", "contrast"), displayAccessChoiceCookie(t, w, "display_text", "larger"), displayAccessChoiceCookie(t, w, "display_motion", "reduce")}
			get = accountCenterAppRequest(t, f.app, credential, http.MethodGet, "/display/", "", nil, false, func(r *http.Request) {
				for _, c := range cookies {
					r.AddCookie(c)
				}
				r.AddCookie(&http.Cookie{Name: "django_language", Value: "ro"})
			})
			body = displayAccessAppHTML(t, get)
			for _, part := range []string{`<html lang="ro" data-theme="contrast" data-text="larger" data-motion="reduce"`, `name="display_theme" value="contrast" checked`, `name="display_text" value="larger" checked`, `name="display_motion" value="reduce" checked`} {
				if !strings.Contains(body, part) {
					t.Fatal("saved device choice did not survive native GET")
				}
			}
			if f.counts(t) != before {
				t.Fatal("display cookies mutated account/session/token/audit state")
			}
		})
	}
	// Each cookie retains independent allowlisting; missing/invalid values do
	// not clear the old device choice or prevent other valid fields saving.
	w := accountCenterAppRequest(t, f.app, anonymous, http.MethodPost, "/display/", "https://app.example", url.Values{"display_theme": {"dark"}, "display_text": {"huge"}}, true, nil)
	displayAccessChoiceCookie(t, w, "display_theme", "dark")
	for _, c := range w.Result().Cookies() {
		if strings.HasPrefix(c.Name, "display_") && c.Name != "display_theme" {
			t.Fatal("invalid/missing display field changed a cookie")
		}
	}
	w = accountCenterAppRequest(t, f.app, anonymous, http.MethodGet, "/display/", "", nil, false, func(r *http.Request) {
		r.AddCookie(&http.Cookie{Name: "display_theme", Value: "tampered"})
		r.AddCookie(&http.Cookie{Name: "display_text", Value: "unbounded"})
	})
	body := displayAccessAppHTML(t, w)
	if !strings.Contains(body, `data-theme="auto" data-text="normal" data-motion="auto"`) || f.counts(t) != before {
		t.Fatal("display fallback or nonpersistent boundary changed")
	}
}

func TestNativeDisplayAssembledCSRFAndMethodRefusalsDoNotSave(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	anonymous := displayAccessAnonymous(t, f.app)
	before := f.counts(t)
	for _, credential := range []familyAppCredential{anonymous, f.ownerCookie} {
		for _, probe := range []struct {
			origin string
			proof  bool
			header string
		}{{"https://app.example", false, ""}, {"https://foreign.invalid", true, ""}, {"", true, ""}, {"https://app.example", true, "incorrect"}} {
			w := accountCenterAppRequest(t, f.app, credential, http.MethodPost, "/display/", probe.origin, url.Values{"display_theme": {"dark"}}, probe.proof, func(r *http.Request) {
				if probe.header != "" {
					r.Header.Set("X-CSRFToken", probe.header)
				}
			})
			if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 || f.counts(t) != before {
				t.Fatal("public/session display CSRF refusal saved state", w.Code)
			}
		}
	}
	for _, path := range []string{"/display", "/display/extra/"} {
		w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, path, "", nil, false, nil)
		if w.Code != http.StatusNotFound {
			t.Fatal("unregistered display path was admitted", w.Code)
		}
	}
	w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPut, "/display/", "https://app.example", nil, true, func(r *http.Request) { r.Header.Set("X-CSRFToken", f.ownerCookie.CSRF.Value) })
	if w.Code != http.StatusMethodNotAllowed || len(w.Result().Cookies()) != 0 || f.counts(t) != before {
		t.Fatal("display method acquired a preference mutation", w.Code)
	}
}

func TestNativeAccessAssembledSelfBooleansIsolationAndAudit(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	peerBefore := displayAccessStored(t, f, f.peer.ID)
	auditBefore := displayAccessAudit(t, f)
	w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, "/access/", "", nil, false, nil)
	displayAccessAppHTML(t, w)
	if displayAccessAudit(t, f) != auditBefore {
		t.Fatal("access GET wrote an audit")
	}
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/access/", "https://app.example", url.Values{"needs_step_free": {"on"}, "needs_accessible_toilet": {"true"}, "needs_hearing_loop": {"1"}, "prefers_quiet": {"unrecognized"}, "user_id": {strconv.FormatInt(f.peer.ID, 10)}, "next": {"https://foreign.invalid/"}}, true, nil)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/access/" || displayAccessStored(t, f, f.owner.ID) != [4]bool{true, true, true, false} || displayAccessStored(t, f, f.peer.ID) != peerBefore || displayAccessAudit(t, f) != auditBefore+1 {
		t.Fatal("self access save/boolean aliases/audit changed", w.Code)
	}
	var target string
	var noPreferenceDetails bool
	if f.app.DB.QueryRow(context.Background(), `SELECT target_ref,data='{}'::jsonb FROM safety_auditlog WHERE event='user.access_preference_updated' AND actor_id=$1 ORDER BY id DESC LIMIT 1`, f.owner.ID).Scan(&target, &noPreferenceDetails) != nil || target != "accounts.user:"+strconv.FormatInt(f.owner.ID, 10) || !noPreferenceDetails {
		t.Fatal("access audit disclosed values or targeted a foreign account")
	}
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/access/", "https://app.example", url.Values{"prefers_quiet": {"on"}}, true, nil)
	if w.Code != http.StatusFound || displayAccessStored(t, f, f.owner.ID) != [4]bool{false, false, false, true} || displayAccessStored(t, f, f.peer.ID) != peerBefore || displayAccessAudit(t, f) != auditBefore+2 {
		t.Fatal("unchecked fields did not remain explicit full replacement")
	}
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, "/access/", "", nil, false, nil)
	body := displayAccessAppHTML(t, w)
	if !strings.Contains(body, `name="prefers_quiet" checked`) || strings.Contains(body, `name="needs_step_free" checked`) || !strings.Contains(body, "places with unknown accessibility are still shown to you.") || !strings.Contains(body, "change what you see.") {
		t.Fatal("persisted self values/unknown-data copy changed")
	}
}

func TestNativeAccessAssembledRefusalAndPersistedErrorContext(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	anonymous := displayAccessAnonymous(t, f.app)
	before, auditBefore := f.counts(t), displayAccessAudit(t, f)
	for _, probe := range []struct {
		origin string
		proof  bool
	}{{"https://app.example", false}, {"https://foreign.invalid", true}} {
		w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/access/", probe.origin, url.Values{"needs_step_free": {"on"}}, probe.proof, nil)
		if w.Code != http.StatusForbidden || f.counts(t) != before || displayAccessAudit(t, f) != auditBefore || displayAccessStored(t, f, f.owner.ID) != [4]bool{} {
			t.Fatal("refused access form mutated self state", w.Code)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		w := accountCenterAppRequest(t, f.app, anonymous, method, "/access/", "https://app.example", nil, true, nil)
		location := "/login/"
		if method == http.MethodGet {
			location = "/login/?next=%2Faccess%2F"
		}
		if w.Code != http.StatusFound || w.Header().Get("Location") != location {
			t.Fatal("anonymous access acquired self form", w.Code)
		}
	}
	w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/access/", "https://app.example", url.Values{"needs_hearing_loop": {"on"}}, true, nil)
	if w.Code != http.StatusFound {
		t.Fatal("error-path persisted fixture unavailable")
	}
	persisted, stable := displayAccessStored(t, f, f.owner.ID), f.counts(t)
	get := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, "/access/", "", nil, false, nil)
	if !strings.Contains(displayAccessAppHTML(t, get), "Generated account site") {
		t.Fatal("normal preference GET lacks configured site")
	}
	originalAPI := f.app.Web.API
	// Fault only the internal PUT response. The actual registered browser
	// action and real GET producer/render still execute; no HTML is supplied
	// by a callback and no source/schema/table is renamed for fallback.
	f.app.Web.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && r.URL.Path == "/api/accounts/me/settings/" {
			platform.Fail(w, platform.ErrInvalid)
			return
		}
		originalAPI.ServeHTTP(w, r)
	})
	t.Cleanup(func() { f.app.Web.API = originalAPI })
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/access/", "https://app.example", url.Values{"needs_step_free": {"on"}}, true, nil)
	body := displayAccessAppHTML(t, w)
	if !strings.Contains(body, `class="msg error"`) || !strings.Contains(body, `<link rel="canonical" href="/access/">`) || strings.Contains(body, "Generated account site") || strings.Contains(body, `<img class="avatar`) || !strings.Contains(body, `name="needs_hearing_loop" checked`) || strings.Contains(body, `name="needs_step_free" checked`) || displayAccessStored(t, f, f.owner.ID) != persisted || f.counts(t) != stable {
		t.Fatal("POST error was replaced with posted/GET-enriched state")
	}
	// A failing re-fetch retains the existing domain-error response; there
	// is no synthetic form fallback or persistence retry in the new view.
	f.app.Web.API = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/accounts/me/settings/" {
			platform.Fail(w, platform.ErrNotFound)
			return
		}
		originalAPI.ServeHTTP(w, r)
	})
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, "/access/", "", nil, false, nil)
	if w.Code != http.StatusNotFound || strings.Contains(w.Body.String(), "display-access-native") || f.counts(t) != stable {
		t.Fatal("failed settings GET manufactured native form", w.Code)
	}
}

func TestNativeDisplayAccessAssembledSPAAndHeaderPaths(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	f.app.Web.Config.SPA = true
	for _, credential := range []familyAppCredential{displayAccessAnonymous(t, f.app), f.ownerCookie} {
		w := accountCenterAppRequest(t, f.app, credential, http.MethodGet, "/display/?_data=1", "", nil, false, nil)
		body := displayAccessAppHTML(t, w)
		if !strings.Contains(body, `name="csrfmiddlewaretoken" value=""`) {
			t.Fatal("public SPA display fallback issued/required different form proof")
		}
	}
	w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, "/access/?_data=1", "", nil, false, nil)
	var payload map[string]any
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &payload) != nil || payload["route"] != "access" || strings.Contains(w.Body.String(), "display-access-native") {
		t.Fatal("current access SPA data contract changed", w.Code)
	}
	f.app.Web.Config.SPA = false
	for _, path := range []string{"/display/", "/access/"} {
		w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodHead, path, "", nil, false, nil)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") {
			t.Fatal("registered preference HEAD/header behavior changed", w.Code)
		}
	}
}
