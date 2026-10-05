package app

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestUploadDeadlineClassesFollowBodyCap(t *testing.T) {
	multipart := "multipart/form-data; boundary=fixture"
	for _, item := range []struct {
		method, path, mime string
		read, write        time.Duration
		large              bool
	}{
		{"POST", "/api/v1/media/photos/", multipart, largeUploadRead, largeUploadWrite, true},
		{"PUT", "/api/media/activity-covers/4/", multipart, largeUploadRead, largeUploadWrite, true},
		{"POST", "/activities/12/post/", multipart, largeUploadRead, largeUploadWrite, true},
		{"POST", "/groups/3/post/", multipart, largeUploadRead, largeUploadWrite, true},
		{"POST", "/settings/avatar/", multipart, smallUploadRead, smallUploadWrite, false},
		{"POST", "/api/v1/media/photos/", "application/json", 0, 0, false},
		{"GET", "/api/v1/media/photos/", multipart, 0, 0, false},
	} {
		r := httptest.NewRequest(item.method, item.path, nil)
		r.Header.Set("Content-Type", item.mime)
		read, write, large := uploadDeadlines(r)
		if read != item.read || write != item.write || large != item.large {
			t.Fatal("upload deadline class", item.method, item.path, item.mime, read, write, large)
		}
	}
	if largeUploadWrite <= largeUploadRead || smallUploadWrite <= smallUploadRead {
		t.Fatal("write budget must cover the body read plus processing")
	}
}

// observedResponse sits between net/http and every handler. Without Unwrap the
// ResponseController calls would silently keep the short server ReadTimeout.
func TestObservedResponseCarriesExtendedUploadDeadline(t *testing.T) {
	type outcome struct {
		n   int64
		err error
	}
	results := make(chan outcome, 1)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := &observedResponse{ResponseWriter: w, bodyBearing: true}
		platform.ExtendDeadlines(response, 5*time.Second, 5*time.Second)
		n, err := io.Copy(io.Discard, r.Body)
		results <- outcome{n, err}
		response.WriteHeader(http.StatusOK)
	}))
	server.Config.ReadTimeout = 300 * time.Millisecond
	server.Config.WriteTimeout = 300 * time.Millisecond
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	t.Cleanup(server.Close)
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
	client := server.Client()
	client.Timeout = 10 * time.Second
	response, err := client.Post(server.URL, "multipart/form-data; boundary=fixture", reader)
	if err != nil {
		t.Fatal("slow upload cut by the server-wide timeout", err)
	}
	_ = response.Body.Close()
	got := <-results
	if response.StatusCode != http.StatusOK || got.err != nil || got.n != 6*1024 {
		t.Fatal("observed response dropped the extended deadline", response.StatusCode, got.n, got.err)
	}
}
