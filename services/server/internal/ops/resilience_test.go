package ops

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNonIdempotentPostNeverRetriesServerFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	client := NewResilient(nil)
	req, _ := http.NewRequest("POST", server.URL, strings.NewReader("generated-booking"))
	_, err := client.Do(context.Background(), req, RequestOptions{Backoff: time.Nanosecond, RetryTimeouts: true})
	if !errors.Is(err, ErrProviderUnavailable) || calls.Load() != 1 {
		t.Fatal("duplicate non-idempotent request", calls.Load(), err)
	}
}
func TestIdempotencyKeyRetriesAndPermanentStatusDoesNotTrip(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()
	client := NewResilient(nil)
	req, _ := http.NewRequest("POST", server.URL, strings.NewReader("generated-payment"))
	req.Header.Set("Idempotency-Key", "generated-test-key")
	out, err := client.Do(context.Background(), req, RequestOptions{Backoff: time.Nanosecond, BreakerKey: "fixture"})
	if err != nil || out.StatusCode != 200 || calls.Load() != 3 {
		t.Fatal(calls.Load(), err)
	}
}
func TestBreakerSingleProbeAfterCooldown(t *testing.T) {
	now := time.Now()
	breaker := NewCircuitBreaker(2, time.Minute)
	breaker.Now = func() time.Time { return now }
	for i := 0; i < 2; i++ {
		if !breaker.Allow() {
			t.Fatal("closed denied")
		}
		breaker.Failure()
	}
	if breaker.Allow() {
		t.Fatal("open admitted")
	}
	now = now.Add(time.Minute)
	var accepted atomic.Int32
	var wait sync.WaitGroup
	for i := 0; i < 20; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if breaker.Allow() {
				accepted.Add(1)
			}
		}()
	}
	wait.Wait()
	if accepted.Load() != 1 {
		t.Fatal("probe herd", accepted.Load())
	}
	breaker.Success()
	if breaker.State() != "closed" {
		t.Fatal("successful probe did not close")
	}
}
