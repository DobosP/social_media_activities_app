package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAccessLogOmitsSearchTermsAndIdentities(t *testing.T) {
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	handler := loggingMiddleware(logger, next)
	for _, tc := range []struct{ method, url, route string }{
		{"GET", "/agent/v1/events/private-record-sentinel?q=private-query-sentinel", "event_detail"},
		{"GET", "/private-path-sentinel?token=private-query-sentinel", "unknown"},
		{"PRIVATE-METHOD-SENTINEL", "/agent/v1/places", "places"},
	} {
		logs.Reset()
		r := httptest.NewRequest(tc.method, tc.url, nil)
		r.RemoteAddr = "203.0.113.25:1234"
		r.Header.Set("Authorization", "private-auth-sentinel")
		r.Header.Set("Cookie", "private-cookie-sentinel")
		r.Header.Set("User-Agent", "private-agent-sentinel")
		handler.ServeHTTP(httptest.NewRecorder(), r)
		line := logs.String()
		if strings.Contains(strings.ToLower(line), "sentinel") || strings.Contains(line, "203.0.113.25") || strings.Contains(line, "query=") || strings.Contains(line, "path=") {
			t.Fatalf("request data leaked in log: %s", line)
		}
		if !strings.Contains(line, "route="+tc.route) || !strings.Contains(line, "status=404") {
			t.Fatalf("missing operational classes: %s", line)
		}
	}
}

func TestLimiterHardCapAndRecovery(t *testing.T) {
	rl := NewRateLimiter(60, 2)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	rl.now = func() time.Time { return clock }
	for i := range maxLimiterClients {
		if !rl.Allow(fmt.Sprintf("client-%d", i)) {
			t.Fatalf("client %d denied before cap", i)
		}
	}
	if rl.Allow("overflow") || len(rl.buckets) != maxLimiterClients {
		t.Fatal("unknown client must fail closed at the fixed memory cap")
	}
	if !rl.Allow("client-0") {
		t.Fatal("existing clients must retain their buckets at capacity")
	}
	if rl.Allow(strings.Repeat("x", 129)) || rl.Allow("") {
		t.Fatal("invalid keys must not consume memory")
	}
	clock = clock.Add(11 * time.Minute)
	if !rl.Allow("recovered") || len(rl.buckets) != 1 {
		t.Fatal("idle clients must expire and restore admission")
	}
}

func TestLimiterConcurrentClients(t *testing.T) {
	rl := NewRateLimiter(60, 3)
	rl.now = func() time.Time { return time.Unix(1, 0) }
	var workers sync.WaitGroup
	for i := range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := range 50 {
				rl.Allow(fmt.Sprintf("client-%d-%d", i, j%3))
			}
		}()
	}
	workers.Wait()
	if len(rl.buckets) != 96 {
		t.Fatalf("unexpected client count: %d", len(rl.buckets))
	}
}

func TestProxyKeyRejectsAmbiguousOrMalformedHeaders(t *testing.T) {
	for _, tc := range []struct {
		name         string
		headers      []string
		remote, want string
	}{
		{"canonical IPv6", []string{"2001:db8:0:0::1"}, "203.0.113.9:1234", "2001:db8::1"},
		{"mapped IPv4", []string{"::ffff:198.51.100.2"}, "203.0.113.9:1234", "198.51.100.2"},
		{"duplicate header", []string{"198.51.100.1", "198.51.100.2"}, "203.0.113.9:1234", "203.0.113.9"},
		{"arbitrary text", []string{"private-client-sentinel"}, "203.0.113.9:1234", "203.0.113.9"},
		{"empty rightmost", []string{"198.51.100.1, "}, "203.0.113.9:1234", "203.0.113.9"},
		{"IPv6 zone", []string{"fe80::1%private-zone-sentinel"}, "203.0.113.9:1234", "203.0.113.9"},
		{"malformed peer", nil, "private-peer-sentinel", "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/agent/v1/events", nil)
			r.RemoteAddr = tc.remote
			for _, header := range tc.headers {
				r.Header.Add("X-Forwarded-For", header)
			}
			if key := clientKey(r, true); key != tc.want {
				t.Fatalf("unexpected limiter identity: %q", key)
			}
		})
	}
}

func TestRequestBoundsAndHeadersAcrossFailures(t *testing.T) {
	loader := NewLoader("", testLogger())
	for _, tc := range []struct {
		name, method, url, body string
		status                  int
	}{
		{"normal", "GET", "/agent/v1/", "", 200},
		{"unknown", "GET", "/agent/v1/unknown", "", 404},
		{"write method", "POST", "/agent/v1/events", "", 405},
		{"preflight", "OPTIONS", "/agent/v1/events", "", 204},
		{"duplicate key", "GET", "/agent/v1/events?city=Cluj&city=Other", "", 400},
		{"malformed escape", "GET", "/agent/v1/events?q=%zz", "", 400},
		{"control character", "GET", "/agent/v1/events?q=%01", "", 400},
		{"invalid UTF8", "GET", "/agent/v1/events?q=%ff", "", 400},
		{"huge query", "GET", "/agent/v1/events?q=" + strings.Repeat("x", 2049), "", 414},
		{"huge value", "GET", "/agent/v1/events?q=" + strings.Repeat("x", 513), "", 400},
		{"huge path", "GET", "/agent/v1/" + strings.Repeat("x", 513), "", 414},
		{"read body", "GET", "/agent/v1/events", "private-body-sentinel", 400},
		{"head duplicate key", "HEAD", "/agent/v1/events?city=Cluj&city=Other", "", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler := buildHandler(NewApp(testConfig(), loader), NewRateLimiter(300, 60), testConfig(), testLogger())
			r := httptest.NewRequest(tc.method, tc.url, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			for header, want := range map[string]string{
				"Access-Control-Allow-Origin": "*", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "no-referrer", "X-Frame-Options": "DENY",
			} {
				if got := w.Header().Get(header); got != want {
					t.Errorf("missing %s security header", header)
				}
			}
			if w.Header().Get("Content-Security-Policy") == "" || w.Header().Get("Cache-Control") == "" {
				t.Error("every response needs CSP and cache policy")
			}
			if tc.method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal("HEAD error responses must not have a body")
			}
		})
	}
}

func TestRateLimitedResponsesRemainPrivate(t *testing.T) {
	loader := NewLoader("", testLogger())
	handler := buildHandler(NewApp(testConfig(), loader), NewRateLimiter(1, 1), testConfig(), testLogger())
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/agent/v1/openapi.json", nil))
		return w
	}
	if w := request(); w.Code != 200 {
		t.Fatalf("first request failed: %d", w.Code)
	}
	w := request()
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("throttled response lacks the safe error contract")
	}
}
