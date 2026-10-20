package web

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// LanguageFormMaxBytes also bounds the assembled App before its existing
// session-CSRF form reader. Other paths retain their existing limits.
const LanguageFormMaxBytes int64 = 4096

func languageReturn(next string) string {
	const fallback = "/"
	if next == "" || len(next) > 2048 || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return fallback
	}
	u, err := url.Parse(next)
	if err != nil || u.IsAbs() || u.Host != "" || u.User != nil || u.Opaque != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return fallback
	}
	for _, value := range []string{next, u.Path, u.RawQuery, u.Fragment} {
		decoded, err := url.QueryUnescape(value)
		if err != nil {
			return fallback
		}
		for _, text := range []string{value, decoded} {
			for _, char := range text {
				if char == '\\' || unicode.IsControl(char) {
					return fallback
				}
			}
		}
	}
	// The existing direct account error renderer deliberately retains its POST
	// action path in hidden next. Saving language returns to the same existing
	// AccountAction success target; no template/context or action is executed.
	if u.Path == routeURL("api_token_revoke") {
		return routeURL("settings")
	}
	// Downloads and non-HTML entry points are not language form destinations.
	if strings.HasPrefix(u.Path, "/api/") || strings.HasPrefix(u.Path, "/static/") || strings.HasPrefix(u.Path, "/media/") {
		return fallback
	}
	prefix := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")[0]
	for _, route := range legacyRouteBuckets[prefix] {
		if !route.Pattern.MatchString(u.Path) {
			continue
		}
		// Current non-HTML dispatch identities are source-derived from the
		// account/social/public download adapters. Do not invoke a producer.
		switch route.Name {
		case "account_export", "my_calendar", "open_data_snapshot", "service_worker", "events_feed", "events_feed_atom", "robots_txt", "robots", "indexnow_key_file", "indexnow", "llms_txt", "llms", "sitemap", "sitemap_xml", "schema", "openapi_schema":
			return fallback
		}
		// The common page adapter's existing SPA data flag selects JSON even
		// for an HTML page identity; it is not a language form destination.
		if u.Query().Get("_data") == "1" {
			return fallback
		}
		// Match the actual router's FIRST path match and GET admission. Looking
		// for a later alias could admit a path whose real first route is405.
		if hasMethod(route.Name, http.MethodGet) || len(legacyMethods[route.Name]) == 0 && !actionOnly[route.Name] {
			return u.String()
		}
		return fallback
	}
	return fallback
}

func (s *Server) setLanguage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != "/i18n/setlang/" || r.URL.EscapedPath() != "/i18n/setlang/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		platform.Error(w, http.StatusMethodNotAllowed, "Submit this form with POST.")
		return
	}
	if s.Auth == nil {
		platform.Error(w, http.StatusServiceUnavailable, "Language settings unavailable.")
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/x-www-form-urlencoded" {
		platform.Error(w, http.StatusUnsupportedMediaType, "Use the language form.")
		return
	}
	if r.ContentLength > LanguageFormMaxBytes {
		platform.Error(w, http.StatusRequestEntityTooLarge, "Language form too large.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, LanguageFormMaxBytes)
	if err := r.ParseForm(); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			platform.Error(w, http.StatusRequestEntityTooLarge, "Language form too large.")
		} else {
			platform.Error(w, http.StatusBadRequest, "Invalid form.")
		}
		return
	}
	for name, values := range r.PostForm {
		if (name != "language" && name != "next" && name != "csrfmiddlewaretoken") || len(values) != 1 || len(values[0]) > 2048 {
			platform.Error(w, http.StatusBadRequest, "Invalid language form.")
			return
		}
	}
	values := r.PostForm["language"]
	if len(values) != 1 || (values[0] != "en" && values[0] != "ro") {
		platform.Error(w, http.StatusBadRequest, "Unsupported language.")
		return
	}
	// Every caller, including anonymous and API-token callers, uses the exact
	// pinned same-origin/double-submit check. Explicit bad headers stay bad.
	if s.Auth.CheckCSRF(formCSRF(r)) != nil {
		platform.Error(w, http.StatusForbidden, "CSRF verification failed.")
		return
	}
	next := languageReturn(r.PostForm.Get("next"))
	// Same native display-preference cookie convention; this is a UI choice,
	// not identity evidence, a legal exemption or an authority/consent change.
	http.SetCookie(w, &http.Cookie{Name: "django_language", Value: values[0], Path: "/", MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || strings.HasPrefix(s.Config.PublicURL, "https://")})
	http.Redirect(w, r, next, http.StatusFound)
}
