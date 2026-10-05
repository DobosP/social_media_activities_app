package media

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// No codec runs here: an absent source is rejected at staging, so each call
// proves only which semaphore it waited on before reaching the file.
func slotProcessor(t *testing.T, wait time.Duration) (*Processor, string) {
	t.Helper()
	cfg := DefaultConfig(t.TempDir())
	cfg.ImageQueueWait = wait
	p, err := NewProcessor(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p, filepath.Join(t.TempDir(), "absent-source")
}

func TestVideoSlotsNeverBlockImageOrPDFWork(t *testing.T) {
	p, absent := slotProcessor(t, time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < cap(p.videoJobs); i++ {
		p.videoJobs <- struct{}{} // every transcode slot is busy for minutes.
	}
	if _, err := p.ProcessImage(ctx, absent); !errors.Is(err, ErrRejected) {
		t.Fatal("image upload waited behind video transcoding", err)
	}
	if _, err := p.ProcessPDF(ctx, absent); !errors.Is(err, ErrRejected) {
		t.Fatal("PDF upload waited behind video transcoding", err)
	}
	for i := 0; i < cap(p.videoJobs); i++ {
		<-p.videoJobs
	}
	for i := 0; i < cap(p.jobs); i++ {
		p.jobs <- struct{}{}
	}
	if _, err := p.ProcessVideo(ctx, absent); !errors.Is(err, ErrRejected) {
		t.Fatal("video work took an image/PDF slot", err)
	}
	if len(p.jobs) != cap(p.jobs) || len(p.videoJobs) != 0 {
		t.Fatal("codec slot leaked", len(p.jobs), len(p.videoJobs))
	}
}

func TestFullImageQueueIsBoundedBusyRefusal(t *testing.T) {
	p, absent := slotProcessor(t, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 0; i < cap(p.jobs); i++ {
		p.jobs <- struct{}{}
	}
	for name, process := range map[string]func(context.Context, string) (Manifest, error){"image": p.ProcessImage, "pdf": p.ProcessPDF} {
		started := time.Now()
		_, err := process(ctx, absent)
		if !errors.Is(err, ErrBusy) || !errors.Is(err, platform.ErrBusy) || ctx.Err() != nil {
			t.Fatal(name, "full queue was not a bounded busy refusal", err)
		}
		if elapsed := time.Since(started); elapsed > 2*time.Second {
			t.Fatal(name, "busy refusal waited past its bound", elapsed)
		}
	}
	if len(p.jobs) != cap(p.jobs) {
		t.Fatal("busy refusal changed slot ownership", len(p.jobs))
	}
	<-p.jobs
	if _, err := p.ProcessImage(ctx, absent); !errors.Is(err, ErrRejected) {
		t.Fatal("freed slot did not admit image work", err)
	}
	if len(p.jobs) != cap(p.jobs)-1 {
		t.Fatal("admitted work leaked its slot", len(p.jobs))
	}
}

func TestBusyCodecQueueIsRetryable503(t *testing.T) {
	for _, err := range []error{ErrBusy, fmt.Errorf("thread attachment: %w", ErrBusy)} {
		w := httptest.NewRecorder()
		mediaFail(w, err)
		if w.Code != 503 || w.Header().Get("Retry-After") == "" {
			t.Fatal("busy codec queue is not a retryable 503", w.Code, w.Header())
		}
	}
	w := httptest.NewRecorder()
	mediaFail(w, ErrProcessing)
	if w.Code != 503 || w.Header().Get("Retry-After") != "" {
		t.Fatal("processing failure gained a retry promise", w.Code, w.Header())
	}
}
