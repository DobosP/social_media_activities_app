package app

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRequestLogThresholdsAndTighterBrowserPermissions(t *testing.T) {
	for _, status := range []int{204, 404, 503} {
		var log bytes.Buffer
		a := &App{Config: Config{AllowedHosts: []string{"fixture.test"}, RequestLoggingEnabled: true, LogLevel: "ERROR", LogWriter: &log, PermissionsPolicy: strings.Replace(DefaultPermissionsPolicy, "geolocation=(self)", "geolocation=()", 1)}, Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://fixture.test/static/test", nil))
		if (log.Len() > 0) != (status >= 500) {
			t.Fatal("log level did not filter", status)
		}
		if w.Header().Get("Permissions-Policy") != a.Config.PermissionsPolicy {
			t.Fatal("browser policy ignored")
		}
	}
	for _, raw := range []string{"", "camera=(self)", "geolocation=(*)", DefaultPermissionsPolicy + "\r\nx: bad"} {
		if ValidatePermissionsPolicy(raw) {
			t.Fatal("unreviewed permissions accepted")
		}
	}
}

func TestAssembledAppCarriesRequestMemoryBudget(t *testing.T) {
	a:=&App{Config:Config{AllowedHosts:[]string{"fixture.test"},DataUploadMemoryBytes:3},Static:http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
		if _,err:=platform.NewUploadBudget(r.Context()).ReadField(strings.NewReader("four"),100);err==nil {t.Fatal("configured budget lost during request adaptation")}
		w.WriteHeader(204)
	})}
	w:=httptest.NewRecorder();a.ServeHTTP(w,httptest.NewRequest("GET","http://fixture.test/static/test",nil))
	if w.Code!=204 {t.Fatal("fixture handler unavailable")}
}

func TestHTTPPolicyRejectsLooserCaps(t *testing.T) {
	for _, c := range []Config{{MaxRequestBodyBytes: 8<<20 + 1}, {DataUploadMemoryBytes: -1}, {LogLevel: "private-value"}, {PermissionsPolicy: "camera=(self)"}} {
		if normalizeHTTPPolicy(&c) == nil {
			t.Fatal("invalid HTTP policy accepted")
		}
	}
}
