package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestServer builds the full request-handling chain (logging -> CORS ->
// rate limit -> routes) wired to a freshly-loaded copy of testdata, plus
// its own private rate limiter so tests never interfere with each other.
func newTestServer(t *testing.T, rateCfg func(*Config)) (http.Handler, *Loader) {
	t.Helper()
	loader, _ := newLoadedLoader(t)
	cfg := testConfig()
	if rateCfg != nil {
		rateCfg(&cfg)
	}
	app := NewApp(cfg, loader)
	rl := NewRateLimiter(cfg.RatePerMin, cfg.RateBurst)
	return buildHandler(app, rl, cfg, testLogger()), loader
}

func doReq(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) listEnvelope {
	t.Helper()
	var env listEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v; body=%s", err, rec.Body.String())
	}
	return env
}

func idsOf(t *testing.T, data []json.RawMessage) []int64 {
	t.Helper()
	out := make([]int64, len(data))
	for i, raw := range data {
		var rec struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatalf("decode record id: %v", err)
		}
		out[i] = rec.ID
	}
	return out
}

func assertIDs(t *testing.T, got []int64, want []int64) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("ids = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids = %v, want %v", got, want)
		}
	}
}

// --- events -----------------------------------------------------------

func TestEvents_HappyPath(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if env.APIVersion != "v1" {
		t.Errorf("api_version = %q", env.APIVersion)
	}
	if env.Total != 4 || env.Count != 4 {
		t.Errorf("total/count = %d/%d, want 4/4", env.Total, env.Count)
	}
	assertIDs(t, idsOf(t, env.Data), []int64{1, 2, 3, 4}) // starts_at asc
}

func TestEvents_FilterActivity(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?activity=football")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{2})
}

func TestEvents_FilterActivity_FallbackField(t *testing.T) {
	// Event 2 only carries "activity_type" in the fixture, not "activity";
	// the loader must resolve the slug from either key.
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?activity=chess")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{1})
}

func TestEvents_FilterCity(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?city=cluj-napoca")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{1, 2})
}

func TestEvents_FilterFromToRFC3339(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?from=2026-07-02T00:00:00Z&to=2026-07-06T00:00:00Z")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{2, 3})
}

func TestEvents_FilterFromToDateOnly(t *testing.T) {
	// "to" on a date-only value covers the whole day (exclusive +24h): a
	// same-day from/to window should include an event that starts later
	// that same day.
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?from=2026-07-05&to=2026-07-05")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{3})
}

func TestEvents_FilterNearRadius(t *testing.T) {
	h, _ := newTestServer(t, nil)

	// Sports Field is ~1099.5m from Central Park (see filters_test.go).
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?near=46.7712,23.6236&radius_m=2000")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{1, 2})

	rec = doReq(t, h, http.MethodGet, "/agent/v1/events?near=46.7712,23.6236&radius_m=1000")
	env = decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{1})
}

func TestEvents_FilterQ(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?q=CHESS")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{1})
}

func TestEvents_LimitCap(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?limit=10000")
	env := decodeEnvelope(t, rec)
	if env.Limit != 200 { // AGENT_API_MAX_LIMIT default via testConfig
		t.Errorf("Limit = %d, want clamped to 200", env.Limit)
	}
	if env.Count != 4 {
		t.Errorf("Count = %d, want 4", env.Count)
	}
}

func TestEvents_OffsetBeyondEnd(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events?offset=100")
	env := decodeEnvelope(t, rec)
	if len(env.Data) != 0 {
		t.Errorf("Data = %v, want empty", env.Data)
	}
	if env.Total != 4 {
		t.Errorf("Total = %d, want 4", env.Total)
	}
	if env.Offset != 100 {
		t.Errorf("Offset = %d, want 100", env.Offset)
	}
}

