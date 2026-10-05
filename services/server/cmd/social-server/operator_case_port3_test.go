package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/app"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/commands"
	"github.com/DobosP/social_media_activities_app/services/server/internal/jobs"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestOperatorCase3RequestIDRejectsCRLFWithExactMintedHex(t *testing.T) {
	var log bytes.Buffer
	a := &app.App{Config: app.Config{AllowedHosts: []string{"fixture.test"}, RequestLoggingEnabled: true, LogFormat: "json", LogWriter: &log}, Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
	r := httptest.NewRequest("GET", "http://fixture.test/static/case3", nil)
	r.Header.Set("X-Request-ID", "a\r\nSet-Cookie: evil=1")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	id := w.Header().Get("X-Request-ID")
	decoded, err := hex.DecodeString(id)
	if w.Code != 200 || len(id) != 32 || err != nil || len(decoded) != 16 || strings.ContainsAny(id, "\r\n") || strings.Contains(log.String(), "Set-Cookie") || strings.Contains(log.String(), "evil=1") {
		t.Fatal("forged request ID was reflected or did not mint exact 32-character crypto hex")
	}
	var record struct {
		ID string `json:"request_id"`
	}
	if json.Unmarshal(log.Bytes(), &record) != nil || record.ID != id {
		t.Fatal("replacement request ID not correlated into log")
	}
}

func TestPostgresOperatorCase3ActualDueFailureReportsFixedSentryEvent(t *testing.T) {
	db := testdb.New(t, *configurationDSN, nil)
	searchPath := db.Config().ConnConfig.RuntimeParams["search_path"]
	if !strings.HasPrefix(searchPath, "native_fixture_") {
		t.Fatal("fixture failure must remain in isolated synthetic schema")
	}
	// Retention enqueues a deferred task; fail that actual insertion rather than
	// its later handler. Keep every table intact and target this cloned schema.
	// No provider, notification delivery or scheduler runs.
	if _, err := db.Exec(context.Background(), `CREATE FUNCTION operator_case3_due_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.kind='notifications.retention_purge' THEN RAISE EXCEPTION 'synthetic-case3-private-error'; END IF; RETURN NEW; END $$; CREATE TRIGGER operator_case3_due_failure BEFORE INSERT ON ops_deferredtask FOR EACH ROW EXECUTE FUNCTION operator_case3_due_failure()`); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(*configurationDSN)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", searchPath)
	u.RawQuery = q.Encode()
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	mediaDir, scratch := t.TempDir(), t.TempDir()
	for _, directory := range []string{mediaDir, scratch} {
		if err := os.Chmod(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMPDIR", t.TempDir()) // run restores nothing; testing restores the original scratch after this one-shot.
	transport := &cliErrorTransport{}
	factory := func(c ops.ErrorReporterConfig) (*ops.ErrorReporter, error) {
		c.Transport = transport
		return ops.NewErrorReporter(c)
	}
	var stdout, stderr bytes.Buffer
	env := fixtureEnv(map[string]string{"DATABASE_URL": u.String(), "SENTRY_DSN": "https://synthetic-case3-key@example.invalid/1", "ROEDU_SYNC_ENABLED": "false", "INDEXNOW_ENABLED": "false"})
	err = runWithReporter(context.Background(), []string{"--dev", "--due", "--media-dir", mediaDir, "--media-scratch", scratch, "--static-dir", filepath.Join(root, "static"), "--site-root", root}, env, strings.NewReader(""), &stdout, &stderr, factory)
	if err == nil || err.Error() != "native due jobs failed" {
		t.Fatal("actual due failure did not fail scheduler after startup", err)
	}
	var rows []jobs.Result
	if json.Unmarshal(stdout.Bytes(), &rows) != nil || len(rows) != len(jobs.DueNames) {
		t.Fatal("failed due run did not finish full registry and structured output")
	}
	foundFailure := false
	for _, row := range rows {
		if row.Name == "purge_read_notifications" && row.Status == "failed" {
			foundFailure = true
		}
	}
	if !foundFailure {
		t.Fatal("injected real due handler failure not reported")
	}
	transport.mu.Lock()
	defer transport.mu.Unlock()
	if len(transport.events) != 1 || transport.events[0].Message != string(ops.JobFailure) || !transport.closed {
		t.Fatal("actual due failure did not capture fixed Sentry event and close transport")
	}
	event := transport.events[0]
	if event.Request != nil || len(event.Contexts) != 0 || len(event.Exception) != 0 || len(event.User.ID)+len(event.User.Email)+len(event.User.Username)+len(event.User.IPAddress) != 0 {
		t.Fatal("private request/exception/user fields reached error telemetry")
	}
	for _, marker := range []string{"synthetic-case3-key", "synthetic-case3-private-error", "ops_deferredtask", "native_fixture_", "fixture-db"} {
		if strings.Contains(event.Message, marker) || strings.Contains(stdout.String()+stderr.String()+err.Error(), marker) {
			t.Fatal("private diagnostic/provider/DB material in due output or Sentry event")
		}
	}
}

func TestPostgresOperatorCase3ActualCollectionCapsOversizedPageLimit(t *testing.T) {
	db := testdb.New(t, *configurationDSN, nil)
	if _, err := db.Exec(context.Background(), `INSERT INTO taxonomy_activitycategory(slug,name,description,parent_id,created_at,updated_at) SELECT 'operator-case3-'||n,'Synthetic category '||n,'',NULL,now(),now() FROM generate_series(1,205) n`); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	catalog.New(db).Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "http://fixture.test/api/taxonomy/categories/?limit=5000", nil))
	var page struct {
		Count   int               `json:"count"`
		Next    string            `json:"next"`
		Results []json.RawMessage `json:"results"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || page.Count != 205 || len(page.Results) != 200 {
		t.Fatal("actual collection failed to cap oversized limit at 200", w.Code)
	}
	next, err := url.Parse(page.Next)
	if err != nil || next.Query().Get("limit") != "200" || next.Query().Get("offset") != "200" {
		t.Fatal("pagination cursor did not preserve actual capped page size")
	}
}

func TestPostgresOperatorCase3AssembledHealthzIsPublicAndDatabaseIndependent(t *testing.T) {
	db := testdb.New(t, *configurationDSN, nil)
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	storage, err := media.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	c := app.Config{PublicURL: "https://fixture.test", Secret: strings.Repeat("s", 48), AllowedHosts: []string{"fixture.test"}, Media: media.DefaultConfig(t.TempDir()), MediaStore: storage, SiteRoot: root, StaticDir: filepath.Join(root, "static")}
	a, err := app.New(context.Background(), db, c, false)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "https://fixture.test/healthz", nil))
	var body map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["status"] != "ok" || body["runtime"] != "go" {
		t.Fatal("assembled public liveness consulted unhealthy fixture DB", w.Code, w.Body.String())
	}
	if _, ok := body["database"]; ok {
		t.Fatal("assembled liveness exposed dependency state")
	}
}

func TestOperatorCase3ROEDUCanonicalAdapterFieldsAndMalformedWithholding(t *testing.T) {
	for _, name := range []string{"valid_venue_and_event", "null_tags", "overlong_address", "missing_longitude", "event_only"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile("../../internal/jobs/testdata/roedu-page.json")
			if err != nil {
				t.Fatal(err)
			}
			var page map[string]any
			if json.Unmarshal(raw, &page) != nil {
				t.Fatal("invalid fixture")
			}
			items := page["items"].([]any)
			venue := items[0].(map[string]any)
			switch name {
			case "null_tags":
				venue["tags"] = nil
				page["items"] = []any{venue}
			case "overlong_address":
				venue["address"].(map[string]any)["street"] = strings.Repeat("x", 256)
				page["items"] = []any{venue}
			case "missing_longitude":
				delete(venue["location"].(map[string]any), "lon")
				page["items"] = []any{venue}
			case "event_only":
				page["items"] = []any{items[1]}
			}
			raw, err = json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			client := &jobs.RoeduClient{BaseURL: "https://producer.invalid/", APIKey: "synthetic-case3-key", HTTP: &http.Client{Transport: responseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/v1/app-packs/social_media_activities_app/"+jobs.SocialPack || r.URL.Query().Get("layer") != "redistributable" || r.URL.Query().Get("city") != "Cluj-Napoca" || r.Header.Get("X-API-Key") != "synthetic-case3-key" || r.Header.Get("Accept") != "application/json" {
					t.Fatal("canonical public request identity or credential changed")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(raw)), Header: make(http.Header)}, nil
			})}}
			out := []commands.RawPlace{}
			err = (roeduPlaces{client: client}).Fetch(context.Background(), commands.PlaceOptions{City: "Cluj-Napoca"}, func(p commands.RawPlace) error { out = append(out, p); return nil })
			if err != nil || calls != 1 {
				t.Fatal("canonical adapter fetch failed or made multiple requests")
			}
			if name != "valid_venue_and_event" {
				if len(out) != 0 {
					t.Fatal("malformed/non-venue item reached adapter output")
				}
				return
			}
			if len(out) != 1 {
				t.Fatal("adapter did not select only the valid venue")
			}
			p := out[0]
			if p.Source != "roedu" || p.ExternalID != "venue-1" || p.Name != "Teatrul Național Cluj" || p.Lat != 46.7712 || p.Lon != 23.5949 || p.Website != "https://opera-cluj.ro/" || p.Attribution != "opera_cluj_events" || p.License != "RO-LAW-8-1996-ART-9" || p.Provenance != "" || !reflect.DeepEqual(p.Address, map[string]string{"street": "Piața Ștefan cel Mare 2", "city": "Cluj-Napoca", "county": "Cluj", "country": "RO"}) {
				t.Fatal("canonical raw venue lost source, license, coordinates, website or exact address")
			}
			for key, value := range map[string]any{"amenity": "theatre", "roedu:tags": []any{"venue:theatre"}, "roedu:city": "Cluj-Napoca", "roedu:county": "Cluj", "roedu:category": "theatre", "roedu:venue_category": "theatre", "roedu:source": "opera_cluj_events", "roedu:confidence": json.Number("1")} {
				if !reflect.DeepEqual(p.Tags[key], value) {
					t.Fatal("canonical venue tag/facet changed", key, p.Tags[key])
				}
			}
		})
	}
}

func TestOperatorCase3MetricsBearerGatesAndExactPublicExposition(t *testing.T) {
	s := ops.NewService(nil, ops.HTTPConfig{})
	mux := http.NewServeMux()
	s.Register(mux)
	s.Observe(200, 2*time.Millisecond)
	s.Observe(503, 3*time.Millisecond)
	s.Observe(404, -time.Second)
	for _, token := range []string{"", "Bearer wrong", "Bearer synthetic-case3-token"} {
		r := httptest.NewRequest("GET", "/metrics?private=synthetic-query-marker", nil)
		r.Header.Set("Authorization", token)
		r.AddCookie(&http.Cookie{Name: "sessionid", Value: "synthetic-cookie-marker"})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 || strings.Contains(w.Body.String(), "# HELP") {
			t.Fatal("unconfigured metrics admitted scrape")
		}
	}
	s.Config.MetricsToken = "synthetic-case3-token"
	for _, token := range []string{"", "Bearer wrong", "synthetic-case3-token", "bearer synthetic-case3-token", "Bearer synthetic-case3-token "} {
		r := httptest.NewRequest("GET", "/metrics", nil)
		r.Header.Set("Authorization", token)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal("metrics credential was loosely matched")
		}
	}
	r := httptest.NewRequest("GET", "/metrics?private=synthetic-query-marker", nil)
	r.Header.Set("Authorization", "Bearer synthetic-case3-token")
	r.AddCookie(&http.Cookie{Name: "sessionid", Value: "synthetic-cookie-marker"})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	want := "# HELP social_http_requests_total Total observed HTTP requests.\n# TYPE social_http_requests_total counter\nsocial_http_requests_total 3\n# HELP social_http_server_errors_total Total observed HTTP responses with status 500 or greater.\n# TYPE social_http_server_errors_total counter\nsocial_http_server_errors_total 1\n# HELP social_http_duration_seconds_sum Total observed HTTP request duration in seconds.\n# TYPE social_http_duration_seconds_sum counter\nsocial_http_duration_seconds_sum 0.005000000\n"
	if w.Code != 200 || w.Body.String() != want || w.Header().Get("Content-Type") != "text/plain; version=0.0.4" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authorized exposition changed descriptions, counts, duration or headers")
	}
	for _, private := range []string{"synthetic-case3-token", "synthetic-query-marker", "synthetic-cookie-marker", "username", "email", "ip=", "{"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("private request material or labels in aggregate metrics")
		}
	}
}

func TestOperatorCase3LivenessDoesNotNeedDatabase(t *testing.T) {
	s := ops.NewService(nil, ops.HTTPConfig{Version: "synthetic-case3"})
	mux := http.NewServeMux()
	s.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	var body map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body["status"] != "ok" {
		t.Fatal("public liveness unavailable without database")
	}
	if _, ok := body["database"]; ok {
		t.Fatal("liveness includes dependency state")
	}
}

func TestOperatorCase3BrowserHeadersCorrelationAndSafeRequestLog(t *testing.T) {
	for _, supplied := range []string{"", "trace-abc-123"} {
		var log bytes.Buffer
		a := &app.App{Config: app.Config{AllowedHosts: []string{"fixture.test"}, RequestLoggingEnabled: true, LogFormat: "json", LogLevel: "INFO", LogWriter: &log}, Static: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })}
		r := httptest.NewRequest("GET", "http://fixture.test/static/case3?email=synthetic-email-marker&token=synthetic-query-marker", nil)
		r.Header.Set("X-Request-ID", supplied)
		r.Header.Set("Authorization", "Bearer synthetic-auth-marker")
		r.AddCookie(&http.Cookie{Name: "sessionid", Value: "synthetic-cookie-marker"})
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		id := w.Header().Get("X-Request-ID")
		if w.Code != 200 || len(id) < 8 || (supplied != "" && id != supplied) {
			t.Fatal("request correlation missing or bounded inbound id changed")
		}
		for key, value := range map[string]string{"X-Content-Type-Options": "nosniff", "Referrer-Policy": "same-origin", "Cross-Origin-Opener-Policy": "same-origin", "Permissions-Policy": "geolocation=(self), camera=(), microphone=(), payment=(), usb=(), interest-cohort=()"} {
			if w.Header().Get(key) != value {
				t.Fatal("normal response lost browser header", key)
			}
		}
		var record map[string]json.RawMessage
		if json.Unmarshal(log.Bytes(), &record) != nil || len(record) != 6 {
			t.Fatal("request log is not the fixed six-field public operational object")
		}
		var gotID, method, route string
		var status int
		var duration int64
		if json.Unmarshal(record["request_id"], &gotID) != nil || gotID != id || json.Unmarshal(record["method"], &method) != nil || method != "GET" || json.Unmarshal(record["route"], &route) != nil || route != "/static/*" || json.Unmarshal(record["status_code"], &status) != nil || status != 200 || json.Unmarshal(record["duration_ms"], &duration) != nil || duration < 0 {
			t.Fatal("correlated operational fields missing")
		}
		for _, marker := range []string{"synthetic-email-marker", "synthetic-query-marker", "synthetic-auth-marker", "synthetic-cookie-marker", "/static/case3", "email="} {
			if strings.Contains(log.String(), marker) {
				t.Fatal("request log retained private URL/credential material")
			}
		}
	}
}

func TestPostgresOperatorCase3FixedRouteLogsExcludePrivateIDsQueriesAndBodies(t *testing.T) {
	db := testdb.New(t, *configurationDSN, nil)
	root, err := filepath.Abs("../../../..")
	if err != nil {
		t.Fatal(err)
	}
	storage, err := media.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	var log bytes.Buffer
	c := app.Config{PublicURL: "https://fixture.test", Secret: strings.Repeat("s", 48), AllowedHosts: []string{"fixture.test"}, Media: media.DefaultConfig(t.TempDir()), MediaStore: storage, SiteRoot: root, StaticDir: filepath.Join(root, "static"), RequestLoggingEnabled: true, LogFormat: "json", LogWriter: &log}
	a, err := app.New(context.Background(), db, c, false)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "https://fixture.test/api/places/98345678/?email=synthetic-email-marker&token=synthetic-query-marker", strings.NewReader(`{"private_body":"synthetic-body-marker"}`))
	r.Header.Set("X-Request-ID", "trace-abc-123")
	r.Header.Set("Authorization", "Bearer synthetic-auth-marker")
	r.AddCookie(&http.Cookie{Name: "sessionid", Value: "synthetic-cookie-marker"})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 404 {
		t.Fatal("fixture must reach actual registered missing-place handler", w.Code)
	}
	var record map[string]json.RawMessage
	if json.Unmarshal(log.Bytes(), &record) != nil || len(record) != 6 {
		t.Fatal("fixed operational log shape changed")
	}
	var route, id, method, event string
	var status int
	var duration int64
	if json.Unmarshal(record["event"], &event) != nil || event != "request" || json.Unmarshal(record["route"], &route) != nil || !strings.Contains(route, "/places/{id}") || json.Unmarshal(record["request_id"], &id) != nil || id != "trace-abc-123" || json.Unmarshal(record["method"], &method) != nil || method != "GET" || json.Unmarshal(record["status_code"], &status) != nil || status != 404 || json.Unmarshal(record["duration_ms"], &duration) != nil || duration < 0 {
		t.Fatal("actual registered-route request lacks correlated operational fields")
	}
	for _, marker := range []string{"98345678", "synthetic-email-marker", "synthetic-query-marker", "synthetic-body-marker", "synthetic-auth-marker", "synthetic-cookie-marker", "private_body", "email="} {
		if strings.Contains(log.String(), marker) {
			t.Fatal("request IDs, body, credentials or query reached operational log")
		}
	}
}

func TestPostgresOperatorCase3ReadinessExactDependencyStates(t *testing.T) {
	db := testdb.New(t, *configurationDSN, nil)
	s := ops.NewService(db, ops.HTTPConfig{})
	check := func(wantStatus int, want map[string]any) {
		t.Helper()
		w := httptest.NewRecorder()
		s.Ready(w, httptest.NewRequest("GET", "/readyz", nil))
		var got map[string]any
		if w.Code != wantStatus || json.Unmarshal(w.Body.Bytes(), &got) != nil || !reflect.DeepEqual(got, want) {
			t.Fatal("readiness status/dependency shape changed", w.Code, w.Body.String())
		}
	}
	check(200, map[string]any{"status": "ready", "draining": false, "database": true})
	s.Config.Cache = func(context.Context) error { return errors.New("synthetic cache unavailable") }
	check(503, map[string]any{"status": "degraded", "draining": false, "database": true, "cache": false})
	s.Config.Cache = func(context.Context) error { t.Fatal("draining readiness invoked cache"); return nil }
	s.Config.Storage = func(context.Context) error { t.Fatal("draining readiness invoked storage"); return nil }
	s.MarkDraining()
	check(503, map[string]any{"status": "draining", "draining": true})
	db.Close()
	s = ops.NewService(db, ops.HTTPConfig{})
	check(503, map[string]any{"status": "degraded", "draining": false, "database": false})
	w := httptest.NewRecorder()
	s.Health(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Fatal("liveness depends on healthy database")
	}
}

func TestPostgresOperatorCase3StaffStatsExactAggregateOnlyShape(t *testing.T) {
	db := testdb.New(t, *configurationDSN, nil)
	actor := testdb.Actor(t, db, "operator-case3-user", "adult")
	s := ops.NewService(db, ops.HTTPConfig{})
	mux := http.NewServeMux()
	s.Register(mux)
	for _, staff := range []bool{false, true} {
		actor.IsStaff = staff
		r := platform.WithActor(httptest.NewRequest("GET", "/api/ops/stats/", nil), actor)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		if !staff {
			if w.Code != 403 {
				t.Fatal("ordinary account admitted to statistics")
			}
			continue
		}
		var got map[string]int64
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !reflect.DeepEqual(got, map[string]int64{"users": 1, "activities": 0, "posts": 0, "bookings": 0, "donations_completed": 0, "donations_total_cents": 0}) {
			t.Fatal("staff statistics changed exact six aggregate fields", w.Code, w.Body.String())
		}
	}
}
