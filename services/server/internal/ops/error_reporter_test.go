package ops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

const fixtureSentryDSN = "https://fixture-public@example.invalid/1"

type mockErrorTransport struct {
	mu        sync.Mutex
	options   sentry.ClientOptions
	events    []*sentry.Event
	started   chan struct{}
	release   <-chan struct{}
	closed    chan struct{}
	startOnce sync.Once
	closeOnce sync.Once
	flushWait bool
}

func (m *mockErrorTransport) Configure(options sentry.ClientOptions) { m.options = options }
func (m *mockErrorTransport) SendEvent(event *sentry.Event) {
	if m.started != nil {
		m.startOnce.Do(func() { close(m.started) })
	}
	if m.release != nil {
		<-m.release
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}
func (m *mockErrorTransport) Flush(timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return m.FlushWithContext(ctx)
}
func (m *mockErrorTransport) FlushWithContext(ctx context.Context) bool {
	if m.flushWait {
		<-ctx.Done()
	}
	return ctx.Err() == nil
}
func (m *mockErrorTransport) Close() {
	if m.closed != nil {
		m.closeOnce.Do(func() { close(m.closed) })
	}
}
func (m *mockErrorTransport) captured() []*sentry.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*sentry.Event{}, m.events...)
}

func reporterFixture(t *testing.T, transport *mockErrorTransport) *ErrorReporter {
	t.Helper()
	reporter, err := NewErrorReporter(ErrorReporterConfig{DSN: fixtureSentryDSN, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reporter.Shutdown(context.Background()) })
	return reporter
}

func waitReporterClosed(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("capture worker did not release SDK resources")
	}
}

func assertPrivateErrorEvent(t *testing.T, event *sentry.Event, class ErrorClass, method, route string) {
	t.Helper()
	if event.Message != string(class) || event.Level != sentry.LevelError || event.Platform != "go" || event.Tags["method"] != method || event.Tags["route"] != route {
		t.Fatalf("unexpected fixed event fields: %+v", event.Tags)
	}
	if event.Request != nil || !reflect.DeepEqual(event.User, sentry.User{}) || event.Exception != nil || event.Threads != nil || event.Breadcrumbs != nil || event.Contexts != nil || event.Modules != nil || event.Attachments != nil || event.DebugMeta != nil || event.Spans != nil || event.CheckIn != nil || event.Logs != nil || event.Metrics != nil || event.ServerName != "" || event.Release != "" || event.Transaction != "" || event.Logger != "" {
		t.Fatal("event retained fields outside the privacy allowlist")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "fixture-private") || strings.Contains(string(raw), "192.0.2.10") || strings.Contains(string(raw), "person@example.invalid") {
		t.Fatal("event retained private synthetic fixture data")
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{"event_id": true, "timestamp": true, "message": true, "level": true, "platform": true, "sdk": true, "tags": true, "fingerprint": true, "environment": true, "user": true}
	for key := range payload {
		if !allowed[key] {
			t.Fatalf("unexpected outgoing event field: %s", key)
		}
	}
	if user, ok := payload["user"].(map[string]any); ok && len(user) != 0 {
		t.Fatal("serialized user is not empty")
	}
	if len(event.EventID) != 32 || event.Timestamp.IsZero() || !reflect.DeepEqual(event.Fingerprint, []string{string(class), method, route}) {
		t.Fatal("missing generated identity, timestamp or fixed grouping")
	}
}

func TestErrorReporterDisabledDoesNotUseSDKEnvironment(t *testing.T) {
	t.Setenv("SENTRY_DSN", fixtureSentryDSN)
	transport := &mockErrorTransport{}
	reporter, err := NewErrorReporter(ErrorReporterConfig{Transport: transport})
	if err != nil || reporter.Enabled() || reporter.Capture(Startup, "", "startup") || !reporter.Shutdown(context.Background()) {
		t.Fatal("empty explicit DSN did not disable reporting")
	}
	if transport.options.Dsn != "" {
		t.Fatal("disabled reporter initialized SDK transport")
	}
	handler := http.NewServeMux()
	if reporter.Middleware(handler) != handler {
		t.Fatal("disabled middleware changed HTTP handler")
	}
	var absent *ErrorReporter
	if absent.Enabled() || absent.Capture(Panic, "GET", "/") || !absent.Shutdown(context.Background()) {
		t.Fatal("nil reporter is not disabled")
	}
}

func TestErrorReporterRejectsConfigurationWithoutDSNDisclosure(t *testing.T) {
	for name, config := range map[string]ErrorReporterConfig{
		"invalid_dsn": {DSN: "fixture-private"},
		"plain_http":  {DSN: "http://fixture-private@example.invalid/1"},
		"secret_key":  {DSN: "https://fixture-public:fixture-private@example.invalid/1"},
		"query":       {DSN: fixtureSentryDSN + "?token=fixture-private"},
		"empty_query": {DSN: fixtureSentryDSN + "?"},
		"fragment":    {DSN: fixtureSentryDSN + "#fixture-private"},
		"large_dsn":   {DSN: fixtureSentryDSN + strings.Repeat("x", 2048)},
		"queue_large": {DSN: fixtureSentryDSN, QueueSize: maxErrorQueue + 1},
		"queue_small": {DSN: fixtureSentryDSN, QueueSize: -1},
		"flush_large": {DSN: fixtureSentryDSN, FlushTimeout: maxErrorFlush + time.Millisecond},
		"flush_small": {DSN: fixtureSentryDSN, FlushTimeout: -time.Second},
		"environment": {DSN: fixtureSentryDSN, Environment: "person@example.invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateErrorReporterConfig(config); !errors.Is(err, ErrErrorReporterConfig) {
				t.Fatal("validation did not reject invalid config")
			}
			if _, err := NewErrorReporter(config); !errors.Is(err, ErrErrorReporterConfig) || strings.Contains(err.Error(), "fixture-private") {
				t.Fatal("invalid config must return only a constant error")
			}
		})
	}
	if err := ValidateErrorReporterConfig(ErrorReporterConfig{DSN: fixtureSentryDSN, Environment: "staging", QueueSize: maxErrorQueue, FlushTimeout: maxErrorFlush}); err != nil {
		t.Fatal("valid fixed configuration boundary rejected")
	}
}

