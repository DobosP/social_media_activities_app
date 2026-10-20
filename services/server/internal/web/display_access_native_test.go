package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func displayAccessFixture(t *testing.T, display, signed, actionError bool) (*Renderer, *http.Request, string, pongo2.Context) {
	t.Helper()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal("preference fixture root unavailable")
	}
	name, path := "web/access_preferences.html", "/access/"
	if display {
		name, path = "web/display_preferences.html", "/display/"
	}
	method := http.MethodGet
	if actionError {
		method = http.MethodPost
	}
	request := httptest.NewRequest(method, "https://preferences.example"+path, nil)
	a := platform.Actor{ID: 917, PublicID: "55555555-5555-4555-8555-555555555555", Username: "generated-preferences", DisplayName: "Generated <name> & person", Cohort: "adult", AgeBand: "adult", IsActive: true}
	user := map[string]any{"is_authenticated": false}
	if signed {
		request = platform.WithActor(request, a)
		// Exact Renderer-default display shell has no AccountView id/pk,
		// is_active or guardian fields. Do not invent them for the fixture.
		user = map[string]any{"is_authenticated": true, "public_id": a.PublicID, "username": a.Username, "display_name": a.DisplayName}
		if !display {
			user = accountUserMap(a)
			user["is_guardian"] = false
		}
		if !actionError {
			user["avatar_uri"] = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><circle cx="5" cy="5" r="3"/></svg>`))
		}
	}
	data := pongo2.Context{"user": user, "language": "en", "display_theme": "auto", "display_text": "normal", "display_motion": "auto", "site_name": "Generated preference site", "canonical_url": "https://preferences.example" + path, "csrf": "synthetic-display-access-csrf", "request": map[string]any{"path": path, "csp_nonce": "synthetic-display-access-nonce"}, "PRIVATE_extra": "PRIVATE-display-access-unrendered"}
	if !display {
		data["is_guardian"], data["has_guardians"], data["connections_enabled"] = false, false, false
		data["pref"] = map[string]any{"needs_step_free": false, "needs_accessible_toilet": false, "needs_hearing_loop": false, "prefers_quiet": false}
	}
	if actionError {
		data["site_name"], data["canonical_url"] = "Social Activities", path
		data["messages"] = []socialMessage{{"message": "Generated <script>error</script>", "tags": "error"}}
	}
	return NewRenderer(root), request, name, data
}

func displayAccessOwningForm(t *testing.T, body string) string {
	t.Helper()
	start := strings.Index(body, `class="display-access-form"`)
	if start < 0 {
		start = strings.Index(body, `class="card display-access-form"`)
	}
	if start < 0 {
		t.Fatal("native preference form unavailable")
	}
	start = strings.LastIndex(body[:start], "<form ")
	if start < 0 {
		t.Fatal("native preference form opening unavailable")
	}
	end := strings.Index(body[start:], "</form>")
	if end < 0 {
		t.Fatal("native preference form closing unavailable")
	}
	return body[start : start+end+len("</form>")]
}

func TestNativeDisplayDefaultShellCookieChoicesAndPublicFallback(t *testing.T) {
	for _, signed := range []bool{false, true} {
		for _, language := range []string{"en", "ro"} {
			for _, state := range []struct{ theme, text, motion string }{{"auto", "normal", "auto"}, {"light", "large", "full"}, {"dark", "normal", "reduce"}, {"contrast", "larger", "reduce"}} {
				r, request, name, data := displayAccessFixture(t, true, signed, false)
				data["language"], data["display_theme"], data["display_text"], data["display_motion"] = language, state.theme, state.text, state.motion
				before, _ := json.Marshal(data)
				raw, err := r.renderDisplayAccess(request, name, data)
				if err != nil {
					t.Fatal("public/default-shell display projection unavailable")
				}
				after, _ := json.Marshal(data)
				if !bytes.Equal(before, after) {
					t.Fatal("display projection mutated producer map")
				}
				body, form := string(raw), displayAccessOwningForm(t, string(raw))
				if !strings.Contains(body, `<html lang="`+language+`" data-theme="`+state.theme+`" data-text="`+state.text+`" data-motion="`+state.motion+`"`) || strings.Count(form, `type="radio"`) != 10 || strings.Count(form, " checked") != 3 || !strings.Contains(form, `action="/display/"`) {
					t.Fatal("finite display choices or shell differ")
				}
				for key, value := range map[string]string{"display_theme": state.theme, "display_text": state.text, "display_motion": state.motion} {
					if strings.Count(form, `name="`+key+`" value="`+value+`" checked`) != 1 {
						t.Fatal("source selection was not preserved")
					}
				}
				if strings.Contains(body, `aria-label="`+map[string]string{"en": "Account", "ro": "Cont"}[language]+`"`) != signed || strings.Contains(body, `action="/logout/"`) != signed || strings.Contains(body, `href="/login/"`) == signed || strings.Contains(body, `href="/connections/"`) || strings.Contains(body, `href="/guardianship/"`) || strings.Contains(body, "PRIVATE-display-access-unrendered") {
					t.Fatal("default/anonymous display acquired account state")
				}
			}
		}
	}
	// Genuine public SPA _data fallback: an empty CSRF display value still
	// renders. Saving remains subject to the existing action CSRF check.
	r, request, name, data := displayAccessFixture(t, true, false, false)
	request.URL.RawQuery = "_data=1"
	data["csrf"] = ""
	raw, err := r.renderDisplayAccess(request, name, data)
	if err != nil || !strings.Contains(displayAccessOwningForm(t, string(raw)), `name="csrfmiddlewaretoken" value=""`) {
		t.Fatal("existing empty-CSRF public fallback was tightened")
	}
}

func TestNativeAccessFourBooleansAndQuietUnknownCopy(t *testing.T) {
	fields := []string{"needs_step_free", "needs_accessible_toilet", "needs_hearing_loop", "prefers_quiet"}
	for mask := 0; mask < 16; mask++ {
		r, request, name, data := displayAccessFixture(t, false, true, false)
		pref := data["pref"].(map[string]any)
		for bit, field := range fields {
			pref[field] = mask&(1<<bit) != 0
		}
		raw, err := r.renderDisplayAccess(request, name, data)
		if err != nil {
			t.Fatal("self access projection unavailable")
		}
		body, form := string(raw), displayAccessOwningForm(t, string(raw))
		if strings.Count(form, `type="checkbox"`) != 4 || strings.Contains(form, " action=") || strings.Contains(form, `name="user_id"`) || strings.Contains(body, `aria-label="Account"`) {
			t.Fatal("four self fields/effective form target changed")
		}
		for bit, field := range fields {
			want := `name="` + field + `"`
			if mask&(1<<bit) != 0 {
				want += " checked"
			}
			if strings.Count(form, want+">") != 1 {
				t.Fatal("self preference checked state changed")
			}
		}
		if !strings.Contains(body, "places with unknown accessibility are still shown to you.") || !strings.Contains(body, "venue data for this isn") || !strings.Contains(body, "change what you see.") {
			t.Fatal("unknown/quiet source disclosure changed")
		}
	}
}

func TestNativeDisplayAccessGetAndPostErrorContextsRemainDistinct(t *testing.T) {
	for _, actionError := range []bool{false, true} {
		r, request, name, data := displayAccessFixture(t, false, true, actionError)
		data["pref"].(map[string]any)["needs_hearing_loop"] = true
		p, err := r.displayAccessPage(request, name, data)
		if err != nil || len(p.Access) != 4 || !p.Access[2].Checked || p.LanguageNext != "/access/" {
			t.Fatal("persisted error/GET projection differs")
		}
		if actionError {
			if p.Shell.Canonical != "/access/" || p.Shell.SiteName != "Social Activities" || p.Avatar != "" || len(p.Messages) != 1 {
				t.Fatal("direct POST error was enriched as GET")
			}
		} else if p.Shell.Canonical != "https://preferences.example/access/" || p.Shell.SiteName != "Generated preference site" || p.Avatar == "" || len(p.Messages) != 0 {
			t.Fatal("normal GET context lost")
		}
		raw, err := r.renderDisplayAccess(request, name, data)
		if err != nil || strings.Contains(string(raw), "<script>error</script>") || strings.Contains(string(raw), "PRIVATE-display-access-unrendered") {
			t.Fatal("error text/private extra escaped boundary failed")
		}
		if actionError && !strings.Contains(string(raw), "&lt;script&gt;error&lt;/script&gt;") {
			t.Fatal("error feedback disappeared")
		}
	}
}

func TestNativeDisplayAccessMalformedSelfAndSnapshotRefuse(t *testing.T) {
	for _, mutate := range []func(pongo2.Context){
		func(d pongo2.Context) { d["user"].(map[string]any)["id"] = int64(999) },
		func(d pongo2.Context) { d["user"].(map[string]any)["is_active"] = false },
		func(d pongo2.Context) { d["user"].(map[string]any)["is_authenticated"] = false },
		func(d pongo2.Context) { d["pref"].(map[string]any)["needs_step_free"] = "true" },
		func(d pongo2.Context) { d["display_theme"] = "unbounded" },
		func(d pongo2.Context) { d["csrf"] = []string{"not-text"} },
		func(d pongo2.Context) { d["canonical_url"] = "javascript:PRIVATE-link" },
		func(d pongo2.Context) { d["request"].(map[string]any)["path"] = "/settings/" },
		func(d pongo2.Context) { d["user"].(map[string]any)["avatar_uri"] = "https://private.invalid/image" },
		func(d pongo2.Context) { d["messages"] = []string{"not-message-map"} },
	} {
		r, request, name, data := displayAccessFixture(t, false, true, false)
		mutate(data)
		if raw, err := r.renderDisplayAccess(request, name, data); err == nil || len(raw) != 0 {
			t.Fatal("malformed/foreign self map emitted HTML")
		}
	}
	r, request, name, data := displayAccessFixture(t, false, false, false)
	if raw, err := r.renderDisplayAccess(request, name, data); !errors.Is(err, platform.ErrForbidden) || len(raw) != 0 {
		t.Fatal("anonymous access acquired a native self form")
	}
}

func TestNativeDisplayRendererDefaultCookieSanitizationAndHeaders(t *testing.T) {
	r, _, _, _ := displayAccessFixture(t, true, false, false)
	for _, state := range []struct{ cookie, want string }{{"dark", "dark"}, {"tampered", "auto"}} {
		request := httptest.NewRequest(http.MethodGet, "https://preferences.example/display/", nil)
		request.AddCookie(&http.Cookie{Name: "display_theme", Value: state.cookie})
		w := httptest.NewRecorder()
		if err := r.Render(w, request, "web/display_preferences.html", pongo2.Context{"csrf": "synthetic-default-csrf"}); err != nil {
			t.Fatal("real Renderer defaults unavailable")
		}
		if w.Header().Get("Cache-Control") != "private, no-cache" || w.Header().Get("Vary") != "Cookie" || !strings.Contains(w.Header().Get("Content-Security-Policy-Report-Only"), "frame-ancestors 'none'") || !strings.Contains(w.Body.String(), `data-theme="`+state.want+`"`) || strings.Contains(w.Body.String(), `style="--scale`) {
			t.Fatal("common headers/cookie sanitization changed")
		}
	}
}

func TestNativeDisplayAccessConcurrentRequestIsolation(t *testing.T) {
	r, _, _, _ := displayAccessFixture(t, true, false, false)
	var wg sync.WaitGroup
	errors := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		_, request, name, data := displayAccessFixture(t, i%2 == 0, true, i%3 == 0)
		data["language"] = []string{"en", "ro"}[i%2]
		wg.Add(1)
		go func(request *http.Request, name string, data pongo2.Context) {
			defer wg.Done()
			before, _ := json.Marshal(data)
			raw, err := r.renderDisplayAccess(request, name, data)
			after, _ := json.Marshal(data)
			errors <- err == nil && bytes.Equal(before, after) && strings.Contains(string(raw), `<html lang="`+data["language"].(string)+`"`) && !strings.Contains(string(raw), "PRIVATE-display-access-unrendered")
		}(request, name, data)
	}
	wg.Wait()
	close(errors)
	for good := range errors {
		if !good {
			t.Fatal("concurrent render leaked/mutated request state")
		}
	}
}