func TestEvents_DetailFound(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/events/1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var env detailEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var rec2 struct {
		ID    int64  `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(env.Data, &rec2); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if rec2.ID != 1 || rec2.Title != "Chess meetup in the park" {
		t.Errorf("data = %+v", rec2)
	}
}

func TestEvents_Detail404(t *testing.T) {
	h, _ := newTestServer(t, nil)
	for _, id := range []string{"9999", "not-a-number"} {
		rec := doReq(t, h, http.MethodGet, "/agent/v1/events/"+id)
		if rec.Code != http.StatusNotFound {
			t.Errorf("id=%s status = %d, want 404", id, rec.Code)
		}
		var env errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if env.Error.Code != "not_found" {
			t.Errorf("id=%s error code = %q", id, env.Error.Code)
		}
	}
}

func TestEvents_BadParams400(t *testing.T) {
	h, _ := newTestServer(t, nil)
	cases := []string{
		"/agent/v1/events?from=not-a-date",
		"/agent/v1/events?to=also-bad",
		"/agent/v1/events?near=not-valid",
		"/agent/v1/events?near=46.7712,23.6236&radius_m=abc",
		"/agent/v1/events?limit=-1",
		"/agent/v1/events?limit=abc",
		"/agent/v1/events?offset=-1",
	}
	for _, target := range cases {
		rec := doReq(t, h, http.MethodGet, target)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400; body=%s", target, rec.Code, rec.Body.String())
			continue
		}
		var env errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
			t.Fatalf("%s: decode error body: %v", target, err)
		}
		if env.Error.Code != "invalid_parameter" {
			t.Errorf("%s: error code = %q", target, env.Error.Code)
		}
	}
}

// --- places -----------------------------------------------------------

func TestPlaces_HappyPathAndSort(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/places")
	env := decodeEnvelope(t, rec)
	// Sorted by name asc: Central Park(10), City Library(12), Sports Field(11)
	assertIDs(t, idsOf(t, env.Data), []int64{10, 12, 11})
}

func TestPlaces_FilterActivity(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/places?activity=football")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{11})
}

func TestPlaces_FilterCity(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/places?city=CLUJ-NAPOCA")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{10, 11})
}

func TestPlaces_FilterNearRadius(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/places?near=46.7712,23.6236&radius_m=1000")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{10})

	rec = doReq(t, h, http.MethodGet, "/agent/v1/places?near=46.7712,23.6236&radius_m=2000")
	env = decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{10, 11})
}

func TestPlaces_FilterQ(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/places?q=library")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{12})
}

func TestPlaces_Detail(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/places/11")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	rec = doReq(t, h, http.MethodGet, "/agent/v1/places/404404")
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

// --- activities -------------------------------------------------------

func TestActivities_HappyPath(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/activities")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{100, 101})
}

func TestActivities_FilterActivityAndPlace(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/activities?activity=chess")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{100})

	rec = doReq(t, h, http.MethodGet, "/agent/v1/activities?place=11")
	env = decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{101})
}

func TestActivities_FilterFromTo(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/activities?from=2026-07-02&to=2026-07-02")
	env := decodeEnvelope(t, rec)
	assertIDs(t, idsOf(t, env.Data), []int64{101})
}

func TestActivities_BadPlaceParam(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/activities?place=not-an-int")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// --- taxonomy / manifest / healthz --------------------------------------

func TestTaxonomy(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/taxonomy")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		Categories []struct {
			Slug string `json:"slug"`
		} `json:"categories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Categories) != 4 || body.Categories[0].Slug != "chess" {
		t.Errorf("categories = %+v", body.Categories)
	}
}

