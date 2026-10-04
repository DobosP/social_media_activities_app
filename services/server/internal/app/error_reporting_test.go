package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/getsentry/sentry-go"
)

type appErrorTransport struct {
	mu         sync.Mutex
	events     []*sentry.Event
	configured bool
	closed     chan struct{}
	closeOnce  sync.Once
}

func (m *appErrorTransport) Configure(sentry.ClientOptions) { m.configured = true }
func (m *appErrorTransport) SendEvent(event *sentry.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}
func (m *appErrorTransport) Flush(time.Duration) bool { return true }
func (m *appErrorTransport) FlushWithContext(ctx context.Context) bool {
	return ctx.Err() == nil
}
func (m *appErrorTransport) Close() { m.closeOnce.Do(func() { close(m.closed) }) }
func (m *appErrorTransport) captured() []*sentry.Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*sentry.Event{}, m.events...)
}

func newAppReporter(t *testing.T) (*ops.ErrorReporter, *appErrorTransport) {
	t.Helper()
	transport := &appErrorTransport{closed: make(chan struct{})}
	reporter, err := ops.NewErrorReporter(ops.ErrorReporterConfig{DSN: "https://fixture-private-dsn@example.invalid/1", Environment: "staging", Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reporter.Shutdown(context.Background()) })
	return reporter, transport
}

func stopAppReporter(t *testing.T, reporter *ops.ErrorReporter, transport *appErrorTransport) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !reporter.Shutdown(ctx) {
		t.Fatal("assembled reporter did not drain mock transport")
	}
	select {
	case <-transport.closed:
	case <-ctx.Done():
		t.Fatal("assembled reporter did not stop transport")
	}
	if reporter.Capture(ops.Startup, "", "startup") || !reporter.Shutdown(ctx) {
		t.Fatal("assembled reporter did not stop admission or allow repeat shutdown")
	}
}

func errorReportingApp(t *testing.T, reporter *ops.ErrorReporter, log *bytes.Buffer) *App {
	t.Helper()
	// No DB is contacted: the short synthetic session value cannot authenticate.
	auth, err := authcore.New(authcore.Config{PublicURL: "https://social.fixture.test"}, accounts.NewStore(nil))
	if err != nil {
		t.Fatal(err)
	}
	return &App{
		Config: Config{AllowedHosts: []string{"social.fixture.test"}, ErrorReporter: reporter, LogWriter: log, LogFormat: "json"},
		Auth:   auth, Mux: http.NewServeMux(),
		// The assembled app replaces the request with a policy-context clone. The
		// reporter must still resolve the registered mux template after handling.
		Catalog: catalog.New(nil),
	}
}

func privateAppRequest() *http.Request {
	request := httptest.NewRequest("GET", "https://social.fixture.test/api/social/activities/fixture-private-user/?token=fixture-private-query&email=person@example.invalid", strings.NewReader("fixture-private-body"))
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("Authorization", "Bearer fixture-private-authorization")
	request.Header.Set("Cookie", "sessionid=fixture-private-cookie; csrftoken=fixture-private-csrf")
	request.Header.Set("X-Forwarded-For", "192.0.2.20")
	request.Header.Set("X-Request-ID", "fixture-private-request-id")
	request.Header.Set("X-CSRFToken", "fixture-private-csrf")
	return request
}

func assertAppErrorPrivacy(t *testing.T, transport *appErrorTransport, class ops.ErrorClass) {
	t.Helper()
	events := transport.captured()
	if len(events) != 1 {
		t.Fatalf("assembled app captured %d events, expected one", len(events))
	}
	event := events[0]
	if event.Message != string(class) || event.Level != sentry.LevelError || event.Environment != "staging" || !reflect.DeepEqual(event.Tags, map[string]string{"method": "GET", "route": "/api/social/{route}"}) {
		t.Fatal("assembled app failed fixed class/method/template contract")
	}
	if event.Request != nil || !reflect.DeepEqual(event.User, sentry.User{}) || event.Exception != nil || event.Threads != nil || event.Breadcrumbs != nil || event.Contexts != nil || event.Modules != nil || event.Attachments != nil || event.ServerName != "" || event.Release != "" {
		t.Fatal("assembled event retained fields outside the allowlist")
	}
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"fixture-private", "person@example.invalid", "social.fixture.test", "example.invalid", "192.0.2.10", "192.0.2.20", "Authorization", "Cookie", "csrftoken", "token="} {
		if bytes.Contains(raw, []byte(private)) {
			t.Fatal("private request/DSN/error material reached mock event")
		}
	}
}

