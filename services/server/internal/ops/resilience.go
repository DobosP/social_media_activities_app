package ops

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

var ErrProviderUnavailable = errors.New("provider temporarily unavailable")

type HTTPStatusError struct{ Status int }

func (e *HTTPStatusError) Error() string { return "provider rejected request" }

type RequestOptions struct {
	MaxAttempts      int
	Timeout, Backoff time.Duration
	RetryStatuses    []int
	RetryTimeouts    bool
	BreakerKey       string
	MaxBodyBytes     int64
}
type Resilient struct {
	Client   *http.Client
	mu       sync.Mutex
	breakers map[string]*CircuitBreaker
}

func NewResilient(client *http.Client) *Resilient {
	if client == nil {
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	return &Resilient{Client: client, breakers: map[string]*CircuitBreaker{}}
}

type CircuitBreaker struct {
	mu                            sync.Mutex
	Threshold                     int
	Cooldown                      time.Duration
	SuccessThreshold, HalfOpenMax int
	Now                           func() time.Time
	state                         string
	failures, successes, probes   int
	until                         time.Time
}

func NewCircuitBreaker(threshold int, cooldown time.Duration) *CircuitBreaker {
	return &CircuitBreaker{Threshold: max(1, threshold), Cooldown: cooldown, SuccessThreshold: 1, HalfOpenMax: 1, Now: time.Now, state: "closed"}
}
func (b *CircuitBreaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "open" {
		if b.Now().Before(b.until) {
			return false
		}
		b.state = "half_open"
		b.successes = 0
		b.probes = 0
	}
	if b.state == "half_open" {
		if b.probes >= max(1, b.HalfOpenMax) {
			return false
		}
		b.probes++
	}
	return true
}
func (b *CircuitBreaker) Success() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "half_open" {
		b.probes = max(0, b.probes-1)
		b.successes++
		if b.successes >= max(1, b.SuccessThreshold) {
			b.reset()
		}
	} else {
		b.failures = 0
	}
}
func (b *CircuitBreaker) Failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == "half_open" {
		b.probes = max(0, b.probes-1)
		b.open()
	} else {
		b.failures++
		if b.failures >= b.Threshold {
			b.open()
		}
	}
}
func (b *CircuitBreaker) Reset()        { b.mu.Lock(); defer b.mu.Unlock(); b.reset() }
func (b *CircuitBreaker) State() string { b.mu.Lock(); defer b.mu.Unlock(); return b.state }
func (b *CircuitBreaker) reset() {
	b.state = "closed"
	b.failures = 0
	b.successes = 0
	b.probes = 0
	b.until = time.Time{}
}
func (b *CircuitBreaker) open() {
	b.state = "open"
	b.until = b.Now().Add(b.Cooldown)
	b.successes = 0
	b.probes = 0
}
func (r *Resilient) breaker(key string) (*CircuitBreaker, error) {
	if key == "" {
		return nil, nil
	}
	if len(key) > 128 {
		return nil, ErrProviderUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if b := r.breakers[key]; b != nil {
		return b, nil
	}
	if len(r.breakers) >= 128 {
		return nil, ErrProviderUnavailable
	}
	b := NewCircuitBreaker(5, 30*time.Second)
	r.breakers[key] = b
	return b, nil
}

func (r *Resilient) Do(ctx context.Context, request *http.Request, o RequestOptions) (*http.Response, error) {
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	if o.MaxAttempts < 1 || o.MaxAttempts > 5 {
		return nil, ErrProviderUnavailable
	}
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}
	if o.Backoff == 0 {
		o.Backoff = 500 * time.Millisecond
	}
	if o.MaxBodyBytes == 0 {
		o.MaxBodyBytes = 2 << 20
	}
	if o.MaxBodyBytes < 1 || o.MaxBodyBytes > 16<<20 {
		return nil, ErrProviderUnavailable
	}
	if o.RetryStatuses == nil {
		o.RetryStatuses = []int{500, 502, 503, 504}
	}
	idempotent := request.Method == "GET" || request.Method == "HEAD" || request.Method == "PUT" || request.Method == "DELETE" || request.Method == "OPTIONS" || request.Header.Get("Idempotency-Key") != ""
	b, err := r.breaker(o.BreakerKey)
	if err != nil {
		return nil, err
	}
	if b != nil && !b.Allow() {
		return nil, ErrProviderUnavailable
	}
	success := false
	defer func() {
		if b != nil {
			if success {
				b.Success()
			} else {
				b.Failure()
			}
		}
	}()
	for attempt := 1; attempt <= o.MaxAttempts; attempt++ {
		attemptCtx, cancel := context.WithTimeout(ctx, o.Timeout)
		req := request.Clone(attemptCtx)
		if attempt > 1 && request.Body != nil {
			if request.GetBody == nil {
				cancel()
				return nil, ErrProviderUnavailable
			}
			req.Body, err = request.GetBody()
			if err != nil {
				cancel()
				return nil, ErrProviderUnavailable
			}
		}
		response, callErr := r.Client.Do(req)
		retry := false
		if callErr != nil {
			var op *net.OpError
			dial := errors.As(callErr, &op) && op.Op == "dial"
			var timeout net.Error
			timed := errors.As(callErr, &timeout) && timeout.Timeout()
			retry = dial || (idempotent && o.RetryTimeouts && timed)
		} else {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, o.MaxBodyBytes+1))
			_ = response.Body.Close()
			response.Body = io.NopCloser(bytes.NewReader(body))
			if readErr != nil || int64(len(body)) > o.MaxBodyBytes {
				retry = idempotent && o.RetryTimeouts && readErr != nil
				callErr = ErrProviderUnavailable
			} else if response.StatusCode >= 400 && response.StatusCode < 500 {
				success = true
				cancel()
				return response, &HTTPStatusError{response.StatusCode}
			} else if response.StatusCode >= 500 {
				for _, status := range o.RetryStatuses {
					if response.StatusCode == status && idempotent {
						retry = true
					}
				}
				callErr = ErrProviderUnavailable
			} else {
				success = true
				cancel()
				return response, nil
			}
		}
		cancel()
		if ctx.Err() != nil {
			return nil, ErrProviderUnavailable
		}
		if !retry || attempt == o.MaxAttempts {
			return nil, ErrProviderUnavailable
		}
		timer := time.NewTimer(o.Backoff * time.Duration(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ErrProviderUnavailable
		case <-timer.C:
		}
	}
	return nil, ErrProviderUnavailable
}