func TestManifest(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/manifest")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		SchemaVersion    int            `json:"schema_version"`
		Site             string         `json:"site"`
		Truncated        bool           `json:"truncated"`
		SnapshotLoadedAt string         `json:"snapshot_loaded_at"`
		RecordCounts     map[string]int `json:"record_counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SchemaVersion != 1 || body.Site != "https://example.org" {
		t.Errorf("body = %+v", body)
	}
	if body.SnapshotLoadedAt == "" {
		t.Error("expected snapshot_loaded_at to be set")
	}
	if body.RecordCounts["events"] != 4 || body.RecordCounts["places"] != 3 || body.RecordCounts["activities"] != 2 {
		t.Errorf("record_counts = %+v", body.RecordCounts)
	}
}

func TestHealthz_OK(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/healthz")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v", body["status"])
	}
	if body["snapshot_generated_at"] != "2026-07-12T10:00:00Z" {
		t.Errorf("snapshot_generated_at = %v", body["snapshot_generated_at"])
	}
}

func TestHealthz_NoSnapshot(t *testing.T) {
	loader := NewLoader(t.TempDir(), testLogger())
	cfg := testConfig()
	app := NewApp(cfg, loader)
	rl := NewRateLimiter(cfg.RatePerMin, cfg.RateBurst)
	h := buildHandler(app, rl, cfg, testLogger())

	rec := doReq(t, h, http.MethodGet, "/agent/v1/healthz")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "no_snapshot" {
		t.Errorf("status = %v", body["status"])
	}
}

func TestDataEndpoints_503BeforeFirstLoad(t *testing.T) {
	loader := NewLoader(t.TempDir(), testLogger())
	cfg := testConfig()
	app := NewApp(cfg, loader)
	rl := NewRateLimiter(cfg.RatePerMin, cfg.RateBurst)
	h := buildHandler(app, rl, cfg, testLogger())

	for _, target := range []string{
		"/agent/v1/events", "/agent/v1/events/1", "/agent/v1/places",
		"/agent/v1/places/1", "/agent/v1/activities", "/agent/v1/manifest",
		"/agent/v1/taxonomy",
	} {
		rec := doReq(t, h, http.MethodGet, target)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", target, rec.Code)
		}
	}

	// Landing/openapi/healthz must still work with no snapshot loaded.
	for _, target := range []string{"/agent/v1/", "/agent/v1/openapi.json"} {
		rec := doReq(t, h, http.MethodGet, target)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", target, rec.Code)
		}
	}
}

// --- landing / openapi --------------------------------------------------

func TestLanding(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "https://example.org/open-data/") {
		t.Error("expected landing doc to link to the manifest's site /open-data/")
	}
}

func TestOpenAPI(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/openapi.json")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", body["openapi"])
	}
}

// --- caching / ETag -----------------------------------------------------

func TestETag_304AndChangeOnReload(t *testing.T) {
	h, loader := newTestServer(t, nil)

	rec1 := doReq(t, h, http.MethodGet, "/agent/v1/events")
	etag1 := rec1.Header().Get("ETag")
	if etag1 == "" {
		t.Fatal("expected an ETag header")
	}

	req2 := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
	req2.Header.Set("If-None-Match", etag1)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want 304", rec2.Code)
	}
	if rec2.Body.Len() != 0 {
		t.Errorf("expected empty body on 304, got %d bytes", rec2.Body.Len())
	}
	if rec2.Header().Get("ETag") != etag1 {
		t.Errorf("304 ETag = %q, want %q", rec2.Header().Get("ETag"), etag1)
	}
	if rec2.Header().Get("Cache-Control") == "" {
		t.Error("expected Cache-Control on 304")
	}
	if rec2.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header on 304")
	}

	// Force a snapshot change; the ETag must change, and the old
	// If-None-Match value must no longer match.
	writeUpdatedFixturesForServer(t, loader)

	rec3 := doReq(t, h, http.MethodGet, "/agent/v1/events")
	etag3 := rec3.Header().Get("ETag")
	if etag3 == etag1 {
		t.Error("expected the ETag to change after the snapshot changed")
	}
}

// writeUpdatedFixturesForServer reloads new content into loader's own
// snapshot directory and forces a reload. It relies on the Loader's dir
// field being the same temp dir newTestServer built from testdata.
func writeUpdatedFixturesForServer(t *testing.T, loader *Loader) {
	t.Helper()
	writeUpdatedFixtures(t, loader.dir)
	changed, err := loader.CheckReload()
	if err != nil {
		t.Fatalf("reload failed: %v", err)
	}
	if !changed {
		t.Fatal("expected the reload to detect a change")
	}
}

// --- rate limiting --------------------------------------------------------

func TestRateLimit_429ThenHealthzExempt(t *testing.T) {
	h, _ := newTestServer(t, func(c *Config) {
		c.RatePerMin = 60
		c.RateBurst = 2
	})

	for i := 0; i < 2; i++ {
		rec := doReq(t, h, http.MethodGet, "/agent/v1/manifest")
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d: status = %d, want 200", i, rec.Code)
		}
	}

	rec := doReq(t, h, http.MethodGet, "/agent/v1/manifest")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "60" {
		t.Errorf("Retry-After = %q, want 60", rec.Header().Get("Retry-After"))
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "rate_limited" {
		t.Errorf("error code = %q", env.Error.Code)
	}

	// healthz must remain exempt even though the bucket is exhausted.
	rec = doReq(t, h, http.MethodGet, "/agent/v1/healthz")
	if rec.Code != http.StatusOK {
		t.Errorf("healthz status = %d, want 200 (exempt from rate limiting)", rec.Code)
	}
}

// --- CORS / methods -----------------------------------------------------

func TestCORSHeaders(t *testing.T) {
	h, _ := newTestServer(t, nil)

	checkCORS := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
			t.Error("missing Access-Control-Allow-Origin")
		}
		if rec.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Error("missing Access-Control-Allow-Methods")
		}
		if rec.Header().Get("Access-Control-Allow-Headers") == "" {
			t.Error("missing Access-Control-Allow-Headers")
		}
	}

	rec := doReq(t, h, http.MethodGet, "/agent/v1/events")
	checkCORS(t, rec)

	rec = doReq(t, h, http.MethodGet, "/agent/v1/events/999999")
	checkCORS(t, rec)

	req := httptest.NewRequest(http.MethodOptions, "/agent/v1/events", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", rec.Code)
	}
	checkCORS(t, rec)

	// 304 case.
	rec1 := doReq(t, h, http.MethodGet, "/agent/v1/events")
	etag := rec1.Header().Get("ETag")
	req2 := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)
	checkCORS(t, rec2)
}

func TestMethodNotAllowed(t *testing.T) {
	h, _ := newTestServer(t, nil)
	rec := doReq(t, h, http.MethodPost, "/agent/v1/events")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	var env errorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error.Code != "method_not_allowed" {
		t.Errorf("error code = %q", env.Error.Code)
	}
}

// --- gzip -----------------------------------------------------------------

func TestGzipRoundTrip(t *testing.T) {
	h, _ := newTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/agent/v1/openapi.json", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (openapi.json should be >=1KB)", rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary = %q", rec.Header().Get("Vary"))
	}

	gz, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	plain, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("gzip read: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(plain, &body); err != nil {
		t.Fatalf("decode decompressed body: %v", err)
	}
	if body["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", body["openapi"])
	}
}

func TestGzip_NotAppliedWithoutAcceptEncoding(t *testing.T) {
	h, _ := newTestServer(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/agent/v1/openapi.json", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Error("did not expect gzip without an Accept-Encoding header")
	}
}
