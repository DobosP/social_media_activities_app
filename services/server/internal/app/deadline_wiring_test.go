package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// http.ResponseController uses these methods before trying Unwrap, so the
// recorder sees every deadline that reaches the connection through
// observedResponse.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	reads, writes []time.Time
}

func (w *deadlineRecorder) SetReadDeadline(t time.Time) error {
	w.reads = append(w.reads, t)
	return nil
}
func (w *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	w.writes = append(w.writes, t)
	return nil
}

// Guards serveHTTP's platform.ExtendDeadlines(w, uploadRead, uploadWrite): it
// must reach the connection for an admitted multipart upload only, after
// authentication and CSRF, sized by the upload class.
func TestNativeUploadDeadlinesReachConnectionOnlyForAdmittedUploads(t *testing.T) {
	if *appDSN == "" {
		t.Skip("explicit generated fixture DSN required")
	}
	db := testdb.New(t, *appDSN, nil)
	a, err := New(context.Background(), db, integrationConfig(t), true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-deadline-owner", "adult")
	session := strings.Repeat("d", 52)
	digest := sha256.Sum256([]byte(session))
	if err = a.Store.CreateSession(ctx, authcore.Session{TokenHash: hex.EncodeToString(digest[:]), UserID: strconv.FormatInt(owner.ID, 10), ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	csrf := strings.Repeat("c", 52)
	upload := func(path string, signedIn bool, formCSRF string) *http.Request {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("csrfmiddlewaretoken", formCSRF)
		part, err := writer.CreateFormFile("image", "fixture.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = part.Write([]byte("synthetic upload bytes")); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "https://app.example"+path, &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		r.Header.Set("Origin", "https://app.example")
		if signedIn {
			r.AddCookie(&http.Cookie{Name: "sessionid", Value: session})
			r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
		}
		return r
	}
	serve := func(r *http.Request) (*deadlineRecorder, time.Time, time.Time) {
		w := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		before := time.Now()
		a.ServeHTTP(w, r)
		return w, before, time.Now()
	}
	sized := func(got []time.Time, want time.Duration, before, after time.Time) bool {
		return len(got) == 1 && !got[0].Before(before.Add(want)) && !got[0].After(after.Add(want))
	}
	for _, item := range []struct {
		name, path  string
		read, write time.Duration
	}{
		{"large media upload", "/api/v1/media/photos/", largeUploadRead, largeUploadWrite},
		{"small image form", "/profile/avatar/", smallUploadRead, smallUploadWrite},
	} {
		w, before, after := serve(upload(item.path, true, csrf))
		if !sized(w.reads, item.read, before, after) || !sized(w.writes, item.write, before, after) {
			t.Fatal(item.name, "admitted upload deadlines not extended to its class", w.Code, w.reads, w.writes)
		}
	}
	if w, _, _ := serve(upload("/api/v1/media/photos/", false, csrf)); len(w.reads)+len(w.writes) != 0 {
		t.Fatal("anonymous upload outlived the server-wide timeouts", w.Code, w.reads, w.writes)
	}
	if w, _, _ := serve(upload("/api/v1/media/photos/", true, strings.Repeat("x", 52))); w.Code != http.StatusForbidden || len(w.reads)+len(w.writes) != 0 {
		t.Fatal("CSRF-refused upload outlived the server-wide timeouts", w.Code, w.reads, w.writes)
	}
	r := httptest.NewRequest("POST", "https://app.example/api/v1/media/photos/", strings.NewReader(`{}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("X-CSRFToken", csrf)
	r.AddCookie(&http.Cookie{Name: "sessionid", Value: session})
	r.AddCookie(&http.Cookie{Name: "csrftoken", Value: csrf})
	if w, _, _ := serve(r); len(w.reads)+len(w.writes) != 0 {
		t.Fatal("non-multipart request outlived the server-wide timeouts", w.Code, w.reads, w.writes)
	}
}
