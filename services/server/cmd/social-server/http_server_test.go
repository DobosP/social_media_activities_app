package main

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestHTTPServerKeepsShortServerWideTimeouts(t *testing.T) {
	base := func(net.Listener) context.Context { return context.Background() }
	s := newHTTPServer("127.0.0.1:0", http.NotFoundHandler(), base)
	if s.Addr != "127.0.0.1:0" || s.BaseContext == nil || s.ReadHeaderTimeout != 5*time.Second || s.ReadTimeout != 30*time.Second || s.WriteTimeout != 30*time.Second || s.IdleTimeout != 60*time.Second || s.MaxHeaderBytes != 32<<10 {
		t.Fatal("server-wide limits changed", s.ReadHeaderTimeout, s.ReadTimeout, s.WriteTimeout, s.IdleTimeout, s.MaxHeaderBytes)
	}
}

// throttledUpload sends 6 KiB over ~900 ms, longer than the scaled 300 ms
// server ReadTimeout and well inside the handler's extension.
func throttledUpload() io.Reader {
	reader, writer := io.Pipe()
	go func() {
		for i := 0; i < 6; i++ {
			time.Sleep(150 * time.Millisecond)
			if _, err := writer.Write([]byte(strings.Repeat("u", 1024))); err != nil {
				return
			}
		}
		_ = writer.Close()
	}()
	return reader
}

func TestExtendedDeadlineCarriesSlowUploadPastServerReadTimeout(t *testing.T) {
	type outcome struct {
		n   int64
		err error
	}
	results := make(chan outcome, 2)
	read := func(extend bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if extend {
				platform.ExtendDeadlines(w, 5*time.Second, 5*time.Second)
			}
			n, err := io.Copy(io.Discard, r.Body)
			results <- outcome{n, err}
			if err != nil {
				http.Error(w, "upload incomplete", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("POST /extended", read(true))
	mux.Handle("POST /default", read(false))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	// The production constructor with only the durations scaled down.
	server := newHTTPServerWithTimeouts(listener.Addr().String(), mux, nil, httpTimeouts{header: time.Second, read: 300 * time.Millisecond, write: 300 * time.Millisecond, idle: time.Second})
	server.ErrorLog = log.New(io.Discard, "", 0)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
	origin := "http://" + listener.Addr().String()
	wait := func() outcome {
		t.Helper()
		select {
		case got := <-results:
			return got
		case <-time.After(10 * time.Second):
			t.Fatal("handler never finished reading")
		}
		return outcome{}
	}

	response, err := client.Post(origin+"/extended", "multipart/form-data; boundary=fixture", throttledUpload())
	if err != nil {
		t.Fatal("extended upload failed", err)
	}
	_ = response.Body.Close()
	if got := wait(); response.StatusCode != http.StatusOK || got.err != nil || got.n != 6*1024 {
		t.Fatal("extended upload cut by the server-wide ReadTimeout", response.StatusCode, got.n, got.err)
	}

	// Negative control: the same body without an extension is what every
	// upload hit under the old whole-request ReadTimeout.
	response, err = client.Post(origin+"/default", "multipart/form-data; boundary=fixture", throttledUpload())
	if err == nil {
		_ = response.Body.Close()
	}
	if got := wait(); got.err == nil || got.n == 6*1024 || err == nil && response.StatusCode == http.StatusOK {
		t.Fatal("throttled body outlived the server-wide ReadTimeout without an extension", got.n, got.err)
	}
}
