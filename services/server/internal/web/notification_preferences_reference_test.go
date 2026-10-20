package web

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// This file serves retained original layout/form-byte predicates ONLY. It is
// not evidence that the rebuilt page has old markup or current route coverage.
// Production has no bypass/fallback: its only notification view requires22rows.
const notificationPreferencesFrozenAlias = "web/__notification_preferences_frozen_reference__.html"

type notificationPreferencesFrozenLoader struct{ delegate pongo2.TemplateLoader }

func (l *notificationPreferencesFrozenLoader) Abs(base, name string) string {
	if name == notificationPreferencesFrozenAlias {
		name = "web/notification_preferences.html"
	}
	return l.delegate.Abs(base, name)
}
func (l *notificationPreferencesFrozenLoader) Get(name string) (io.Reader, error) {
	if name == notificationPreferencesFrozenAlias {
		name = "web/notification_preferences.html"
	}
	return l.delegate.Get(name)
}
func notificationPreferencesFrozenReferenceRender(renderer *Renderer, w http.ResponseWriter, request *http.Request, name string, data pongo2.Context) error {
	if name != "web/notification_preferences.html" {
		return renderer.Render(w, request, name, data)
	}
	loader := &notificationPreferencesFrozenLoader{delegate: renderer.loader}
	reference := &Renderer{Root: renderer.Root, CSPEnforce: renderer.CSPEnforce, loader: loader, set: pongo2.NewSet("notification-frozen-reference-only", loader), catalog: renderer.catalog}
	// The alias selects the existing Pongo path, with exactly the current
	// Renderer common nonce/header/default/filter behavior and unchanged files.
	return reference.Render(w, request, notificationPreferencesFrozenAlias, data)
}
func notificationPreferencesFrozenReferenceHandler(s *Server, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == routeURL("notification_preferences") {
			notificationPreferencesFrozenReferencePage(s, w, r, "notification_preferences")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Exact source99b88a4 Server.page body, changing only its receiver signature
// and final render call. It is entered only by the retained notification GET
// reference adapter; no production/auth/route/view/service implementation changes.
func notificationPreferencesFrozenReferencePage(s *Server, w http.ResponseWriter, r *http.Request, name string) {
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
	if err = notificationPreferencesFrozenReferenceRender(s.Renderer, w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}

func TestNativeNotificationPreferencesFrozenReferenceIsSeparateFromStrictNative(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	renderer := NewRenderer(root)
	for _, count := range []int{0, 1, 2} {
		request, data := notificationPreferencesFixture("none-en")
		delete(data, "request")
		data["rows"] = data["rows"].([]map[string]any)[:count]
		native := httptest.NewRecorder()
		if err := renderer.Render(native, request, "web/notification_preferences.html", data); err == nil || native.Body.Len() != 0 {
			t.Fatal("production dispatcher acquired a short-row fallback")
		}
		reference := httptest.NewRecorder()
		delete(data, "PRIVATE-foreign-preferences")
		if err := notificationPreferencesFrozenReferenceRender(renderer, reference, request, "web/notification_preferences.html", data); err != nil {
			t.Fatal("retained frozen-template fixture refused", err)
		}
		if !strings.Contains(reference.Body.String(), `<form method="post" class="card">`) || strings.Count(reference.Body.String(), `type="checkbox" name="muted"`) != count || strings.Contains(reference.Body.String(), "notification-preferences-page") {
			t.Fatal("reference was silently replaced by rebuilt markup")
		}
	}
	request, data := groupCreateProjectionFixture(false)
	a, b := httptest.NewRecorder(), httptest.NewRecorder()
	if renderer.Render(a, request, "web/group_form.html", data) != nil || notificationPreferencesFrozenReferenceRender(renderer, b, request, "web/group_form.html", data) != nil || !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) || !strings.Contains(b.Body.String(), "group-create-page") {
		t.Fatal("non-notification route stopped using its actual native renderer")
	}
	raw, err := os.ReadFile(filepath.Join(root, "services/server/internal/web/server.go"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err = completionSourcePreimage("services/server/internal/web/server.go", raw)
	if err != nil {
		t.Fatal("exact current completion source required before historical page proof")
	}
	start := bytes.Index(raw, []byte("func (s *Server) page("))
	if start < 0 {
		t.Fatal("source page boundary changed")
	}
	end := bytes.Index(raw[start:], []byte("\nfunc id("))
	if end < 0 {
		t.Fatal("source page boundary changed")
	}
	if guiPublicHash(raw[start:start+end]) != "547bc5c6506ce56ea98ccbe6efd5a5988eb4f640cd7087dd91a4e5d1c9b87498" {
		t.Fatal("frozen reference page source changed; explicit new bridge review required")
	}
	helper, err := os.ReadFile(filepath.Join(root, "services/server/internal/web/notification_preferences_reference_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	first := bytes.Index(helper, []byte("func notificationPreferencesFrozenReferencePage("))
	if first < 0 {
		t.Fatal("frozen reference page boundary missing")
	}
	last := bytes.Index(helper[first:], []byte("\nfunc TestNativeNotificationPreferencesFrozenReference"))
	if last < 0 {
		t.Fatal("frozen reference page end missing")
	}
	recovered := bytes.TrimSpace(helper[first : first+last])
	recovered = bytes.Replace(recovered, []byte("func notificationPreferencesFrozenReferencePage(s *Server, w http.ResponseWriter, r *http.Request, name string) {"), []byte("func (s *Server) page(w http.ResponseWriter, r *http.Request, name string) {"), 1)
	recovered = bytes.Replace(recovered, []byte("notificationPreferencesFrozenReferenceRender(s.Renderer, w, r, template, data)"), []byte("s.Renderer.Render(w, r, template, data)"), 1)
	if !bytes.Equal(recovered, bytes.TrimSpace(raw[start:start+end])) {
		t.Fatal("reference page differs beyond its signature and renderer adapter")
	}

}
