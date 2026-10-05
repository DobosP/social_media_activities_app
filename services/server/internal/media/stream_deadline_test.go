package media_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

// http.ResponseController uses these methods before trying Unwrap.
type streamDeadlineRecorder struct {
	*httptest.ResponseRecorder
	reads, writes []time.Time
}

func (w *streamDeadlineRecorder) SetReadDeadline(t time.Time) error {
	w.reads = append(w.reads, t)
	return nil
}
func (w *streamDeadlineRecorder) SetWriteDeadline(t time.Time) error {
	w.writes = append(w.writes, t)
	return nil
}

// Guards stream()'s platform.ExtendDeadlines: proxied bytes get a write
// deadline sized to the bytes actually served, and HEAD gets none.
func TestPostgresStreamSizesWriteDeadlineToServedBytes(t *testing.T) {
	m, s, db, blobs := mediaStore(t)
	ctx := context.Background()
	owner := user(t, db, "stream-deadline-owner", "adult")
	act, _ := activity(t, s, db, owner)
	post, e := s.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic streamed file"}, false)
	if e != nil {
		t.Fatal(e)
	}
	// Over 64 KiB, so its full-size deadline differs from a short range's.
	source := filepath.Join(t.TempDir(), "large.pdf")
	if e = os.WriteFile(source, append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte("0"), 1<<20)...), 0600); e != nil {
		t.Fatal(e)
	}
	att, e := m.AttachToPost(ctx, owner, post, source, "large.pdf", nil)
	if e != nil || att.URL == "" {
		t.Fatal("ready attachment", att, e)
	}
	var key string
	if e = db.QueryRow(ctx, `SELECT storage_key FROM media_attachment WHERE id=$1`, att.ID).Scan(&key); e != nil {
		t.Fatal(e)
	}
	size, e := blobs.Size(ctx, key)
	if e != nil {
		t.Fatal(e)
	}
	full, partial := platform.TransferWriteTimeout(size), platform.TransferWriteTimeout(100)
	if full == partial {
		t.Fatal("fixture too small to distinguish range-sized deadlines", size)
	}
	mux := http.NewServeMux()
	m.Register(mux)
	serve := func(method, byteRange string) (*streamDeadlineRecorder, time.Time, time.Time) {
		r := platform.WithActor(httptest.NewRequest(method, att.URL, nil), owner)
		if byteRange != "" {
			r.Header.Set("Range", byteRange)
		}
		w := &streamDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		before := time.Now()
		mux.ServeHTTP(w, r)
		return w, before, time.Now()
	}
	for _, item := range []struct {
		byteRange string
		status    int
		want      time.Duration
	}{{"", 200, full}, {"bytes=0-99", 206, partial}} {
		w, before, after := serve("GET", item.byteRange)
		if w.Code != item.status || len(w.reads) != 0 || len(w.writes) != 1 || w.writes[0].Before(before.Add(item.want)) || w.writes[0].After(after.Add(item.want)) {
			t.Fatal("stream write deadline not sized to served bytes", item.byteRange, w.Code, w.writes, item.want)
		}
	}
	if w, _, _ := serve("HEAD", ""); w.Code != 200 || len(w.reads)+len(w.writes) != 0 {
		t.Fatal("HEAD extended a deadline without a body", w.Code, w.writes)
	}
}
