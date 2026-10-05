package web

import (
	"bytes"
	"context"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestBusyPageIsRetryable503UnlessStatusIsExplicit(t *testing.T) {
	w := httptest.NewRecorder()
	page := busyPage(w)
	if _, err := page.Write([]byte("<p>form</p>")); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" || w.Body.String() != "<p>form</p>" {
		t.Fatal("rendered busy form is not a retryable 503", w.Code, w.Header())
	}
	w = httptest.NewRecorder()
	page = busyPage(w)
	http.Redirect(page, httptest.NewRequest("POST", "/form/", nil), "/back/", http.StatusFound)
	if w.Code != http.StatusFound {
		t.Fatal("explicit status was overridden", w.Code)
	}
	if unwrapped, ok := page.(interface{ Unwrap() http.ResponseWriter }); !ok || unwrapped.Unwrap() != w {
		t.Fatal("busy page hides the connection writer")
	}
}

type busyDocuments struct{ entered, release chan struct{} }

func (d busyDocuments) ScanDocument(ctx context.Context, _ string, _ int64) (media.Verdict, error) {
	d.entered <- struct{}{}
	select {
	case <-d.release:
	case <-ctx.Done():
	}
	return media.Verdict{Clean: true}, nil
}

// busyMedia installs a media service whose only image/PDF codec slot stays held
// until cleanup, so every upload in the test meets a full queue after 50 ms.
func busyMedia(t *testing.T, s *Server) {
	t.Helper()
	ctx := context.Background()
	scratch := t.TempDir()
	s.Config.UploadScratch = scratch
	cfg := media.DefaultConfig(scratch)
	cfg.ImageQueueWait = 50 * time.Millisecond
	documents := busyDocuments{entered: make(chan struct{}, 1), release: make(chan struct{})}
	processor, err := media.NewProcessor(cfg, socialCleanScanner{}, documents)
	if err != nil {
		t.Fatal(err)
	}
	store, err := media.NewLocalStore(filepath.Join(scratch, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	s.Media = media.NewService(s.DB, processor, store, media.TokenCodec{Key: bytes.Repeat([]byte("x"), 32)}, s.Social)
	if err = media.EnsureSchema(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(scratch, "held.pdf")
	if err = os.WriteFile(held, []byte("%PDF-1.7\nheld codec slot\n%%EOF"), 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if m, err := processor.ProcessPDF(ctx, held); err == nil {
			_ = m.Cleanup()
		}
	}()
	t.Cleanup(func() {
		close(documents.release)
		<-done
	})
	select {
	case <-documents.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("fixture never held the codec slot")
	}
}

func busyUploadRequest(t *testing.T, a platform.Actor, target, field, filename string, content []byte, values map[string]string, pk int64, fetch bool) *http.Request {
	t.Helper()
	var payload bytes.Buffer
	writer := multipart.NewWriter(&payload)
	for name, value := range values {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", target, &payload)
	r.SetPathValue("pk", fmt.Sprint(pk))
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if fetch {
		r.Header.Set("X-Requested-With", "fetch")
	}
	return platform.WithActor(r, a)
}

func assertMediaBusy(t *testing.T, w *httptest.ResponseRecorder, fetch bool) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "5" {
		t.Fatal("busy codec queue is not a retryable 503", fetch, w.Code, w.Header().Get("Retry-After"), w.Body.String())
	}
	contentType := w.Header().Get("Content-Type")
	if fetch && !strings.HasPrefix(contentType, "application/json") || !fetch && (!strings.HasPrefix(contentType, "text/html") || !strings.Contains(w.Body.String(), mediaBusyMessage)) {
		t.Fatal("busy response is not the fetch JSON or the re-rendered form", fetch, contentType, w.Body.String())
	}
}

func TestPostgresBusyThreadAttachmentRerendersFormOrAnswersFetch(t *testing.T) {
	s, a, place, typ := socialLegacyFixture(t)
	pk := socialLegacyActivity(t, s, a, place, typ, "Busy codec queue fixture")
	busyMedia(t, s)
	pdf := []byte("%PDF-1.7\nbusy queue fixture\n%%EOF")
	for _, fetch := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := busyUploadRequest(t, a, "/activities/1/post/", "attachment", "busy.pdf", pdf, map[string]string{"body": ""}, pk, fetch)
		if !s.SocialAction(w, r, a, "activity_post") {
			t.Fatal("thread post action not handled")
		}
		assertMediaBusy(t, w, fetch)
	}
}

func TestPostgresBusyPlaceCoverRerendersFormOrAnswersFetch(t *testing.T) {
	s, a, place, _ := socialLegacyFixture(t)
	if _, err := s.DB.Exec(context.Background(), `UPDATE accounts_user SET is_staff=true WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	a.IsStaff = true
	busyMedia(t, s)
	for _, fetch := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := busyUploadRequest(t, a, fmt.Sprintf("/places/%d/official-image/", place), "image", "cover.png", []byte("synthetic cover bytes"), map[string]string{"rights_confirmed": "on", "alt_text": "Busy fixture cover"}, place, fetch)
		if !s.PublicAction(w, r, a, "place_official_image") {
			t.Fatal("place cover action not handled")
		}
		assertMediaBusy(t, w, fetch)
	}
}

func TestPostgresBusyAvatarRerendersFormOrAnswersFetch(t *testing.T) {
	s, a := accountWebFixture(t)
	busyMedia(t, s)
	for _, fetch := range []bool{false, true} {
		w := httptest.NewRecorder()
		r := busyUploadRequest(t, a, "/profile/avatar/", "image", "avatar.png", []byte("synthetic avatar bytes"), nil, 0, fetch)
		if !s.AccountAction(w, r, a, "avatar_upload") {
			t.Fatal("avatar action not handled")
		}
		assertMediaBusy(t, w, fetch)
	}
}

type snapshotDeadlineRecorder struct {
	*httptest.ResponseRecorder
	reads, writes []time.Time
}

func (w *snapshotDeadlineRecorder) SetReadDeadline(t time.Time) error {
	w.reads = append(w.reads, t)
	return nil
}
func (w *snapshotDeadlineRecorder) SetWriteDeadline(t time.Time) error {
	w.writes = append(w.writes, t)
	return nil
}

// Guards platform.ExtendDeadlines in PublicDownload's open_data_snapshot case.
func TestOpenDataSnapshotSizesWriteDeadlineToFile(t *testing.T) {
	root := t.TempDir()
	snapshot := bytes.Repeat([]byte(" "), 1<<20)
	if err := os.WriteFile(filepath.Join(root, "places.json"), snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	s := &Server{Config: Config{PublicURL: "https://fixture.local", SnapshotDir: root}}
	r := httptest.NewRequest("GET", "/open-data/snapshot/places.json", nil)
	r.SetPathValue("name", "places.json")
	w := &snapshotDeadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	before := time.Now()
	if !s.PublicDownload(w, r, platform.Actor{}, "open_data_snapshot") {
		t.Fatal("snapshot download not handled")
	}
	after := time.Now()
	want := platform.TransferWriteTimeout(int64(len(snapshot)))
	if want == platform.TransferWriteTimeout(0) {
		t.Fatal("fixture too small to distinguish a size-based deadline")
	}
	if w.Code != 200 || w.Body.Len() != len(snapshot) || len(w.reads) != 0 || len(w.writes) != 1 || w.writes[0].Before(before.Add(want)) || w.writes[0].After(after.Add(want)) {
		t.Fatal("snapshot write deadline is not sized to the file", w.Code, w.reads, w.writes, want)
	}
}
