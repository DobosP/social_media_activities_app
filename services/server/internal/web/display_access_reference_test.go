package web

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Original Pongo form/load/DOM predicates only. These aliases never enter
// production routing or substitute for new ordinary App/native UI evidence.
func displayAccessFrozenAlias(name string) string {
	switch name {
	case "web/display_preferences.html":
		return "web/__display_preferences_frozen_reference__.html"
	case "web/access_preferences.html":
		return "web/__access_preferences_frozen_reference__.html"
	}
	return ""
}
func displayAccessFrozenOriginal(name string) string {
	switch name {
	case "web/__display_preferences_frozen_reference__.html":
		return "web/display_preferences.html"
	case "web/__access_preferences_frozen_reference__.html":
		return "web/access_preferences.html"
	}
	return name
}

type displayAccessFrozenLoader struct{ delegate pongo2.TemplateLoader }

func (l *displayAccessFrozenLoader) Abs(base, name string) string {
	return l.delegate.Abs(base, displayAccessFrozenOriginal(name))
}
func (l *displayAccessFrozenLoader) Get(name string) (io.Reader, error) {
	return l.delegate.Get(displayAccessFrozenOriginal(name))
}
func displayAccessFrozenReferenceRender(renderer *Renderer, w http.ResponseWriter, request *http.Request, name string, data pongo2.Context) error {
	alias := displayAccessFrozenAlias(name)
	if alias == "" {
		return notificationPreferencesFrozenReferenceRender(renderer, w, request, name, data)
	}
	loader := &displayAccessFrozenLoader{delegate: renderer.loader}
	reference := &Renderer{Root: renderer.Root, CSPEnforce: renderer.CSPEnforce, loader: loader, set: pongo2.NewSet("display-access-frozen-reference-only", loader), catalog: renderer.catalog}
	return reference.Render(w, request, alias, data)
}
func displayAccessFrozenReferenceHandler(s *Server, next http.Handler) http.Handler {
	previous := notificationPreferencesFrozenReferenceHandler(s, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			for _, name := range []string{"display_preferences", "access_preferences"} {
				if r.URL.Path == routeURL(name) {
					displayAccessFrozenReferencePage(s, w, r, name)
					return
				}
			}
		}
		previous.ServeHTTP(w, r)
	})
}

// Exact current Server.page, changing only receiver signature and final
// render call. Both original reference and current native route keep the
// real view/auth/services/GET enrichment. Original frozen loads stay named.
func displayAccessFrozenReferencePage(s *Server, w http.ResponseWriter, r *http.Request, name string) {
	actor, logged := platform.ActorFrom(r)
	if !publicPages[name] && !logged {
		http.Redirect(w, r, "/login/?next="+url.QueryEscape(r.URL.RequestURI()), 302)
		return
	}
	if name == "groups" {
		http.Redirect(w, r, "/communities/", 302)
		return
	}
	if s.PublicDownload(w, r, actor, name) {
		return
	}
	if s.SocialDownload(w, r, actor, name) {
		return
	}
	if s.AccountDownload(w, r, actor, name) {
		return
	}
	data, template, err := s.view(r, actor, name)
	if err != nil {
		failPage(w, err)
		return
	}
	if data == nil {
		data = pongo2.Context{}
	}
	if target, ok := data["redirect"].(string); ok && strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "//") {
		http.Redirect(w, r, target, 302)
		return
	}
	if logged {
		if notice := s.consumePostNotice(w, r, actor); len(notice) > 0 {
			data["messages"] = notice
		}
	}
	data["csrf"] = ""
	if !(s.Config.SPA && r.URL.Query().Get("_data") == "1" && publicPages[name] && name != "home") {
		data["csrf"] = s.Auth.EnsureCSRF(w, r)
	}
	if canonical, _ := data["canonical_url"].(string); canonical == "" {
		data["canonical_url"] = strings.TrimRight(s.Config.PublicURL, "/") + r.URL.Path
	}
	data["site_name"] = s.Config.SiteName
	if data["site_name"] == "" {
		data["site_name"] = "Activities"
	}
	data["google_site_verification"] = s.Config.SiteGoogleVerification
	data["bing_site_verification"] = s.Config.SiteBingVerification
	data["activity_svg"] = activitySVG
	if s.Config.SPA && !(name == "home" && !logged) {
		data["csrf_token"] = data["csrf"]
		payload, title, public, seo, buildErr := s.BuildSPA(r.Context(), r, actor, name, data)
		if buildErr == nil {
			if r.URL.Query().Get("_data") == "1" {
				if public {
					w.Header().Set("Cache-Control", "private, max-age=3600")
				}
				platform.JSON(w, 200, payload)
				return
			}
			snapshot := seo["snapshot_template"]
			data["spa_snapshot_template"] = snapshot
			data["spa_title"] = title
			data["spa_route"] = payload["route"]
			data["spa_bootstrap"] = payload
			data["spa_meta_description"] = seo["description"]
			data["spa_meta_robots"] = seo["robots"]
			data["spa_structured_data"] = seo["structured_data"]
			data["spa_rss"] = seo["rss"]
			template = "web/spa.html"
		} else if !errors.Is(buildErr, platform.ErrNotFound) {
			platform.Fail(w, buildErr)
			return
		}
	}
	if logged {
		avatar, _ := data["avatar_uri"].(string)
		if avatar == "" {
			var err error
			avatar, err = accounts.Avatar(r.Context(), s.DB, actor.ID)
			if err != nil {
				platform.Fail(w, err)
				return
			}
		}
		data["avatar_uri"] = avatar
		if user, ok := data["user"].(map[string]any); ok && user["id"] == actor.ID && user["public_id"] == actor.PublicID {
			user["avatar_uri"] = avatar
		}
	}
	if err = displayAccessFrozenReferenceRender(s.Renderer, w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}

func TestNativeDisplayAccessOriginalReferenceIsSeparateFromCurrent(t *testing.T) {
	for _, display := range []bool{false, true} {
		renderer, request, name, data := displayAccessFixture(t, display, true, false)
		current := httptest.NewRecorder()
		if err := renderer.Render(current, request, name, data); err != nil {
			t.Fatal("current native preference unavailable")
		}
		old := httptest.NewRecorder()
		if err := displayAccessFrozenReferenceRender(renderer, old, request, name, data); err != nil {
			t.Fatal("retained original preference unavailable")
		}
		if !strings.Contains(current.Body.String(), "display-access-native") || strings.Contains(old.Body.String(), "display-access-native") {
			t.Fatal("reference was substituted for current native markup")
		}
		if current.Header().Get("Cache-Control") != old.Header().Get("Cache-Control") || current.Header().Get("Vary") != old.Header().Get("Vary") {
			t.Fatal("reference lost common response semantics")
		}
		// Original Pongo accepts a bool map fixture; the new current typed API
		// snapshot deliberately requires the actual decoded string/any map.
		if !display {
			data["pref"] = map[string]bool{"needs_step_free": false, "needs_accessible_toilet": false, "needs_hearing_loop": false, "prefers_quiet": false}
			if raw, err := renderer.renderDisplayAccess(request, name, data); err == nil || len(raw) != 0 {
				t.Fatal("current production template gained reference fallback")
			}
			if err := displayAccessFrozenReferenceRender(renderer, httptest.NewRecorder(), request, name, data); err != nil {
				t.Fatal("original bool-map fixture was discarded")
			}
		}
	}
}
