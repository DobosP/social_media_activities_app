package main

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHaversineAntipodesRemainFinite(t *testing.T) {
	distance := haversineMeters(46.7712, 23.6236, -46.7712, -156.3764)
	if math.IsNaN(distance) || math.Abs(distance-math.Pi*earthRadiusM) > 1 {
		t.Fatalf("antipodal distance=%v, want half the circumference", distance)
	}
}

func TestEventsPublicQueryPlaceAndSearch(t *testing.T) {
	h, _ := newTestServer(t, nil)
	for _, tc := range []struct {
		query string
		ids   []int64
	}{
		{"place=10", []int64{1}},
		{"place=11&activity=football&city=cluj-napoca", []int64{2}},
		{"place=999", []int64{}},
		{"q=LEVELS", []int64{1}}, // Description is part of the public event search.
		{"q=SPORTS", []int64{2}}, // The approved public venue name is searchable too.
		{"q=%20book%20", []int64{4}},
		{"city=%20CLUJ-NAPOCA%20", []int64{1, 2}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			rec := doReq(t, h, http.MethodGet, "/agent/v1/events?"+tc.query)
			if rec.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			assertIDs(t, idsOf(t, decodeEnvelope(t, rec).Data), tc.ids)
		})
	}
}

func TestEventsPublicQueryNearAliasesAndNearestOrder(t *testing.T) {
	h, _ := newTestServer(t, nil)
	for _, query := range []string{
		"near=46.7810881,23.6236&radius_m=2000",
		"near_lat=46.7810881&near_lon=23.6236&radius_m=2000",
	} {
		rec := doReq(t, h, http.MethodGet, "/agent/v1/events?"+query)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", query, rec.Code, rec.Body.String())
		}
		assertIDs(t, idsOf(t, decodeEnvelope(t, rec).Data), []int64{2, 1})
	}
}

func TestEventsPublicQueryInvalidCoordinatesFailClosed(t *testing.T) {
	h, _ := newTestServer(t, nil)
	for _, query := range []string{
		"place=not-an-id", "place=9223372036854775808",
		"near=NaN,23.6", "near=46.7,Inf", "near=46.7,-Inf",
		"near_lat=NaN&near_lon=23.6", "near_lat=46.7&near_lon=Inf",
		"near_lat=91&near_lon=23.6", "near_lat=46.7&near_lon=-181",
		"near_lat=46.7", "near_lon=23.6", "near_lat=&near_lon=23.6",
		"near=46.7,23.6&near_lat=46.7&near_lon=23.6",
		"near=46.7,23.6&near=46.8,23.6",
		"near_lat=46.7&near_lat=46.8&near_lon=23.6", "near=",
	} {
		t.Run(query, func(t *testing.T) {
			rec := doReq(t, h, http.MethodGet, "/agent/v1/events?"+query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d, want400; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestConfiguredPageCapIncludesDefault(t *testing.T) {
	h, _ := newTestServer(t, func(cfg *Config) { cfg.MaxLimit = 1 })
	for _, dataset := range []string{"events", "places", "activities"} {
		rec := doReq(t, h, http.MethodGet, "/agent/v1/"+dataset)
		env := decodeEnvelope(t, rec)
		if rec.Code != http.StatusOK || env.Limit != 1 || env.Count != 1 {
			t.Fatalf("%s: status=%d limit=%d count=%d", dataset, rec.Code, env.Limit, env.Count)
		}
	}
}

func TestConditionalRequestsCannotSuppressInvalidQueriesOrMissingDetails(t *testing.T) {
	h, loader := newTestServer(t, nil)
	for _, tc := range []struct {
		url    string
		status int
	}{
		{"/agent/v1/events?place=bad", http.StatusBadRequest},
		{"/agent/v1/events?near_lat=46.7", http.StatusBadRequest},
		{"/agent/v1/events?from=bad", http.StatusBadRequest},
		{"/agent/v1/events?limit=-1", http.StatusBadRequest},
		{"/agent/v1/places?near=NaN,23.6", http.StatusBadRequest},
		{"/agent/v1/places?offset=-1", http.StatusBadRequest},
		{"/agent/v1/activities?place=bad", http.StatusBadRequest},
		{"/agent/v1/activities?limit=-1", http.StatusBadRequest},
		{"/agent/v1/events/999", http.StatusNotFound},
		{"/agent/v1/events/bad", http.StatusNotFound},
		{"/agent/v1/places/999", http.StatusNotFound},
		{"/agent/v1/places/bad", http.StatusNotFound},
	} {
		t.Run(tc.url, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			req.Header.Set("If-None-Match", computeETag(loader.Current().version, req))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status=%d, want%d; body=%s", rec.Code, tc.status, rec.Body.String())
			}
		})
	}
}

func TestHealthGenerationAgeSurvivesRecentReload(t *testing.T) {
	h, loader := newTestServer(t, nil)
	snap := *loader.Current()
	snap.GeneratedAt = time.Now().Add(-30 * time.Minute).UTC().Format(time.RFC3339)
	snap.LoadedAt = time.Now().Add(-5 * time.Second)
	loader.current.Store(&snap)
	rec := doReq(t, h, http.MethodGet, "/agent/v1/healthz")
	var body struct {
		GenerationAge int64 `json:"snapshot_age_seconds"`
		LoadedAge     int64 `json:"snapshot_loaded_age_seconds"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || body.GenerationAge < 1800 || body.LoadedAge < 5 || body.LoadedAge > 10 {
		t.Fatalf("status=%d ages=%+v", rec.Code, body)
	}
}

func TestHealthInvalidGeneration(t *testing.T) {
	for _, generated := range []string{"bad", time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339)} {
		h, loader := newTestServer(t, nil)
		snap := *loader.Current()
		snap.GeneratedAt = generated
		loader.current.Store(&snap)
		rec := doReq(t, h, http.MethodGet, "/agent/v1/healthz")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("generation=%s: status=%d", generated, rec.Code)
		}
	}
}
