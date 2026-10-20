package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// Credentials/cookies/rendered HTML stay in memory. This fixture uses the
// real assembled App and the already reviewed account password-login helper.
func languageAppCookie(t *testing.T, w *httptest.ResponseRecorder, value, target string) *http.Cookie {
	t.Helper()
	cookies := w.Result().Cookies()
	if w.Code != http.StatusFound || w.Header().Get("Location") != target || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Social-Runtime") != "go" || len(cookies) != 1 {
		t.Fatal("registered language choice/response unavailable", w.Code)
	}
	c := cookies[0]
	if c.Name != "django_language" || c.Value != value || c.Path != "/" || c.Domain != "" || c.HttpOnly || !c.Secure || c.MaxAge != 365*24*60*60 || c.SameSite != http.SameSiteLaxMode {
		t.Fatal("language choice acquired identity-cookie semantics")
	}
	return c
}

func languageAppAnonymous(t *testing.T, a *App) familyAppCredential {
	t.Helper()
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://app.example/login/", nil))
	if w.Code != http.StatusOK {
		t.Fatal("anonymous CSRF seed unavailable")
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "csrftoken" {
			return familyAppCredential{CSRF: cookie}
		}
	}
	t.Fatal("anonymous CSRF cookie unavailable")
	return familyAppCredential{}
}