func TestErrorReporterSDKCollectionAndCaptureAreAllowlisted(t *testing.T) {
	t.Setenv("SENTRY_RELEASE", "fixture-private")
	t.Setenv("SENTRY_ENVIRONMENT", "person@example.invalid")
	transport := &mockErrorTransport{}
	reporter := reporterFixture(t, transport)
	options := transport.options
	collection := options.DataCollection
	if options.Release != "social-native" || options.Environment != "production" || options.ServerName != "social-native" || options.Debug || options.SendDefaultPII || options.AttachStacktrace || options.EnableTracing || options.TracesSampleRate != 0 || !options.DisableClientReports || !options.DisableTelemetryBuffer || options.MaxBreadcrumbs != -1 || len(options.Integrations(nil)) != 0 {
		t.Fatal("SDK runtime metadata/instrumentation options are not fixed")
	}
	if collection == nil || collection.UserInfo.Value || len(collection.HTTPBodies) != 0 || collection.Cookies.Mode != sentry.CollectionOff || collection.QueryParams.Mode != sentry.CollectionOff || collection.HTTPHeaders.Request.Mode != sentry.CollectionOff || collection.HTTPHeaders.Response.Mode != sentry.CollectionOff {
		t.Fatal("automatic request/user collection is enabled")
	}
	if options.HTTPClient == nil || options.HTTPClient.Timeout != defaultErrorFlush || options.HTTPClient.CheckRedirect(nil, nil) != http.ErrUseLastResponse {
		t.Fatal("outbound transport timeout/redirect policy is not bounded")
	}
	if options.BeforeSendTransaction(&sentry.Event{Message: "fixture-private"}, nil) != nil || options.BeforeSendLog(&sentry.Log{}) != nil || options.BeforeSendMetric(&sentry.Metric{}) != nil {
		t.Fatal("non-error telemetry is not disabled")
	}
	if !reporter.Capture(HTTP5xx, "GET", "GET /api/v1/social/activities/{id}/{$}") || reporter.Capture(ErrorClass("fixture-private"), "GET", "/") {
		t.Fatal("fixed event admission failed")
	}
	if !reporter.Shutdown(context.Background()) {
		t.Fatal("mock events did not flush")
	}
	events := transport.captured()
	if len(events) != 1 {
		t.Fatalf("captured %d events", len(events))
	}
	assertPrivateErrorEvent(t, events[0], HTTP5xx, "GET", "/api/social/{route}")
	if events[0].Environment != "production" || reporter.Capture(Startup, "", "startup") {
		t.Fatal("shutdown/environment invariant failed")
	}
}

