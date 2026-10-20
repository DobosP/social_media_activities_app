package web

import (
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// One retained Pongo reference alias, never a production routing alternative.
const topicsFrozenAlias = "web/__topics_frozen_reference__.html"

type topicsFrozenLoader struct{ delegate pongo2.TemplateLoader }

func topicsFrozenOriginal(name string) string {
	if name == topicsFrozenAlias {
		return "web/topic_preferences.html"
	}
	return name
}
func (l *topicsFrozenLoader) Abs(base, name string) string {
	return l.delegate.Abs(base, topicsFrozenOriginal(name))
}
func (l *topicsFrozenLoader) Get(name string) (io.Reader, error) {
	return l.delegate.Get(topicsFrozenOriginal(name))
}
func topicsFrozenReferenceRender(renderer *Renderer, w http.ResponseWriter, request *http.Request, name string, data pongo2.Context) error {
	if name != "web/topic_preferences.html" {
		return displayAccessFrozenReferenceRender(renderer, w, request, name, data)
	}
	loader := &topicsFrozenLoader{delegate: renderer.loader}
	reference := &Renderer{Root: renderer.Root, CSPEnforce: renderer.CSPEnforce, loader: loader, set: pongo2.NewSet("topics-frozen-reference-only", loader), catalog: renderer.catalog}
	return reference.Render(w, request, topicsFrozenAlias, data)
}
func topicsFrozenReferenceHandler(s *Server, next http.Handler) http.Handler {
	previous := displayAccessFrozenReferenceHandler(s, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == routeURL("topic_preferences") {
			topicsFrozenReferencePage(s, w, r, "topic_preferences")
			return
		}
		previous.ServeHTTP(w, r)
	})
}

// Exact current Server.page, substituting only signature and final renderer.
func topicsFrozenReferencePage(s *Server, w http.ResponseWriter, r *http.Request, name string) {
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
	if err = topicsFrozenReferenceRender(s.Renderer, w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}

func TestNativeTopicsOriginalReferenceStaysSeparateFromCurrent(t *testing.T) {
	renderer, request, data := topicsNativeFixture(t)
	delete(data, "request") // Let the current common renderer own its actual nonce.
	current := httptest.NewRecorder()
	if err := renderer.Render(current, request, "web/topic_preferences.html", data); err != nil {
		t.Fatal("current native topics unavailable")
	}
	previous := httptest.NewRecorder()
	if err := topicsFrozenReferenceRender(renderer, previous, request, "web/topic_preferences.html", data); err != nil {
		t.Fatal("original topics reference unavailable")
	}
	if !strings.Contains(current.Body.String(), "topics-native") || strings.Contains(previous.Body.String(), "topics-native") {
		t.Fatal("original reference replaced current native presentation")
	}
	if !strings.Contains(previous.Body.String(), `<form method="post" class="card">`) {
		t.Fatal("original implicit-action form authority unavailable")
	}
	for _, header := range []string{"Content-Type", "Cache-Control", "Vary"} {
		if current.Header().Get(header) != previous.Header().Get(header) {
			t.Fatal("reference changed common response semantics")
		}
	}
}