func TestNativeLanguageAssembledAnonymousSessionAndTokenRequireSameCSRF(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	anonymous := languageAppAnonymous(t, f.app)
	token := f.token(t, f.owner, time.Now().UTC()) // Generated in memory, never logged/exported.
	before := f.counts(t)
	for _, identity := range []string{"anonymous", "session", "token"} {
		t.Run(identity, func(t *testing.T) {
			credential := anonymous
			if identity == "session" {
				credential = f.ownerCookie
			}
			extra := func(r *http.Request) {
				if identity == "token" {
					r.Header.Set("Authorization", "Token "+token)
				}
			}
			w := accountCenterAppRequest(t, f.app, credential, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"ro"}, "next": {"/settings/"}}, true, extra)
			choice := languageAppCookie(t, w, "ro", "/settings/")
			get := httptest.NewRequest(http.MethodGet, "https://app.example/login/", nil)
			get.AddCookie(choice)
			get.Header.Set("Accept-Language", "en")
			page := httptest.NewRecorder()
			f.app.ServeHTTP(page, get)
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<html lang="ro"`) {
				t.Fatal("saved cookie did not affect next anonymous HTML")
			}
			for _, probe := range []struct {
				proof          bool
				origin, header string
			}{{false, "https://app.example", ""}, {true, "https://foreign.invalid", ""}, {true, "", ""}, {true, "https://app.example", "incorrect"}} {
				w = accountCenterAppRequest(t, f.app, credential, http.MethodPost, "/i18n/setlang/", probe.origin, url.Values{"language": {"en"}, "next": {"/you/"}}, probe.proof, func(r *http.Request) {
					extra(r)
					if probe.header != "" {
						r.Header.Set("X-CSRFToken", probe.header)
					}
				})
				if w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 || f.counts(t) != before {
					t.Fatal("caller class bypassed language CSRF/origin boundary", w.Code)
				}
			}
			w = accountCenterAppRequest(t, f.app, credential, http.MethodPost, "/i18n/setlang/", "", url.Values{"language": {"en"}, "next": {"/settings/api-token/revoke/"}}, true, func(r *http.Request) { extra(r); r.Header.Set("Referer", "https://app.example/settings/") })
			languageAppCookie(t, w, "en", "/settings/")
			if f.counts(t) != before {
				t.Fatal("language preference mutated account/token/session/audit rows")
			}
		})
	}
	// A valid token does not exempt this public preference action from CSRF.
	// An invalid supplied token still fails in the unchanged App admission.
	w := accountCenterAppRequest(t, f.app, anonymous, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"ro"}}, true, func(r *http.Request) { r.Header.Set("Authorization", "Token generated-invalid-memory-only") })
	if w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 || f.counts(t) != before {
		t.Fatal("invalid credential gained a language response")
	}
}

func TestNativeLanguageAssembledInvalidFormsAndLocalReturns(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	before := f.counts(t)
	for _, value := range []string{"", "fr", "RO", "ro-RO", " en", "en,ro", "ro\x00"} {
		w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {value}, "next": {"/settings/"}}, true, nil)
		if w.Code != http.StatusBadRequest || len(w.Result().Cookies()) != 0 || f.counts(t) != before {
			t.Fatal("unsupported language changed cookie or account state")
		}
	}
	for _, values := range []url.Values{{"language": {"en", "ro"}}, {"language": {"ro"}, "next": {"/", "/settings/"}}, {"language": {"ro"}, "user_id": {"9"}}, {"language": {"ro"}, "csrfmiddlewaretoken": {"duplicate", "duplicate"}}} {
		w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", values, false, func(r *http.Request) { r.Header.Set("X-CSRFToken", f.ownerCookie.CSRF.Value) })
		if w.Code != http.StatusBadRequest || len(w.Result().Cookies()) != 0 {
			t.Fatal("ambiguous/foreign form field admitted", w.Code)
		}
	}
	for _, next := range []string{"https://foreign.invalid/", "//foreign.invalid/", "/%2Fforeign.invalid", "/settings/?x=%0a", "/logout/", "/account/export/", "/not-registered/"} {
		w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"en"}, "next": {next}}, true, nil)
		languageAppCookie(t, w, "en", "/")
	}
	for _, target := range []string{"/account/export/", "/account/calendar.ics", "/open-data/snapshot/events.json", "/sw.js", "/events/feed/", "/events/feed/atom/"} {
		for _, suffix := range []string{"", "?source=language#download"} {
			w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"en"}, "next": {target + suffix}}, true, nil)
			languageAppCookie(t, w, "en", "/")
		}
	}
	for _, target := range []string{"/open-data/", "/open-data/?catalog=1#licensing"} {
		w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"en"}, "next": {target}}, true, nil)
		languageAppCookie(t, w, "en", target)
	}
	w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"en"}, "next": {"/settings/?_data=1"}}, true, nil)
	languageAppCookie(t, w, "en", "/")
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"ro"}, "next": {"/settings/api-token/revoke/?error=1#message"}}, true, nil)
	choice := languageAppCookie(t, w, "ro", "/settings/")
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, "/settings/", "", nil, false, func(r *http.Request) { r.AddCookie(choice); r.Header.Set("Accept-Language", "en") })
	if !strings.Contains(accountCenterAppHTML(t, w), `<html lang="ro"`) || f.counts(t) != before {
		t.Fatal("error-path return became POST-only405 or mutated account state")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		w = accountCenterAppRequest(t, f.app, f.ownerCookie, method, "/i18n/setlang/", "https://app.example", nil, true, func(r *http.Request) { r.Header.Set("X-CSRFToken", f.ownerCookie.CSRF.Value) })
		want, allow := http.StatusMethodNotAllowed, "GET, HEAD, POST"
		if method == http.MethodGet || method == http.MethodHead {
			want, allow = http.StatusNotFound, ""
		}
		if w.Code != want || w.Header().Get("Allow") != allow || len(w.Result().Cookies()) != 0 {
			t.Fatal("registered language fallback/method boundary changed", w.Code)
		}
	}
	for _, path := range []string{"/i18n/setlang", "/i18n/setlang/extra/", "/i18n/%73etlang/"} {
		w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, path, "https://app.example", url.Values{"language": {"ro"}}, true, nil)
		if w.Code != http.StatusNotFound || len(w.Result().Cookies()) != 0 {
			t.Fatal("language alias/subtree admitted", w.Code)
		}
	}
	if f.counts(t) != before {
		t.Fatal("negative language cases changed source account rows")
	}
}

func TestNativeLanguagePublicChoiceCannotReviveARevokedCurrentSession(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	logout := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/logout/", "https://app.example", nil, true, nil)
	if logout.Code != http.StatusSeeOther {
		t.Fatal("current synthetic session could not be revoked")
	}
	retired := f.counts(t)
	// A stale session is now anonymous. Language remains a public CSRF-protected
	// preference, and must not turn that stale credential into account access.
	w := accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"ro"}, "next": {"/settings/"}}, true, nil)
	choice := languageAppCookie(t, w, "ro", "/settings/")
	w = accountCenterAppRequest(t, f.app, f.ownerCookie, http.MethodGet, "/settings/", "", nil, false, func(r *http.Request) { r.AddCookie(choice) })
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login/?next=%2Fsettings%2F" || f.counts(t) != retired {
		t.Fatal("language cookie revived stale account authority")
	}
	w = accountCenterAppRequest(t, f.app, f.peerCookie, http.MethodGet, "/settings/", "", nil, false, nil)
	accountCenterAppHTML(t, w)
	if f.counts(t) != retired {
		t.Fatal("language action changed peer/current sessions")
	}
}

type languageCountingReader struct {
	reader io.Reader
	bytes  int
}

func (r *languageCountingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += n
	return n, err
}

func TestNativeLanguageAssembledLimitsBeforeCurrentSessionFormReader(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	anonymous := languageAppAnonymous(t, f.app)
	before := f.counts(t)
	for _, session := range []bool{false, true} {
		for _, unknown := range []bool{false, true} {
			for _, header := range []bool{false, true} {
				for _, size := range []int{4095, 4096, 4097} {
					credential := anonymous
					if session {
						credential = f.ownerCookie
					}
					base := url.Values{"language": {"ro"}, "next": {"/settings/"}, "csrfmiddlewaretoken": {credential.CSRF.Value}}.Encode()
					body := base + strings.Repeat("&", size-len(base)) // Ignored empty separators preserve exactly three valid fields.
					counter := &languageCountingReader{reader: strings.NewReader(body)}
					r := httptest.NewRequest(http.MethodPost, "https://app.example/i18n/setlang/", counter)
					r.ContentLength = int64(len(body))
					if unknown {
						r.ContentLength = -1
					}
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					r.Header.Set("Origin", "https://app.example")
					r.AddCookie(credential.CSRF)
					if session {
						r.AddCookie(credential.Session)
					}
					if header {
						r.Header.Set("X-CSRFToken", credential.CSRF.Value)
					}
					w := httptest.NewRecorder()
					f.app.ServeHTTP(w, r)
					want := http.StatusFound
					if size > 4096 {
						want = http.StatusRequestEntityTooLarge
						if unknown && session && !header {
							want = http.StatusBadRequest
						}
					} // Existing upstream ParseForm error status.
					if w.Code != want || counter.bytes > 4097 || size > 4096 && !unknown && counter.bytes != 0 || f.counts(t) != before {
						t.Fatal("language body escaped exact declared/stream reader boundary", w.Code)
					}
					if size <= 4096 {
						languageAppCookie(t, w, "ro", "/settings/")
					} else if len(w.Result().Cookies()) != 0 {
						t.Fatal("oversize form acquired cookie")
					}
				}
			}
		}
	}
	// Literal inverse in the source packet proves every nonmatching path keeps
	// the old code; these registered boundary probes also distinguish the cap.
	for _, path := range []string{"/i18n/setlang", "/i18n/setlang/extra/", "/i18n/%73etlang/", "/not-a-language-action/"} {
		r := httptest.NewRequest(http.MethodPost, "https://app.example"+path, strings.NewReader(strings.Repeat("x", 5000)))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		f.app.ServeHTTP(w, r)
		if w.Code != http.StatusNotFound || len(w.Result().Cookies()) != 0 {
			t.Fatal("language-specific cap changed a neighboring path", w.Code)
		}
	}
	if f.counts(t) != before {
		t.Fatal("limit probes changed account state")
	}
}
