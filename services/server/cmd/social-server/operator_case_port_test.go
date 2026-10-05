package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
)

type operatorCaseTransport func(*http.Request) (*http.Response, error)

func (f operatorCaseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type operatorCaseReadTimeout struct{}

func (operatorCaseReadTimeout) Error() string   { return "synthetic timeout" }
func (operatorCaseReadTimeout) Timeout() bool   { return true }
func (operatorCaseReadTimeout) Temporary() bool { return true }

// Independent exact counters/errors replace the mocked requests.request tests;
// the mock is a native RoundTripper and makes no network/provider calls.
func TestOperatorCaseHTTPRetryAndPermanentFailureContracts(t *testing.T) {
	cases := []struct {
		name, method                       string
		statuses                           []int
		transportError                     error
		maxAttempts, wantCalls, wantStatus int
		wantUnavailable, wantHTTP          bool
	}{
		{"retries_5xx_then_succeeds", "GET", []int{503, 200}, nil, 3, 2, 200, false, false},
		{"exhausts_to_provider_unavailable", "GET", []int{503, 503}, nil, 2, 2, 0, true, false},
		{"non_idempotent_post_does_not_retry_5xx", "POST", []int{503}, nil, 3, 1, 0, true, false},
		{"connection_error_is_retried", "POST", []int{200}, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("synthetic connect")}, 3, 2, 200, false, false},
		{"read_timeout_not_retried_when_disabled", "POST", nil, &net.OpError{Op: "read", Net: "tcp", Err: operatorCaseReadTimeout{}}, 3, 1, 0, true, false},
		{"4xx_reraised_as_http_error", "GET", []int{400}, nil, 3, 1, 400, false, true},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			calls := 0
			client := ops.NewResilient(&http.Client{Transport: operatorCaseTransport(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != item.method {
					t.Fatal("retry changed method")
				}
				if item.transportError != nil && calls == 1 {
					return nil, item.transportError
				}
				index := calls - 1
				if item.transportError != nil {
					index--
				}
				status := item.statuses[min(index, len(item.statuses)-1)]
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
			})})
			req, _ := http.NewRequest(item.method, "https://synthetic.invalid/operator", nil)
			out, err := client.Do(context.Background(), req, ops.RequestOptions{MaxAttempts: item.maxAttempts, Backoff: time.Nanosecond, RetryTimeouts: false, RetryStatuses: []int{503}})
			if calls != item.wantCalls || errors.Is(err, ops.ErrProviderUnavailable) != item.wantUnavailable {
				t.Fatal("retry budget or provider error classification differs")
			}
			var statusError *ops.HTTPStatusError
			if errors.As(err, &statusError) != item.wantHTTP {
				t.Fatal("permanent rejection classified as transient failure")
			}
			if item.wantHTTP && statusError.Status != 400 {
				t.Fatal("permanent status lost")
			}
			if item.wantStatus != 0 && (out == nil || out.StatusCode != item.wantStatus) {
				t.Fatal("native final response differs")
			}
			if out != nil {
				out.Body.Close()
			}
		})
	}
}

func TestOperatorCaseCircuitThresholdAndNoRequestWhenOpen(t *testing.T) {
	var calls atomic.Int32
	client := ops.NewResilient(&http.Client{Transport: operatorCaseTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})})
	for i := 0; i < 5; i++ {
		r, _ := http.NewRequest("GET", "https://synthetic.invalid/closed", nil)
		if _, err := client.Do(context.Background(), r, ops.RequestOptions{MaxAttempts: 1, BreakerKey: "case.breaker"}); !errors.Is(err, ops.ErrProviderUnavailable) {
			t.Fatal("failed provider wasn't counted")
		}
	}
	before := calls.Load()
	r, _ := http.NewRequest("GET", "https://synthetic.invalid/open", nil)
	if _, err := client.Do(context.Background(), r, ops.RequestOptions{MaxAttempts: 1, BreakerKey: "case.breaker"}); !errors.Is(err, ops.ErrProviderUnavailable) || calls.Load() != before || before != 5 {
		t.Fatal("open breaker sent another provider request")
	}
}

