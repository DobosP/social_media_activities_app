package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
)

func languageTestServer(t *testing.T, origin string) (*Server, *http.ServeMux) {
	t.Helper()
	// CheckCSRF never uses the identity store. This nil-DB store cannot perform
	// identity queries; the actual pinned same-origin/double-submit check runs.
	auth, err := authcore.New(authcore.Config{PublicURL: origin}, accounts.NewStore(nil))
	if err != nil {
		t.Fatal("synthetic CSRF service unavailable")
	}
	s := &Server{Auth: auth, Config: Config{PublicURL: origin}}
	mux := http.NewServeMux()
	s.Register(mux)
	return s, mux
}

func languageTestRequest(method, target, origin, body string, proof bool) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if proof {
		r.AddCookie(&http.Cookie{Name: "csrftoken", Value: strings.Repeat("c", 52)})
	}
	return r
}

func TestNativeLanguageReturnUsesKnownGETAndExplicitActionTarget(t *testing.T) {
	for _, row := range []struct{ input, want string }{
		{"/", "/"}, {"/you/", "/you/"}, {"/settings/", "/settings/"}, {"/activities/?city=Cluj#results", "/activities/?city=Cluj#results"},
		{"/settings/api-token/revoke/", "/settings/"}, {"/settings/api-token/revoke/?from=error#context", "/settings/"},
		{"/open-data/", "/open-data/"}, {"/open-data/?catalog=1#licensing", "/open-data/?catalog=1#licensing"},
		{"/account/export/?x=1#download", "/"}, {"/account/calendar.ics", "/"}, {"/account/calendar.ics?x=1#download", "/"}, {"/open-data/snapshot/events.json", "/"}, {"/open-data/snapshot/events.json?x=1#download", "/"},
		{"/sw.js", "/"}, {"/sw.js?x=1#worker", "/"}, {"/events/feed/", "/"}, {"/events/feed/?x=1#feed", "/"}, {"/events/feed/atom/", "/"}, {"/events/feed/atom/?x=1#feed", "/"},
		{"/settings/?_data=1", "/"},
		{"", "/"}, {"unknown", "/"}, {"https://foreign.invalid/", "/"}, {"//foreign.invalid/", "/"}, {"///foreign.invalid/", "/"},
		{"/\\foreign.invalid", "/"}, {"/%5Cforeign.invalid", "/"}, {"/%2Fforeign.invalid", "/"}, {"/settings/\r\nLocation:https://foreign.invalid", "/"},
		{"/settings/?x=%0d%0a", "/"}, {"/settings/#%00", "/"}, {"/settings/?x=%5c", "/"}, {"/settings/?bad=%zz", "/"},
		{"/logout/", "/"}, {"/i18n/setlang/", "/"}, {"/account/export/", "/"}, {"/api/auth/me", "/"}, {"/static/css/base.css", "/"},
		{"/not-a-registered-route/", "/"}, {"/settings/extra/", "/"}, {strings.Repeat("/", 2049), "/"},
	} {
		if got := languageReturn(row.input); got != row.want {
			t.Fatal("finite safe return policy differs")
		}
	}
}

func TestNativeLanguageRegisteredChoiceCookieAndNegotiation(t *testing.T) {
	for _, row := range []struct {
		origin, target, choice string
		secure                 bool
	}{
		{"https://fixture.local", "https://fixture.local/i18n/setlang/", "ro", true},
		{"https://fixture.local", "http://fixture.local/i18n/setlang/", "en", true},
		{"http://127.0.0.1:8000", "http://127.0.0.1:8000/i18n/setlang/", "ro", false},
	} {
		_, mux := languageTestServer(t, row.origin)
		body := url.Values{"language": {row.choice}, "next": {"/settings/"}, "csrfmiddlewaretoken": {strings.Repeat("c", 52)}}.Encode()
		request := languageTestRequest(http.MethodPost, row.target, row.origin, body, true)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, request)
		cookies := w.Result().Cookies()
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/settings/" || w.Header().Get("Cache-Control") != "no-store" || len(cookies) != 1 {
			t.Fatal("registered language choice unavailable")
		}
		c := cookies[0]
		if c.Name != "django_language" || c.Value != row.choice || c.Path != "/" || c.Domain != "" || c.HttpOnly || c.MaxAge != 365*24*60*60 || c.SameSite != http.SameSiteLaxMode || c.Secure != row.secure || !c.Expires.IsZero() {
			t.Fatal("native display-cookie convention changed")
		}
		next := httptest.NewRequest(http.MethodGet, row.origin+"/settings/", nil)
		next.AddCookie(c)
		next.Header.Set("Accept-Language", map[string]string{"en": "ro", "ro": "en"}[row.choice])
		if language(next) != row.choice {
			t.Fatal("saved language did not take precedence over source Accept-Language")
		}
	}
}

