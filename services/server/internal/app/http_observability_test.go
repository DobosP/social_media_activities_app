package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
)

func TestHTTPSBoundaryRejectsSpoofedProtocolAndPreservesSafeRedirect(t *testing.T) {
	a := &App{Config: Config{PublicURL: "https://social.fixture.test", AllowedHosts: []string{"social.fixture.test"}, SecureSSLRedirect: true, HSTSSeconds: 31536000, HSTSIncludeSubdomains: true, HSTSPreload: true}, proxyNetworks: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}, Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })}
	r := httptest.NewRequest("GET", "http://social.fixture.test/static/css/base.css?fixture=value", nil)
	r.RemoteAddr = "198.51.100.5:1234"
	r.Header.Set("X-Forwarded-Proto", "https")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 301 || w.Header().Get("Location") != "https://social.fixture.test/static/css/base.css?fixture=value" || w.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("spoofed TLS protocol bypassed redirect", w.Code, w.Header())
	}
	r.RemoteAddr = "192.0.2.5:1234"
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 204 || w.Header().Get("Strict-Transport-Security") != "max-age=31536000; includeSubDomains; preload" || w.Header().Get("Cross-Origin-Opener-Policy") != "same-origin" {
		t.Fatal("trusted TLS boundary failed", w.Code, w.Header())
	}
	r.Host = "attacker.fixture.test"
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 400 || w.Header().Get("Location") != "" {
		t.Fatal("invalid Host reflected in redirect")
	}
}

func TestNativeOperationalLogsExcludePrivateRequestMaterial(t *testing.T) {
	var log bytes.Buffer
	a := &App{Config: Config{AllowedHosts: []string{"social.fixture.test"}, RequestLoggingEnabled: true, LogFormat: "json", LogWriter: &log}, Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { panic("synthetic-private-panic-value") })}
	r := httptest.NewRequest("GET", "http://social.fixture.test/static/private-user-key?password=synthetic-secret-query", nil)
	r.Header.Set("Authorization", "Bearer synthetic-secret-header")
	r.Header.Set("X-Request-ID", "unsafe\r\nlog")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 500 || !requestIDPattern.MatchString(w.Header().Get("X-Request-ID")) {
		t.Fatal("bounded request id or generic error missing")
	}
	var entry map[string]any
	if json.Unmarshal(log.Bytes(), &entry) != nil || entry["route"] != "/static/*" || entry["status_code"] != float64(500) {
		t.Fatal("operational log missing bounded route/status", log.String())
	}
	for _, private := range []string{"private-user-key", "synthetic-secret-query", "synthetic-secret-header", "synthetic-private-panic-value", "unsafe", "password"} {
		if strings.Contains(log.String(), private) || strings.Contains(w.Body.String(), private) {
			t.Fatal("private material reached operational output", private)
		}
	}
}
