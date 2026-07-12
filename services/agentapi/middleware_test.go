package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRateLimiter_BurstAndRefill(t *testing.T) {
	rl := NewRateLimiter(60, 3) // 1 token/sec refill, burst of 3
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return base }

	for i := 0; i < 3; i++ {
		if !rl.Allow("client-a") {
			t.Fatalf("expected allow on token %d", i)
		}
	}
	if rl.Allow("client-a") {
		t.Fatal("expected burst to be exhausted")
	}

	base = base.Add(1 * time.Second)
	if !rl.Allow("client-a") {
		t.Fatal("expected allow after a 1s refill")
	}
	if rl.Allow("client-a") {
		t.Fatal("expected exhausted again immediately after")
	}
}

func TestRateLimiter_PerClientIsolation(t *testing.T) {
	rl := NewRateLimiter(60, 1)
	base := time.Now()
	rl.now = func() time.Time { return base }

	if !rl.Allow("a") {
		t.Fatal("client a should get its own token")
	}
	if !rl.Allow("b") {
		t.Fatal("client b should get its own token, independent of a")
	}
	if rl.Allow("a") {
		t.Fatal("client a should be exhausted")
	}
}

func TestRateLimiter_SweepEvictsIdleBuckets(t *testing.T) {
	rl := NewRateLimiter(60, 5)
	base := time.Now()
	rl.now = func() time.Time { return base }

	rl.Allow("stale-client")
	if _, ok := rl.buckets["stale-client"]; !ok {
		t.Fatal("expected bucket to be created")
	}

	base = base.Add(11 * time.Minute)
	rl.Allow("other-client") // triggers a sweep pass as a side effect

	if _, ok := rl.buckets["stale-client"]; ok {
		t.Error("expected the idle bucket to be evicted after 11 minutes")
	}
	if _, ok := rl.buckets["other-client"]; !ok {
		t.Error("expected the active bucket to remain")
	}
}

func TestClientKey(t *testing.T) {
	t.Run("no trust proxy uses RemoteAddr", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
		r.RemoteAddr = "203.0.113.9:54321"
		r.Header.Set("X-Forwarded-For", "198.51.100.1")
		if got := clientKey(r, false); got != "203.0.113.9" {
			t.Errorf("clientKey = %q, want 203.0.113.9", got)
		}
	})

	t.Run("trust proxy uses last XFF hop", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
		r.RemoteAddr = "203.0.113.9:54321"
		r.Header.Set("X-Forwarded-For", "198.51.100.1, 198.51.100.2")
		if got := clientKey(r, true); got != "198.51.100.2" {
			t.Errorf("clientKey = %q, want 198.51.100.2", got)
		}
	})

	t.Run("trust proxy falls back to RemoteAddr without XFF", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
		r.RemoteAddr = "203.0.113.9:54321"
		if got := clientKey(r, true); got != "203.0.113.9" {
			t.Errorf("clientKey = %q, want 203.0.113.9", got)
		}
	})
}

func TestIfNoneMatchHit(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
	r.Header.Set("If-None-Match", `"abc-123"`)
	if !ifNoneMatchHit(r, `"abc-123"`) {
		t.Error("expected a match")
	}
	if ifNoneMatchHit(r, `"other"`) {
		t.Error("expected no match")
	}

	r2 := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
	if ifNoneMatchHit(r2, `"abc-123"`) {
		t.Error("expected no match without a header")
	}
}

func TestShouldGzip(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
	r.Header.Set("Accept-Encoding", "gzip, deflate")
	if !shouldGzip(r, 2048) {
		t.Error("expected gzip for a >=1KB body with Accept-Encoding: gzip")
	}
	if shouldGzip(r, 100) {
		t.Error("expected no gzip for a small body")
	}

	r2 := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
	if shouldGzip(r2, 2048) {
		t.Error("expected no gzip without an Accept-Encoding header")
	}
}
