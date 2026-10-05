package web

import (
	"bytes"
	"encoding/json"
	"errors"

	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/booking"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/discovery"
	"github.com/DobosP/social_media_activities_app/services/server/internal/donations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/notifications"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Config struct {
	Root, PublicURL                                        string
	IndexNowKey, SnapshotDir                               string
	SiteSameAs                                             []string
	SiteAreaServed, SiteContactEmail                       string
	SiteName, SiteGoogleVerification, SiteBingVerification string
	UploadScratch                                          string
	AccountRetention                                       AccountRetentionConfig
	SPA, CSPEnforce                                        bool
}
type Server struct {
	DB              *pgxpool.Pool
	Auth            *authcore.Service
	Accounts        *accounts.Service
	Social          *social.Service
	Safety          *safety.Service
	Donations       *donations.Service
	Catalog         *catalog.Service
	Discovery       *discovery.Service
	Recommendations *recommendations.Service
	Messaging       *messaging.Service
	Media           *media.Service
	Booking         *booking.Service
	Notifications   *notifications.Service
	API             http.Handler
	Renderer        *Renderer
	Config          Config
}

func NewServer(db *pgxpool.Pool, auth *authcore.Service, accounts *accounts.Service, social *social.Service, safety *safety.Service, api http.Handler, config Config) *Server {
	renderer := NewRenderer(config.Root)
	renderer.CSPEnforce = config.CSPEnforce
	return &Server{DB: db, Auth: auth, Accounts: accounts, Social: social, Safety: safety, API: api, Renderer: renderer, Config: config}
}
func pattern(path string) string {
	if path == "" {
		return "/{$}"
	}
	path = converter.ReplaceAllString(path, "{$1}")
	if strings.HasSuffix(path, "/") {
		path += "{$}"
	}
	return "/" + path
}
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/schema/{$}", s.OpenAPIDocument)
	mux.HandleFunc("GET /api/docs/{$}", s.APIDocumentation)
	// Django's typed int/uuid paths overlap in ways ServeMux's untyped wildcards
	// cannot express. Native API paths keep ServeMux; this typed router owns HTML.
	mux.HandleFunc("GET /", s.legacyHTTP)
	mux.HandleFunc("POST /", s.legacyHTTP)
	// Exact paths only: a subtree would reach the pinned credential handlers on
	// paths the application's admission marker and login intercept never see.
	mux.HandleFunc("POST /login/{$}", func(w http.ResponseWriter, r *http.Request) { s.credentials(w, r, false) })
	mux.HandleFunc("POST /register/{$}", func(w http.ResponseWriter, r *http.Request) { s.credentials(w, r, true) })
	mux.HandleFunc("POST /logout/", func(w http.ResponseWriter, r *http.Request) {
		if s.Auth.CheckCSRF(formCSRF(r)) != nil {
			platform.Error(w, 403, "CSRF verification failed.")
			return
		}
		if s.Auth.RevokeSession(r) != nil {
			platform.Error(w, 503, "Logout unavailable.")
			return
		}
		s.Auth.ClearCookies(w)
		http.Redirect(w, r, "/", 303)
	})
}
func formCSRF(r *http.Request) *http.Request {
	cloned := r.Clone(r.Context())
	cloned.Header = r.Header.Clone()
	if cloned.Header.Get("X-CSRFToken") == "" && cloned.Header.Get("X-CSRF-Token") == "" {
		_ = cloned.ParseForm()
		cloned.Header.Set("X-CSRFToken", cloned.PostForm.Get("csrfmiddlewaretoken"))
	}
	return cloned
}
func (s *Server) credentials(w http.ResponseWriter, r *http.Request, signup bool) {
	if r.ParseForm() != nil {
		platform.Error(w, 400, "Invalid form.")
		return
	}
	input := map[string]any{"username": r.PostForm.Get("username"), "password": r.PostForm.Get("password")}
	if signup {
		input["name"] = r.PostForm.Get("display_name")
	}
	body, _ := json.Marshal(input)
	copy := formCSRF(r)
	copy.Body = io.NopCloser(bytes.NewReader(body))
	copy.ContentLength = int64(len(body))
	copy.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	if signup {
		s.Auth.Signup(response, copy)
	} else {
		s.Auth.Login(response, copy)
	}
	for _, cookie := range response.Header().Values("Set-Cookie") {
		w.Header().Add("Set-Cookie", cookie)
	}
	if response.Code >= 200 && response.Code < 300 {
		http.Redirect(w, r, "/", 303)
		return
	}
	w.WriteHeader(response.Code)
	_, _ = w.Write(response.Body.Bytes())
}
func (s *Server) call(r *http.Request, method, path string, body any) (any, *httptest.ResponseRecorder, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
	}
	target, err := url.Parse(path)
	if err != nil || target.IsAbs() || target.Host != "" || !strings.HasPrefix(target.Path, "/api/") {
		return nil, nil, platform.ErrInvalid
	}
	copy := r.Clone(r.Context())
	copy.URL = target
	copy.Method = method
	copy.RequestURI = target.RequestURI()
	copy.Body = io.NopCloser(bytes.NewReader(raw))
	copy.ContentLength = int64(len(raw))
	copy.Header = r.Header.Clone()
	copy.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	s.API.ServeHTTP(response, copy)
	if response.Code < 200 || response.Code >= 300 {
		switch response.Code {
		case 400, 422:
			return nil, response, platform.ErrInvalid
		case 401, 403:
			return nil, response, platform.ErrForbidden
		case 404:
			return nil, response, platform.ErrNotFound
		}
		return nil, response, errors.New("native operation unavailable")
	}
	var result any
	if response.Code != 204 {
		decoder := json.NewDecoder(response.Body)
		decoder.UseNumber()
		if decoder.Decode(&result) != nil {
			return nil, response, errors.New("native response invalid")
		}
	}
	return result, response, nil
}
func (s *Server) page(w http.ResponseWriter, r *http.Request, name string) {
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
		platform.Fail(w, err)
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
	}
	if err = s.Renderer.Render(w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}
func id(r *http.Request, name string) int64 {
	v, _ := strconv.ParseInt(r.PathValue(name), 10, 64)
	return v
}
func object(value any) map[string]any {
	if item, ok := value.(map[string]any); ok {
		return item
	}
	return map[string]any{}
}
func results(value any) []any {
	if items, ok := value.([]any); ok {
		return items
	}
	if items, ok := object(value)["results"].([]any); ok {
		return items
	}
	return []any{}
}
func date(raw string) string {
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return djangoDate(parsed, "D j M, H:i")
	}
	return raw
}

var publicPages = map[string]bool{"display_preferences": true, "home": true, "places_map": true, "places_list": true, "place_detail": true, "place_detail_slug": true, "events_list": true, "event_detail": true, "event_detail_slug": true, "events_feed": true, "events_feed_atom": true, "things_to_do_index": true, "things_to_do_city": true, "things_to_do": true, "donate": true, "transparency": true, "campaigns": true, "partners": true, "open_data": true, "open_data_snapshot": true, "discover": true, "privacy": true, "terms": true, "service_worker": true}
var actionOnly = map[string]bool{"logout": true}
