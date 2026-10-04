package ops

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/getsentry/sentry-go"
)

// ErrorClass contains fixed operational categories, never raw error or panic text.
type ErrorClass string

const (
	HTTP5xx    ErrorClass = "http_server_error"
	Panic      ErrorClass = "http_panic"
	Startup    ErrorClass = "startup_failure"
	JobFailure ErrorClass = "job_failure"

	defaultErrorQueue = 64
	maxErrorQueue     = 256
	defaultErrorFlush = 2 * time.Second
	maxErrorFlush     = 5 * time.Second
)

var ErrErrorReporterConfig = errors.New("invalid error reporter configuration")

type ErrorReporterConfig struct {
	DSN          string
	Environment  string
	QueueSize    int
	FlushTimeout time.Duration
	// Transport supports isolated tests. Production callers leave it nil.
	Transport sentry.Transport
}

type errorRecord struct {
	class         ErrorClass
	method, route string
}

// ErrorReporter owns one bounded queue and one capture worker. SDK globals,
// request instrumentation, traces, logs and raw exception capture are not used.
type ErrorReporter struct {
	client       *sentry.Client
	httpClient   *http.Client
	queue        chan errorRecord
	done, abort  chan struct{}
	abortOnce    sync.Once
	flushTimeout time.Duration
	mu           sync.Mutex
	closed       bool
	shutdownCtx  context.Context
	flushed      atomic.Bool
}

func errorReporterDefaults(config ErrorReporterConfig) (ErrorReporterConfig, error) {
	if config.Environment == "" {
		config.Environment = "production"
	}
	switch config.Environment {
	case "production", "staging", "development":
	default:
		return config, ErrErrorReporterConfig
	}
	if config.QueueSize == 0 {
		config.QueueSize = defaultErrorQueue
	}
	if config.FlushTimeout == 0 {
		config.FlushTimeout = defaultErrorFlush
	}
	if config.QueueSize < 1 || config.QueueSize > maxErrorQueue || config.FlushTimeout < time.Millisecond || config.FlushTimeout > maxErrorFlush {
		return config, ErrErrorReporterConfig
	}
	if config.DSN != "" {
		if len(config.DSN) > 2048 {
			return config, ErrErrorReporterConfig
		}
		parsed, err := url.Parse(config.DSN)
		if err != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
			return config, ErrErrorReporterConfig
		}
		// Legacy secret-key DSNs are serialized into envelope metadata by the SDK.
		// Modern public-key DSNs are sufficient; do not export a second credential.
		if parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword {
				return config, ErrErrorReporterConfig
			}
		}
		dsn, err := sentry.NewDsn(config.DSN)
		if err != nil || dsn.GetScheme() != "https" {
			return config, ErrErrorReporterConfig
		}
	}
	return config, nil
}

// ValidateErrorReporterConfig checks policy without starting SDK workers or I/O.
// Its error never includes any part of the supplied DSN.
func ValidateErrorReporterConfig(config ErrorReporterConfig) error {
	_, err := errorReporterDefaults(config)
	return err
}

func NewErrorReporter(config ErrorReporterConfig) (*ErrorReporter, error) {
	config, err := errorReporterDefaults(config)
	if err != nil {
		return nil, err
	}
	// Do not initialize the SDK for an empty DSN: NewClient otherwise falls back
	// to the process SENTRY_DSN and starts background workers even when disabled.
	if config.DSN == "" {
		return &ErrorReporter{}, nil
	}
	transport := config.Transport
	if transport == nil {
		async := sentry.NewHTTPTransport()
		async.BufferSize = config.QueueSize
		async.Timeout = defaultErrorFlush
		transport = async
	}
	httpClient := &http.Client{
		Timeout: defaultErrorFlush,
		// A fixed client also prevents SENTRYGODEBUG from adding HTTP dumps.
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	client, err := sentry.NewClient(sentry.ClientOptions{
		Dsn: config.DSN, Transport: transport,
		// Fixed values prevent SDK fallbacks to environment/git/hostname data.
		Release: "social-native", Environment: config.Environment, ServerName: "social-native",
		HTTPClient: httpClient,
		SampleRate: 1, MaxBreadcrumbs: -1, DisableClientReports: true,
		DisableTelemetryBuffer: true,
		Integrations:           func([]sentry.Integration) []sentry.Integration { return nil },
		DataCollection: &sentry.DataCollection{
			UserInfo: sentry.Set(false), HTTPBodies: []sentry.BodyType{},
			Cookies:     &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			QueryParams: &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			HTTPHeaders: &sentry.HeaderCollectionConfig{
				Request:  &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
				Response: &sentry.KeyValueCollectionBehavior{Mode: sentry.CollectionOff},
			},
		},
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			clean := scrubErrorEvent(event, hint)
			if clean != nil {
				clean.Environment = config.Environment
			}
			return clean
		},
		BeforeSendTransaction: func(*sentry.Event, *sentry.EventHint) *sentry.Event { return nil },
		BeforeSendLog:         func(*sentry.Log) *sentry.Log { return nil },
		BeforeSendMetric:      func(*sentry.Metric) *sentry.Metric { return nil },
	})
	if err != nil {
		// SDK parse errors can contain the DSN; never return them to logs.
		return nil, ErrErrorReporterConfig
	}
	r := &ErrorReporter{client: client, httpClient: httpClient, queue: make(chan errorRecord, config.QueueSize), done: make(chan struct{}), abort: make(chan struct{}), flushTimeout: config.FlushTimeout}
	go r.run()
	return r, nil
}

