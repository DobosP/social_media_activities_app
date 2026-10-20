package web

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/flosch/pongo2/v6"
)

// Retained source/nonce/capture predicates execute their original Pongo pages.
// These test-only aliases never enter the registered production renderer. The
// ordinary new view tests and current App/browser tests exercise the Go pages.
//
//go:embed testdata/native-completion/base-before.html
var completionBaseBefore []byte

var completionReferenceNames = map[string]bool{
	"web/my_meetups.html": true, "web/my_venues.html": true,
	"web/donate.html": true, "web/transparency.html": true, "web/campaigns.html": true, "web/partners.html": true, "web/my_donations.html": true,
	"web/series_list.html": true, "web/series_detail.html": true, "web/series_form.html": true, "web/gauges.html": true, "web/gauge_detail.html": true, "web/gauge_form.html": true, "web/gauge_convert.html": true,
	"web/person.html": true, "web/_person_card.html": true, "web/activity_log.html": true, "web/safety_record.html": true, "web/my_privacy.html": true, "web/verify_age.html": true, "web/account_delete.html": true, "web/report.html": true, "web/privacy.html": true, "web/terms.html": true,
	"web/landing.html": true, "web/communities_graph.html": true, "web/discover.html": true, "web/places.html": true, "web/place_detail.html": true, "web/event_detail.html": true, "web/open_data.html": true,
	"web/activity_form.html": true, "web/activity_edit.html": true, "web/activity_detail.html": true, "web/activity_safe_exit.html": true, "web/place_propose.html": true, "web/place_claim.html": true, "web/places_pending.html": true, "web/messages.html": true,
}

func completionReferenceAlias(name string) string {
	if completionReferenceNames[name] {
		return "web/__completion_reference_" + strings.TrimPrefix(name, "web/")
	}
	return ""
}
func completionReferenceOriginal(name string) string {
	if strings.HasPrefix(name, "web/__completion_reference_") {
		original := "web/" + strings.TrimPrefix(name, "web/__completion_reference_")
		if completionReferenceNames[original] {
			return original
		}
	}
	return name
}

type completionReferenceLoader struct{ delegate pongo2.TemplateLoader }

func (l *completionReferenceLoader) Abs(base, name string) string {
	return l.delegate.Abs(completionReferenceOriginal(base), completionReferenceOriginal(name))
}
func (l *completionReferenceLoader) Get(name string) (io.Reader, error) {
	name = completionReferenceOriginal(name)
	reader, err := l.delegate.Get(name)
	if err != nil || name != "base.html" {
		return reader, err
	}
	raw, err := io.ReadAll(io.LimitReader(reader, (512<<10)+1))
	if err != nil || len(raw) > 512<<10 {
		return nil, fmt.Errorf("completion-reference-base-read")
	}
	// Only the reviewed main addition is inverted. Exact old OS/FS source
	// fixtures and intentional mutation fixtures keep their own provided bytes.
	anchor := []byte(`<main class="container" id="main" role="main" tabindex="-1">`)
	if bytes.Count(completionBaseBefore, anchor) != 1 {
		return nil, fmt.Errorf("completion-reference-base-anchor")
	}
	addition := []byte(`{% if local_poc_mode or local_poc_content %}` + localPOCNotice + `{% endif %}`)
	current := bytes.Replace(completionBaseBefore, anchor, append(append([]byte(nil), anchor...), addition...), 1)
	if bytes.Equal(raw, []byte(transformTemplate(string(current)))) {
		return strings.NewReader(transformTemplate(string(completionBaseBefore))), nil
	}
	return bytes.NewReader(raw), nil
}

func completionFrozenReferenceRender(renderer *Renderer, w http.ResponseWriter, request *http.Request, name string, data pongo2.Context) error {
	alias := completionReferenceAlias(name)
	if alias == "" {
		return topicsFrozenReferenceRender(renderer, w, request, name, data)
	}
	loader := &completionReferenceLoader{delegate: renderer.loader}
	reference := &Renderer{Root: renderer.Root, CSPEnforce: renderer.CSPEnforce, loader: loader, set: pongo2.NewSet("completion-frozen-reference-only", loader), catalog: renderer.catalog}
	return reference.Render(w, request, alias, data)
}

func completionFrozenReferenceHandler(s *Server, next http.Handler) http.Handler {
	previous := topicsFrozenReferenceHandler(s, next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			prefix := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")[0]
			for _, route := range legacyRouteBuckets[prefix] {
				matches := route.Pattern.FindStringSubmatch(r.URL.Path)
				if matches == nil {
					continue
				}
				// The same first-match typed router and GET admission as legacyHTTP.
				if !(hasMethod(route.Name, "GET") || len(legacyMethods[route.Name]) == 0 && !actionOnly[route.Name]) {
					break
				}
				if completionReferenceRoute(route.Name) {
					cloned := r.Clone(r.Context())
					for i, key := range route.Keys {
						cloned.SetPathValue(key, matches[i+1])
					}
					completionFrozenReferencePage(s, w, cloned, route.Name)
					return
				}
				break
			}
		}
		previous.ServeHTTP(w, r)
	})
}
func completionReferenceRoute(name string) bool {
	switch name {
	case "home", "community_graph", "places_map", "place_detail", "place_detail_slug", "event_detail", "event_detail_slug", "discover", "open_data":
		return true
	case "person_card":
		return true
	case "activity_create":
		return true
	case "series_create", "gauge_create":
		return true
	}
	return completionReferenceNames["web/"+name+".html"]
}

func TestNativeCompletionReferenceRetainsPongoAndCurrentNativeSeparately(t *testing.T) {
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	r := NewRenderer(root)
	request := httptest.NewRequest(http.MethodGet, "https://reference.example/privacy/", nil)
	data := pongo2.Context{"csrf": "synthetic-reference-csrf"}
	current, reference := httptest.NewRecorder(), httptest.NewRecorder()
	if r.Render(current, request, "web/privacy.html", data) != nil || completionFrozenReferenceRender(r, reference, request, "web/privacy.html", data) != nil {
		t.Fatal("current/reference renderer unavailable")
	}
	if !strings.Contains(current.Body.String(), "trust-native") || strings.Contains(reference.Body.String(), "trust-native") {
		t.Fatal("reference substituted for current native implementation")
	}
	for _, header := range []string{"Content-Type", "Cache-Control", "Vary"} {
		if current.Header().Get(header) != reference.Header().Get(header) {
			t.Fatal("reference changed common transport headers")
		}
	}
	physical, err := os.ReadFile(filepath.Join(root, "templates/base.html"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := completionSourcePreimage("templates/base.html", physical)
	if err != nil || !bytes.Equal(before, completionBaseBefore) {
		t.Fatal("reference lost exact original a267 base source")
	}
	if len(completionReferenceNames) != 39 {
		t.Fatal("finite selected reference set changed")
	}
}

// Exact current Server.page, changing only the signature and final renderer.
func completionFrozenReferencePage(s *Server, w http.ResponseWriter, r *http.Request, name string) {
	actor, logged := platform.ActorFrom(r)
	if !publicPages[name] && !logged {
		http.Redirect(w, r, "/login/?next="+url.QueryEscape(r.URL.RequestURI()), 302)
		return
	}
	if name == "inbox" {
		http.Redirect(w, r, "/notifications/", 302)
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
	data["local_poc_mode"] = s.Config.LocalPOC
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
	if err = completionFrozenReferenceRender(s.Renderer, w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}