func TestAssembledAppReporterCapturesRecoveredPanicWithoutPrivateFields(t *testing.T) {
	reporter, transport := newAppReporter(t)
	var log bytes.Buffer
	a := errorReportingApp(t, reporter, &log)
	a.Config.RequestLoggingEnabled = true
	called := false
	a.Mux.HandleFunc("GET /api/social/activities/{id}/{$}", func(http.ResponseWriter, *http.Request) {
		called = true
		panic(errors.New("fixture-private-panic person@example.invalid"))
	})
	response := httptest.NewRecorder()
	a.ServeHTTP(response, privateAppRequest())
	if !called || response.Code != http.StatusInternalServerError || response.Body.String() != "{\"detail\":\"Request unavailable.\"}\n" {
		t.Fatal("assembled recovered panic response changed")
	}
	for _, private := range []string{"fixture-private-panic", "fixture-private-body", "fixture-private-query", "fixture-private-authorization", "fixture-private-cookie", "person@example.invalid", "192.0.2.10"} {
		if strings.Contains(log.String(), private) || strings.Contains(response.Body.String(), private) {
			t.Fatal("assembled panic leaked private error/request fields")
		}
	}
	stopAppReporter(t, reporter, transport)
	assertAppErrorPrivacy(t, transport, ops.Panic)
}

func TestAssembledAppReporterCaptures5xxWhenRequestLoggingIsDisabled(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			reporter, transport := newAppReporter(t)
			var log bytes.Buffer
			a := errorReportingApp(t, reporter, &log)
			a.Config.RequestLoggingEnabled = false
			a.Mux.HandleFunc("GET /api/social/activities/{id}/{$}", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Set-Cookie", "fixture-private-response-cookie")
				w.WriteHeader(status)
				_, _ = w.Write([]byte("fixture-private-response-body"))
			})
			response := httptest.NewRecorder()
			a.ServeHTTP(response, privateAppRequest())
			if response.Code != status || response.Body.String() != "fixture-private-response-body" || response.Header().Get("Set-Cookie") != "fixture-private-response-cookie" || log.Len() != 0 {
				t.Fatal("reporter changed 5xx response or enabled request logging")
			}
			stopAppReporter(t, reporter, transport)
			assertAppErrorPrivacy(t, transport, ops.HTTP5xx)
		})
	}
}

func TestAssembledAppReporterCapturesPanicAfterCommittedResponse(t *testing.T) {
	reporter, transport := newAppReporter(t)
	var log bytes.Buffer
	a := errorReportingApp(t, reporter, &log)
	a.Config.RequestLoggingEnabled = false
	a.Mux.HandleFunc("GET /api/social/activities/{id}/{$}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("started"))
		panic("fixture-private-streaming-panic")
	})
	response := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Fatal("assembled app must abort already-committed panic response")
			}
		}()
		a.ServeHTTP(response, privateAppRequest())
	}()
	if response.Code != http.StatusOK || response.Body.String() != "started" || log.Len() != 0 {
		t.Fatal("assembled panic rewrote committed response or enabled logging")
	}
	stopAppReporter(t, reporter, transport)
	assertAppErrorPrivacy(t, transport, ops.Panic)
}

func TestAssembledAppReporterDefaultDisabledIgnoresProcessDSN(t *testing.T) {
	t.Setenv("SENTRY_DSN", "https://fixture-private-process-dsn@example.invalid/1")
	transport := &appErrorTransport{closed: make(chan struct{})}
	reporter, err := ops.NewErrorReporter(ops.ErrorReporterConfig{Transport: transport})
	if err != nil || reporter.Enabled() {
		t.Fatal("empty explicit DSN did not disable assembled reporter")
	}
	var log bytes.Buffer
	a := errorReportingApp(t, reporter, &log)
	a.Mux.HandleFunc("GET /api/social/activities/{id}/{$}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	response := httptest.NewRecorder()
	a.ServeHTTP(response, privateAppRequest())
	if response.Code != http.StatusServiceUnavailable || transport.configured || len(transport.captured()) != 0 || !reporter.Shutdown(context.Background()) || log.Len() != 0 {
		t.Fatal("disabled assembled reporting consulted SDK environment or changed behavior")
	}
}