func TestScrubErrorEventReconstructsAllFields(t *testing.T) {
	event := &sentry.Event{
		Message: string(Panic), Level: sentry.LevelFatal, EventID: "fixture-private",
		Tags:        map[string]string{"method": "fixture-private", "route": "/api/social/person@example.invalid?token=fixture-private", "token": "fixture-private"},
		Request:     &sentry.Request{URL: "https://example.invalid/fixture-private?token=fixture-private", Data: "fixture-private", Cookies: "fixture-private", Headers: map[string]string{"Authorization": "fixture-private"}, Env: map[string]string{"REMOTE_ADDR": "192.0.2.10"}},
		User:        sentry.User{ID: "fixture-private", Email: "person@example.invalid", IPAddress: "192.0.2.10"},
		Exception:   []sentry.Exception{{Type: "fixture-private", Value: "fixture-private"}},
		Threads:     []sentry.Thread{{Name: "fixture-private", Stacktrace: &sentry.Stacktrace{Frames: []sentry.Frame{{Filename: "fixture-private"}}}}},
		Breadcrumbs: []*sentry.Breadcrumb{{Message: "fixture-private"}},
		Contexts:    map[string]sentry.Context{"private": {"body": "fixture-private"}},
		Modules:     map[string]string{"fixture-private": "fixture-private"},
		Attachments: []*sentry.Attachment{{Filename: "fixture-private", Payload: []byte("fixture-private")}},
		Release:     "fixture-private", ServerName: "fixture-private", Environment: "fixture-private", Dist: "fixture-private", Transaction: "fixture-private", Logger: "fixture-private",
		Sdk: sentry.SdkInfo{Name: "fixture-private", Version: "fixture-private"},
	}
	hint := &sentry.EventHint{OriginalException: errors.New("fixture-private"), RecoveredException: "fixture-private", Request: httptest.NewRequest("GET", "/fixture-private", strings.NewReader("fixture-private"))}
	clean := scrubErrorEvent(event, hint)
	assertPrivateErrorEvent(t, clean, Panic, "OTHER", "/api/social/{route}")
	if clean.Environment != "" || clean.Dist != "" || clean.Sdk.Name != "sentry.go" || clean.Sdk.Version != sentry.SDKVersion {
		t.Fatal("unexpected metadata survived reconstruction")
	}
	if scrubErrorEvent(nil, hint) != nil || scrubErrorEvent(&sentry.Event{Message: "fixture-private"}, hint) != nil {
		t.Fatal("unrecognized classes were not dropped")
	}
}

