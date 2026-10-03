package main

import (
	"bytes"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestServerDiagnosticsDoNotExposeRawErrors(t *testing.T) {
	var logs bytes.Buffer
	writer := serverErrorWriter{logger: log.New(&logs, "", 0)}
	raw := []byte("http: panic serving 203.0.113.25:1234: private-stack-sentinel")
	if n, err := writer.Write(raw); n != len(raw) || err != nil {
		t.Fatal("diagnostic writer must acknowledge the discarded bytes")
	}
	if strings.Contains(logs.String(), "sentinel") || strings.Contains(logs.String(), "203.0.113") {
		t.Fatal("stdlib diagnostic leaked request data")
	}
	if !strings.Contains(logs.String(), "HTTP server error") {
		t.Fatal("aggregate failure event missing")
	}
}

func TestHealthcheckUsesLoopbackWithoutProxyOrRedirects(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	var redirects atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirects.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/agent/v1/healthz" {
					t.Errorf("unexpected health path: %s", r.URL.Path)
				}
				w.Header().Set("Location", target.URL)
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-response-sentinel"))
			}))
			defer server.Close()
			_, port, err := net.SplitHostPort(server.Listener.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			// A configured public host must not turn the check into an
			// outbound request; only its listen port is retained.
			if healthy := checkHealth(net.JoinHostPort("192.0.2.1", port)); healthy != (status == http.StatusOK) {
				t.Fatalf("unexpected health result for status %d", status)
			}
		})
	}
	if redirects.Load() != 0 {
		t.Fatal("health check followed a redirect")
	}
	if checkHealth("invalid-listen-address") {
		t.Fatal("malformed listen address cannot be healthy")
	}
}
