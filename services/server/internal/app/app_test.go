package app

import (
	"bytes"
	"context"
	"flag"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

var appDSN = flag.String("app-test-dsn", "", "explicit isolated generated native application fixture")

type closedScanner struct{}

func (closedScanner) Scan(context.Context, media.ScanInput) (media.Verdict, error) {
	return media.Verdict{}, media.ErrScanner
}
func TestMultipartCSRFPeekPreservesStreamingBytes(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("csrfmiddlewaretoken", strings.Repeat("a", 52))
	part, _ := writer.CreateFormFile("image", "synthetic.png")
	_, _ = part.Write(bytes.Repeat([]byte{42}, 70000))
	_ = writer.Close()
	original := append([]byte(nil), body.Bytes()...)
	r := httptest.NewRequest("POST", "/profile/avatar/", bytes.NewReader(original))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	token, err := multipartCSRF(r)
	if err != nil || token != strings.Repeat("a", 52) {
		t.Fatal("CSRF prefix rejected")
	}
	restored, _ := io.ReadAll(r.Body)
	if !bytes.Equal(original, restored) {
		t.Fatal("multipart bytes changed")
	}
	r = httptest.NewRequest("POST", "/profile/avatar/", bytes.NewReader(original))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=invalid")
	if _, err = multipartCSRF(r); err == nil {
		t.Fatal("wrong boundary")
	}
	tracked := &trackedUploadBody{Reader: bytes.NewReader(original)}
	r = httptest.NewRequest("POST", "/profile/avatar/", nil)
	r.Body = tracked
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if _, err = multipartCSRF(r); err != nil {
		t.Fatal(err)
	}
	if err = r.Body.Close(); err != nil || !tracked.closed {
		t.Fatal("CSRF peek lost underlying body closer")
	}
}

type trackedUploadBody struct {
	io.Reader
	closed bool
}

func (b *trackedUploadBody) Close() error { b.closed = true; return nil }
func TestAllowedHostBoundary(t *testing.T) {
	for _, test := range []struct {
		raw  string
		want bool
	}{{"app.example:8000", true}, {"app.example.", true}, {"APP.EXAMPLE", true}, {"evilapp.example", false}, {"app.example.evil", false}, {"app.example@evil", false}, {"app.example/", false}} {
		if got := allowedHost(test.raw, []string{"app.example"}); got != test.want {
			t.Fatal("host boundary", test.raw)
		}
	}
}
func TestNativeApplicationMuxAndAccountLifecycle(t *testing.T) {
	if *appDSN == "" {
		t.Skip("explicit generated fixture DSN required")
	}
	db := testdb.New(t, *appDSN, nil)
	root, _ := filepath.Abs("../../../../")
	storage, err := media.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	config := Config{PublicURL: "https://app.example", Secret: strings.Repeat("x", 48), Auth: authcore.Config{}, Accounts: accounts.Config{}, Media: media.DefaultConfig(t.TempDir()), MediaStore: storage, Scanner: closedScanner{}, SiteRoot: root, StaticDir: filepath.Join(root, "static")}
	a, err := New(context.Background(), db, config, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/healthz", "/readyz", "/api/health", "/api/ready", "/api/places/", "/api/events/", "/api/taxonomy/activities/", "/privacy/", "/terms/", "/login/", "/register/"} {
		r := httptest.NewRequest("GET", "https://app.example"+path, nil)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		if w.Header().Get("X-Social-Runtime") != "go" {
			t.Fatal("runtime header")
		}
	}
	csrf := strings.Repeat("a", 52)
	r := httptest.NewRequest("POST", "https://app.example/api/auth/signup", strings.NewReader(`{"username":"native-fixture-user","password":"synthetic-password-only"}`))
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("X-CSRFToken", csrf)
	r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal("signup", w.Code, w.Body.String())
	}
	var session *http.Cookie
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "sessionid" {
			session = cookie
		}
	}
	if session == nil {
		t.Fatal("session missing")
	}
	r = httptest.NewRequest("GET", "https://app.example/api/accounts/me/", nil)
	r.AddCookie(session)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"cohort":"unassigned"`) || !strings.Contains(w.Body.String(), `"can_participate":false`) {
		t.Fatal("unsafe pending account", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "https://app.example/api/v1/ops/csp-report/", strings.NewReader(`{"csp-report":{"blocked-uri":"https://example.invalid?a=private"}}`))
	r.AddCookie(session)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("browser CSP report incorrectly requires CSRF", w.Code)
	}
	r = httptest.NewRequest("DELETE", "https://app.example/api/accounts/me/", nil)
	r.AddCookie(session)
	r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("X-CSRFToken", csrf)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 204 {
		t.Fatal("native erasure", w.Code, w.Body.String())
	}
	r = httptest.NewRequest("GET", "https://app.example/api/accounts/me/", nil)
	r.AddCookie(session)
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("erased session survived", w.Code)
	}
}