func TestErrorReporterCaptureQueueAndShutdownStayBounded(t *testing.T) {
	release := make(chan struct{})
	transport := &mockErrorTransport{started: make(chan struct{}), release: release, closed: make(chan struct{})}
	reporter, err := NewErrorReporter(ErrorReporterConfig{DSN: fixtureSentryDSN, QueueSize: 2, FlushTimeout: 100 * time.Millisecond, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { close(release); waitReporterClosed(t, transport.closed) })
	if !reporter.Capture(Panic, "GET", "/") {
		t.Fatal("initial record not admitted")
	}
	select {
	case <-transport.started:
	case <-time.After(time.Second):
		t.Fatal("worker did not capture initial event")
	}
	start := time.Now()
	if !reporter.Capture(Panic, "GET", "/") || !reporter.Capture(Panic, "GET", "/") || reporter.Capture(Panic, "GET", "/") {
		t.Fatal("queue did not enforce capacity")
	}
	if time.Since(start) > 50*time.Millisecond {
		t.Fatal("capture waited for blocked transport")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	start = time.Now()
	if reporter.Shutdown(ctx) || time.Since(start) > 100*time.Millisecond || reporter.Capture(Panic, "GET", "/") {
		t.Fatal("shutdown did not honor caller deadline/stop admission")
	}
}

func TestErrorReporterFlushHonorsBudgetAndClosesSDK(t *testing.T) {
	transport := &mockErrorTransport{flushWait: true, closed: make(chan struct{})}
	reporter, err := NewErrorReporter(ErrorReporterConfig{DSN: fixtureSentryDSN, FlushTimeout: 10 * time.Millisecond, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if reporter.Shutdown(context.Background()) || time.Since(start) > 100*time.Millisecond {
		t.Fatal("shutdown did not enforce configured budget")
	}
	waitReporterClosed(t, transport.closed)
}

func TestErrorReporterMiddlewareReportsWithoutRequestOrResponseData(t *testing.T) {
	transport := &mockErrorTransport{}
	reporter := reporterFixture(t, transport)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/social/activities/{id}/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "fixture-private")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("fixture-private"))
	})
	request := httptest.NewRequest("GET", "https://example.invalid/api/social/activities/42/?person=person@example.invalid&token=fixture-private", strings.NewReader("fixture-private"))
	request.RemoteAddr = "192.0.2.10:2345"
	request.Header.Set("Authorization", "Bearer fixture-private")
	request.Header.Set("Cookie", "session=fixture-private")
	response := httptest.NewRecorder()
	reporter.Middleware(mux).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "fixture-private" || response.Header().Get("Set-Cookie") != "fixture-private" {
		t.Fatal("middleware altered HTTP behavior")
	}
	if !reporter.Shutdown(context.Background()) {
		t.Fatal("mock flush failed")
	}
	events := transport.captured()
	if len(events) != 1 {
		t.Fatalf("captured %d events", len(events))
	}
	assertPrivateErrorEvent(t, events[0], HTTP5xx, "GET", "/api/social/{route}")
}

func TestErrorReporterMiddlewarePreservesPanicAndStreaming(t *testing.T) {
	transport := &mockErrorTransport{}
	reporter := reporterFixture(t, transport)
	value := errors.New("fixture-private")
	func() {
		defer func() {
			if recover() != value {
				t.Fatal("middleware replaced swallowed panic value")
			}
		}()
		reporter.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(value) })).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/fixture-private", nil))
	}()
	response := httptest.NewRecorder()
	reporter.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, "data: fixture\n\n")
	})).ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if !response.Flushed || response.Body.String() != "data: fixture\n\n" {
		t.Fatal("middleware changed streaming response")
	}
	if !reporter.Shutdown(context.Background()) {
		t.Fatal("mock flush failed")
	}
	events := transport.captured()
	if len(events) != 1 {
		t.Fatalf("captured %d events", len(events))
	}
	assertPrivateErrorEvent(t, events[0], Panic, "GET", "unmatched")
}

func TestErrorReporterConcurrentCaptureAndShutdown(t *testing.T) {
	transport := &mockErrorTransport{}
	reporter := reporterFixture(t, transport)
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Go(func() {
			for j := 0; j < 100; j++ {
				reporter.Capture(HTTP5xx, "POST", "/api/social/activities/{id}/")
			}
		})
	}
	for i := 0; i < 3; i++ {
		workers.Go(func() { reporter.Shutdown(context.Background()) })
	}
	workers.Wait()
	if reporter.Capture(Startup, "", "startup") {
		t.Fatal("capture accepted after concurrent shutdown")
	}
}
