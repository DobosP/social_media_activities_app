package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/getsentry/sentry-go"
)

type cliErrorTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
	closed bool
}

func (*cliErrorTransport) Configure(sentry.ClientOptions) {}
func (m *cliErrorTransport) SendEvent(e *sentry.Event) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
}
func (*cliErrorTransport) Flush(time.Duration) bool                  { return true }
func (*cliErrorTransport) FlushWithContext(ctx context.Context) bool { return ctx.Err() == nil }
func (m *cliErrorTransport) Close()                                  { m.mu.Lock(); defer m.mu.Unlock(); m.closed = true }

func TestCLIReportsStartupAndJobFailuresThroughMockAndCloses(t *testing.T) {
	for _, args := range [][]string{{"--migrate"}, {"--due", "--migrate"}} {
		transport := &cliErrorTransport{}
		factory := func(c ops.ErrorReporterConfig) (*ops.ErrorReporter, error) {
			c.Transport = transport
			return ops.NewErrorReporter(c)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		env := fixtureEnv(map[string]string{"DATABASE_URL": "postgres://fixture:synthetic-db-only@127.0.0.1:1/fixture?sslmode=disable", "SENTRY_DSN": "https://fixture-key@example.invalid/1"})
		err := runWithReporter(ctx, args, env, strings.NewReader(""), io.Discard, io.Discard, factory)
		if err == nil {
			t.Fatal("canceled startup unexpectedly succeeded")
		}
		transport.mu.Lock()
		want := string(ops.Startup)
		if len(args) > 1 {
			want = string(ops.JobFailure)
		}
		if len(transport.events) != 1 || transport.events[0].Message != want || !transport.closed {
			t.Fatal("fixed error or SDK shutdown missing")
		}
		if transport.events[0].Request != nil || len(transport.events[0].Contexts) != 0 || len(transport.events[0].Exception) != 0 {
			t.Fatal("private metadata reached transport")
		}
		transport.mu.Unlock()
		if strings.Contains(err.Error(), "synthetic-db-only") || strings.Contains(err.Error(), "fixture-key") {
			t.Fatal("credential material reached startup output")
		}
	}
}

func TestSentryConfigurationValidatesWithoutInitializingSDK(t *testing.T) {
	c, err := configuration(fixtureEnv(map[string]string{"SENTRY_DSN": "https://fixture-key@example.invalid/1", "SENTRY_ENVIRONMENT": "staging", "DJANGO_REQUIRE_SHARED_STATE": "true"}), fixtureOptions())
	if err != nil || c.ErrorReporting.Environment != "staging" || !c.RequireSharedState {
		t.Fatal("qualified optional configuration rejected", err)
	}
	for _, entry := range []struct{ name, value string }{{"SENTRY_DSN", "https://fixture-key:legacy-secret@example.invalid/1"}, {"SENTRY_DSN", "https://fixture-key@example.invalid/1?private=value"}, {"SENTRY_ENVIRONMENT", "private-user-label"}, {"SENTRY_TRACES_SAMPLE_RATE", "0.1"}, {"SENTRY_TRACES_SAMPLE_RATE", "NaN"}, {"SENTRY_TRACES_SAMPLE_RATE", ""}} {
		_, err := configuration(fixtureEnv(map[string]string{entry.name: entry.value}), fixtureOptions(), func(name string) bool { return name == entry.name })
		if err == nil || !strings.Contains(err.Error(), entry.name) || entry.value != "" && strings.Contains(err.Error(), entry.value) {
			t.Fatal("optional telemetry rejected without named private error", entry.name)
		}
	}
}