func TestNativeLanguageRegisteredCSRFFormAndExactPathRefusals(t *testing.T) {
	s, mux := languageTestServer(t, "https://fixture.local")
	valid := url.Values{"language": {"ro"}, "next": {"/you/"}, "csrfmiddlewaretoken": {strings.Repeat("c", 52)}}.Encode()
	for _, row := range []struct {
		method, path, origin, body, header, content string
		proof                                       bool
		status                                      int
	}{
		{http.MethodGet, "/i18n/setlang/", "", "", "", "", false, http.StatusNotFound},
		{http.MethodHead, "/i18n/setlang/", "", "", "", "", false, http.StatusNotFound},
		{http.MethodPut, "/i18n/setlang/", "https://fixture.local", valid, "", "", true, http.StatusMethodNotAllowed},
		{http.MethodPost, "/i18n/setlang", "https://fixture.local", valid, "", "", true, http.StatusNotFound},
		{http.MethodPost, "/i18n/setlang/child/", "https://fixture.local", valid, "", "", true, http.StatusNotFound},
		{http.MethodPost, "/i18n/setlang/", "", valid, "", "", true, http.StatusForbidden},
		{http.MethodPost, "/i18n/setlang/", "https://foreign.invalid", valid, "", "", true, http.StatusForbidden},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", valid, "", "", false, http.StatusForbidden},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", valid, "incorrect", "", true, http.StatusForbidden},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", "language=fr", "", "", true, http.StatusBadRequest},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", "language=ro&language=en", "", "", true, http.StatusBadRequest},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", "language=ro&next=/&next=/you/", "", "", true, http.StatusBadRequest},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", "language=ro&user_id=9", "", "", true, http.StatusBadRequest},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", "language=%zz", "", "", true, http.StatusBadRequest},
		{http.MethodPost, "/i18n/setlang/", "https://fixture.local", `{"language":"ro"}`, strings.Repeat("c", 52), "application/json", true, http.StatusUnsupportedMediaType},
	} {
		request := languageTestRequest(row.method, "https://fixture.local"+row.path, row.origin, row.body, row.proof)
		if row.header != "" {
			request.Header.Set("X-CSRFToken", row.header)
		}
		if row.content != "" {
			request.Header.Set("Content-Type", row.content)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, request)
		if w.Code != row.status || len(w.Result().Cookies()) != 0 || (row.status == http.StatusMethodNotAllowed && w.Header().Get("Allow") != "GET, HEAD, POST") {
			t.Fatal("language refusal acquired a cookie, alias or method", w.Code)
		}
	}
	// Direct-handler method rejection remains405/POST. Registered GET/HEAD
	// retain the unchanged legacy404; other verbs use the original mux405.
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		r := languageTestRequest(method, "https://fixture.local/i18n/setlang/", "https://fixture.local", valid, true)
		w := httptest.NewRecorder()
		s.setLanguage(w, r)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != http.MethodPost || len(w.Result().Cookies()) != 0 {
			t.Fatal("direct language method guard changed")
		}
	}
	r := languageTestRequest(http.MethodPost, "https://fixture.local/i18n/setlang/", "https://fixture.local", valid, true)
	r.URL.RawPath = "/i18n/%73etlang/"
	w := httptest.NewRecorder()
	s.setLanguage(w, r)
	if w.Code != http.StatusNotFound || len(w.Result().Cookies()) != 0 {
		t.Fatal("encoded alias admitted")
	}
	s.Auth = nil
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, languageTestRequest(http.MethodPost, "https://fixture.local/i18n/setlang/", "https://fixture.local", valid, true))
	if w.Code != http.StatusServiceUnavailable || len(w.Result().Cookies()) != 0 {
		t.Fatal("nil CSRF authority did not fail closed")
	}
}

func TestNativeLanguageBoundedDeclaredAndStreamedForm(t *testing.T) {
	_, mux := languageTestServer(t, "https://fixture.local")
	for _, unknown := range []bool{false, true} {
		r := languageTestRequest(http.MethodPost, "https://fixture.local/i18n/setlang/", "https://fixture.local", "language=ro&next="+strings.Repeat("x", int(LanguageFormMaxBytes)), true)
		if unknown {
			r.ContentLength = -1
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge || len(w.Result().Cookies()) != 0 {
			t.Fatal("declared/streamed language form escaped bounded parser", w.Code)
		}
	}
	_, mux = languageTestServer(t, "https://fixture.local")
	for _, next := range []string{"https://foreign.invalid/", "//foreign.invalid/", "/logout/", "/settings/api-token/revoke/"} {
		values := url.Values{"language": {"en"}, "next": {next}, "csrfmiddlewaretoken": {strings.Repeat("c", 52)}}
		r := languageTestRequest(http.MethodPost, "https://fixture.local/i18n/setlang/", "", values.Encode(), true)
		r.Header.Set("Referer", "https://fixture.local/settings/")
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		want := "/"
		if next == "/settings/api-token/revoke/" {
			want = "/settings/"
		}
		if w.Code != http.StatusFound || w.Header().Get("Location") != want || len(w.Result().Cookies()) != 1 {
			t.Fatal("same-origin Referer or bounded safe return changed")
		}
	}
}
