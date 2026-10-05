package platform

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBusyFailureIsRetryable503(t *testing.T) {
	w := httptest.NewRecorder()
	Fail(w, fmt.Errorf("codec queue: %w", ErrBusy))
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatal("busy admission is not a retryable 503", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	Fail(w, errors.New("opaque failure"))
	if w.Code != 503 || w.Header().Get("Retry-After") != "" {
		t.Fatal("generic failure gained a retry promise", w.Code, w.Header())
	}
}

func TestTransferWriteTimeoutFloorAndCap(t *testing.T) {
	cases := []struct {
		size int64
		want time.Duration
	}{{-1, 30 * time.Second}, {0, 30 * time.Second}, {60 << 16, 90 * time.Second}, {80 << 20, 1310 * time.Second}, {1 << 62, 30 * time.Minute}}
	for _, item := range cases {
		if got := TransferWriteTimeout(item.size); got != item.want {
			t.Fatal("transfer write timeout", item.size, got, item.want)
		}
	}
}

func TestExtendDeadlinesToleratesUnsupportedWriter(t *testing.T) {
	// A recorder has no connection; the server defaults simply remain.
	ExtendDeadlines(httptest.NewRecorder(), time.Minute, time.Minute)
}