func validErrorClass(class ErrorClass) bool {
	switch class {
	case HTTP5xx, Panic, Startup, JobFailure:
		return true
	}
	return false
}

func errorMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return method
	}
	return "OTHER"
}

// Only fixed, coarse route templates leave the process. A mux pattern, URL,
// username, token or unexpected future route always maps to one of these values.
func errorRoute(pattern string) string {
	if len(pattern) > 256 {
		return "unmatched"
	}
	if _, path, ok := strings.Cut(pattern, " "); ok {
		pattern = path
	}
	pattern = strings.TrimSuffix(pattern, "{$}")
	if strings.HasPrefix(pattern, "/api/v1/") {
		pattern = "/api/" + strings.TrimPrefix(pattern, "/api/v1/")
	}
	for _, prefix := range []string{"/api/accounts/", "/api/social/", "/api/events/", "/api/places/", "/api/messaging/", "/api/media/", "/api/safety/", "/api/booking/", "/api/donations/", "/api/notifications/", "/api/connections/", "/api/ops/", "/api/discovery/", "/api/recommendations/", "/api/admin/"} {
		if strings.HasPrefix(pattern, prefix) {
			return prefix + "{route}"
		}
	}
	switch pattern {
	case "/healthz", "/readyz", "/metrics", "/api/health", "/api/health/", "/api/ready", "/api/ready/", "/", "/login/", "/register/":
		return pattern
	case "startup", "job":
		return pattern
	}
	if strings.HasPrefix(pattern, "/api/") {
		return "/api/{route}"
	}
	if strings.HasPrefix(pattern, "/") {
		return "/{route}"
	}
	return "unmatched"
}

// Reconstruct instead of redacting: SDK additions, scopes, hints, attachments,
// request metadata, stack frames and raw exception values cannot survive.
func scrubErrorEvent(event *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if event == nil || !validErrorClass(ErrorClass(event.Message)) {
		return nil
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil
	}
	method, route := errorMethod(event.Tags["method"]), errorRoute(event.Tags["route"])
	return &sentry.Event{
		EventID: sentry.EventID(hex.EncodeToString(id[:])), Timestamp: time.Now().UTC(),
		Message: event.Message, Level: sentry.LevelError, Platform: "go",
		Sdk:         sentry.SdkInfo{Name: "sentry.go", Version: sentry.SDKVersion},
		Tags:        map[string]string{"method": method, "route": route},
		Fingerprint: []string{event.Message, method, route},
	}
}

// Capture returns whether the fixed record entered the queue. It never waits
// for network I/O; full queues and captures after shutdown are dropped.
func (r *ErrorReporter) Capture(class ErrorClass, method, muxPattern string) bool {
	if r == nil || r.client == nil || !validErrorClass(class) {
		return false
	}
	record := errorRecord{class: class, method: errorMethod(method), route: errorRoute(muxPattern)}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return false
	}
	select {
	case r.queue <- record:
		return true
	default:
		return false
	}
}

func (r *ErrorReporter) Enabled() bool { return r != nil && r.client != nil }

func (r *ErrorReporter) run() {
	defer close(r.done)
	defer r.httpClient.CloseIdleConnections()
	defer r.client.Close()
	for {
		select {
		case <-r.abort:
			return
		default:
		}
		select {
		case <-r.abort:
			return
		case record, ok := <-r.queue:
			if !ok {
				r.mu.Lock()
				ctx := r.shutdownCtx
				r.mu.Unlock()
				r.flushed.Store(r.client.FlushWithContext(ctx))
				return
			}
			r.client.CaptureEvent(&sentry.Event{Message: string(record.class), Tags: map[string]string{"method": record.method, "route": record.route}}, nil, nil)
		}
	}
}

// Shutdown stops admission, drains the queue and flushes the SDK within the
// smaller of the caller's deadline and the configured (maximum five-second)
// budget. False means draining/flush timed out, not a delivery guarantee.
func (r *ErrorReporter) Shutdown(ctx context.Context) bool {
	if !r.Enabled() {
		return true
	}
	ctx, cancel := context.WithTimeout(ctx, r.flushTimeout)
	defer cancel()
	r.mu.Lock()
	if !r.closed {
		r.closed = true
		r.shutdownCtx = ctx
		close(r.queue)
	}
	r.mu.Unlock()
	select {
	case <-r.done:
		return r.flushed.Load()
	case <-ctx.Done():
		r.abortOnce.Do(func() { close(r.abort) })
		return false
	}
}

type errorResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *errorResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *errorResponseWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 && status != http.StatusSwitchingProtocols {
		w.ResponseWriter.WriteHeader(status)
		return
	}
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *errorResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *errorResponseWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *errorResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
func (w *errorResponseWriter) Push(target string, options *http.PushOptions) error {
	if pusher, ok := w.ResponseWriter.(http.Pusher); ok {
		return pusher.Push(target, options)
	}
	return http.ErrNotSupported
}

// Middleware reports 5xx responses and escaping panics. It re-panics with the
// original value for the application's existing recovery/response contract.
func (r *ErrorReporter) Middleware(next http.Handler) http.Handler {
	if !r.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		observed := &errorResponseWriter{ResponseWriter: w}
		defer func() {
			if value := recover(); value != nil {
				r.Capture(Panic, request.Method, request.Pattern)
				panic(value)
			}
			if observed.status >= 500 {
				r.Capture(HTTP5xx, request.Method, request.Pattern)
			}
		}()
		next.ServeHTTP(observed, request)
	})
}
