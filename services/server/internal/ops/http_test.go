package ops

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOperationalEndpointsHaveNoUserDataAndClosedMetrics(t *testing.T) {
	s := NewService(nil, HTTPConfig{Version: "generated-version"})
	mux := http.NewServeMux()
	s.Register(mux)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, httptest.NewRequest("GET", "/api/health", nil))
	if out.Code != 200 || !strings.Contains(out.Body.String(), "generated-version") {
		t.Fatal(out.Code, out.Body)
	}
	out = httptest.NewRecorder()
	mux.ServeHTTP(out, httptest.NewRequest("GET", "/metrics", nil))
	if out.Code != 403 {
		t.Fatal("metrics default-open")
	}
	s.Config.MetricsToken = "generated-test-metrics"
	r := httptest.NewRequest("GET", "/metrics", nil)
	r.Header.Set("Authorization", "Bearer generated-test-metrics")
	s.Observe(500, time.Millisecond)
	out = httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	if out.Code != 200 || strings.Contains(out.Body.String(), "username") || strings.Contains(out.Body.String(), "ip=") {
		t.Fatal("private metrics metadata", out.Code, out.Body)
	}
	s.MarkDraining()
	out = httptest.NewRecorder()
	s.Ready(out, httptest.NewRequest("GET", "/api/ready", nil))
	if out.Code != 503 || !strings.Contains(out.Body.String(), "draining") {
		t.Fatal("draining node reported ready")
	}
}
func TestCSPDigestGroupsJSONLWithoutCredentials(t *testing.T) {
	row := `{"csp-report":{"effective-directive":"script-src 'self'","blocked-uri":"https://user:password@asset.example/code.js?private=secret#fragment","document-uri":"/profile/?private=secret"}}`
	digest, err := ReadCSPDigest(strings.NewReader(row + "\n" + row + "\n{bad"))
	if err != nil || digest.Total != 2 || digest.Malformed != 1 || len(digest.Groups) != 1 || digest.Groups[0].Count != 2 || digest.Groups[0].Blocked != "https://asset.example/code.js" || digest.Groups[0].Document != "/profile/" {
		t.Fatal("private or incorrect CSP digest", digest, err)
	}
}
func TestCSPPrivacyBudgetAndAlways204(t *testing.T) {
	s := NewService(nil, HTTPConfig{})
	for _, body := range []string{`{"csp-report":{"effective-directive":"script-src 'self'","document-uri":"https://site.example/account/?token=generated#private","blocked-uri":"https://asset.example/file.js?secret=generated"}}`, "{bad", strings.Repeat("x", 9<<10)} {
		out := httptest.NewRecorder()
		s.CSPReport(out, httptest.NewRequest(http.MethodPost, "/api/ops/csp-report/", strings.NewReader(body)))
		if out.Code != 204 {
			t.Fatal("browser report rejected")
		}
	}
	// The ceiling is per process, so a missing database never drops reports.
	rows := s.RecentCSP()
	if len(rows) != 1 || rows[0].Document != "https://site.example/account/" || rows[0].Blocked != "https://asset.example/file.js" || rows[0].Directive != "script-src" {
		t.Fatal("CSP secrets retained", rows)
	}
	rows, err := ParseCSP([]byte(`{"csp-report":{"effective-directive":"script-src self","document-uri":"https://site.example/account/?token=generated#private","blocked-uri":"https://asset.example/file.js?secret=generated"}}`))
	if err != nil || len(rows) != 1 || rows[0].Document != "https://site.example/account/" || rows[0].Blocked != "https://asset.example/file.js" || rows[0].Directive != "script-src" {
		t.Fatal("CSP secrets retained", rows, err)
	}
}

// A nil pool proves the ceiling never consults the database: any admission
// through it would fail and drop every report.
func TestCSPIngressCeilingIsPerProcessWithoutDatabase(t *testing.T) {
	s := NewService(nil, HTTPConfig{})
	report := `{"csp-report":{"effective-directive":"img-src","document-uri":"https://site.example/","blocked-uri":"https://asset.example/a.png"}}`
	for i := 0; i < 121; i++ {
		out := httptest.NewRecorder()
		s.CSPReport(out, httptest.NewRequest(http.MethodPost, "/api/ops/csp-report/", strings.NewReader(report)))
		if out.Code != 204 {
			t.Fatal("browser report rejected", i, out.Code)
		}
	}
	if got := len(s.RecentCSP()); got != 120 {
		t.Fatal("per-process CSP ceiling", got)
	}
	s.mu.Lock()
	s.budgetUntil = time.Now().Add(-time.Second)
	s.mu.Unlock()
	s.CSPReport(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/ops/csp-report/", strings.NewReader(report)))
	if got := len(s.RecentCSP()); got != 121 {
		t.Fatal("CSP window did not reset", got)
	}
}
