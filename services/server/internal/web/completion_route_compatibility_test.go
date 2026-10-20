package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestNativeInboxBookmarkRedirectPreservesAdmissionAndMethods(t *testing.T) {
	// These requests must finish in the registered route/admission boundary.
	// The bare server deliberately has no database, auth service or renderer.
	s := &Server{}
	mux := http.NewServeMux()
	s.Register(mux)
	path := "/inbox/?kind=message"
	actor := platform.Actor{ID: 1, IsActive: true, PublicID: "12345678-1234-1234-1234-123456789abc", Username: "bookmark-user"}
	for _, test := range []struct {
		name   string
		method string
		signed bool
		status int
		place  string
	}{
		{"signed-get", http.MethodGet, true, http.StatusFound, "/notifications/"},
		{"signed-head", http.MethodHead, true, http.StatusFound, "/notifications/"},
		{"anonymous-get", http.MethodGet, false, http.StatusFound, "/login/?next=" + url.QueryEscape(path)},
		{"anonymous-head", http.MethodHead, false, http.StatusFound, "/login/?next=" + url.QueryEscape(path)},
		{"signed-post-refused", http.MethodPost, true, http.StatusMethodNotAllowed, ""},
		{"anonymous-post-refused", http.MethodPost, false, http.StatusMethodNotAllowed, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "https://social.example"+path, nil)
			if test.signed {
				request = platform.WithActor(request, actor)
			}
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if response.Code != test.status || response.Header().Get("Location") != test.place {
				t.Fatalf("inbox bookmark returned %d location %q; want %d location %q", response.Code, response.Header().Get("Location"), test.status, test.place)
			}
			if test.method == http.MethodPost && response.Header().Get("Allow") != "GET" {
				t.Fatalf("inbox methods changed: %q", response.Header().Get("Allow"))
			}
		})
	}
}

func TestNativePublicDownloadAliasesRegisterExactExistingHandlers(t *testing.T) {
	// The three non-database downloads must remain usable without an account,
	// database, auth service or HTML renderer. Sitemap's producer needs the DB,
	// so its real response is covered by the registered PG transport fixture.
	s := &Server{Config: Config{PublicURL: "https://social.example"}}
	mux := http.NewServeMux()
	s.Register(mux)
	for _, path := range []string{"/robots.txt", "/llms.txt", "/indexnow.txt", "/sitemap.xml"} {
		request := httptest.NewRequest(http.MethodGet, "https://social.example"+path, nil)
		_, pattern := mux.Handler(request)
		if pattern != "GET "+path {
			t.Fatalf("public download %s resolved to pattern %q", path, pattern)
		}
		request = httptest.NewRequest(http.MethodGet, "https://social.example"+path+"/extra", nil)
		_, pattern = mux.Handler(request)
		if pattern != "GET /" {
			t.Fatalf("public download %s became a subtree: %q", path, pattern)
		}
	}
	for _, test := range []struct {
		path   string
		status int
		kind   string
		body   string
	}{
		{"/robots.txt", http.StatusOK, "text/plain; charset=utf-8", "Disallow: /activities/"},
		{"/llms.txt", http.StatusOK, "text/markdown; charset=utf-8", "/api/schema/"},
		{"/indexnow.txt", http.StatusNotFound, "", ""},
	} {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://social.example"+test.path, nil))
			if response.Code != test.status {
				t.Fatalf("public download returned %d: %.200s", response.Code, response.Body.String())
			}
			if test.status == http.StatusOK {
				if response.Header().Get("Content-Type") != test.kind || response.Header().Get("Cache-Control") != "public, max-age=3600" {
					t.Fatalf("public download headers changed: %v", response.Header())
				}
				if !strings.Contains(response.Body.String(), test.body) {
					t.Fatalf("existing public producer was not called: %.200s", response.Body.String())
				}
			}
			if len(response.Result().Cookies()) != 0 {
				t.Fatal("public download unexpectedly created an account/CSRF cookie")
			}
		})
	}
}