func TestOperatorCaseCircuitRecoveryResetAndConcurrency(t *testing.T) {
	t.Run("half_open_one_probe_success", func(t *testing.T) {
		b := ops.NewCircuitBreaker(2, 0)
		b.Failure()
		b.Failure()
		if b.State() != "open" || !b.Allow() || b.State() != "half_open" || b.Allow() {
			t.Fatal("half-open reservation differs")
		}
		b.Success()
		if b.State() != "closed" || !b.Allow() {
			t.Fatal("successful probe did not restore closed")
		}
	})
	t.Run("half_open_failure_refreshes_cooldown", func(t *testing.T) {
		now := time.Now()
		b := ops.NewCircuitBreaker(1, time.Second)
		b.Now = func() time.Time { return now }
		b.Failure()
		now = now.Add(time.Second)
		if !b.Allow() {
			t.Fatal("probe denied after cooldown")
		}
		b.Failure()
		if b.State() != "open" || b.Allow() {
			t.Fatal("failed probe did not reopen fresh cooldown")
		}
	})
	t.Run("success_threshold_consecutive_probes", func(t *testing.T) {
		b := ops.NewCircuitBreaker(1, 0)
		b.SuccessThreshold = 2
		b.Failure()
		if !b.Allow() {
			t.Fatal("first probe denied")
		}
		b.Success()
		if b.State() != "half_open" || !b.Allow() {
			t.Fatal("first success closed too soon or failed to release slot")
		}
		b.Success()
		if b.State() != "closed" {
			t.Fatal("second success did not close")
		}
	})
	t.Run("open_cooldown_then_reset", func(t *testing.T) {
		b := ops.NewCircuitBreaker(1, 999*time.Second)
		b.Failure()
		if b.Allow() {
			t.Fatal("open cooldown admitted request")
		}
		b.Reset()
		if b.State() != "closed" || !b.Allow() {
			t.Fatal("reset did not restore admission")
		}
	})
	t.Run("8000_failures_are_lock_safe", func(t *testing.T) {
		b := ops.NewCircuitBreaker(8001, 0)
		var wait sync.WaitGroup
		for i := 0; i < 8; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				for n := 0; n < 1000; n++ {
					b.Failure()
				}
			}()
		}
		wait.Wait()
		if b.State() != "closed" {
			t.Fatal("more than8000 failures counted")
		}
		b.Failure()
		if b.State() != "open" {
			t.Fatal("concurrent increments lost failures")
		}
	})
	t.Run("one_probe_under24_way_contention", func(t *testing.T) {
		b := ops.NewCircuitBreaker(1, 0)
		b.Failure()
		var accepted atomic.Int32
		var wait sync.WaitGroup
		for i := 0; i < 24; i++ {
			wait.Add(1)
			go func() {
				defer wait.Done()
				if b.Allow() {
					accepted.Add(1)
				}
			}()
		}
		wait.Wait()
		if accepted.Load() != 1 {
			t.Fatal("half-open check/reserve was not atomic")
		}
	})
}

func TestOperatorCaseDueReminderSixHourCLIAdapter(t *testing.T) {
	o, err := parseCLI([]string{"--due", "--reminder-within-hours", "6"}, io.Discard)
	if err != nil || !o.Due || o.ReminderWithinHours != 6 {
		t.Fatal("source six-hour due reminder option lost at native CLI")
	}
	for _, invalid := range [][]string{{"--reminder-within-hours", "6"}, {"--due", "--reminder-within-hours", "0"}} {
		if _, err := parseCLI(invalid, io.Discard); err == nil {
			t.Fatal("invalid reminder mode was silently accepted")
		}
	}
}
