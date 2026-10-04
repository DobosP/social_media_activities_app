package media_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type inlineDownloadGate struct {
	*media.LocalStore
	mu      sync.Mutex
	entered chan struct{}
	release chan struct{}
	active  int
}

func (g *inlineDownloadGate) OpenRange(ctx context.Context, key string, start, end int64) ([]byte, error) {
	if start == 0 {
		g.mu.Lock()
		g.active++
		g.mu.Unlock()
		defer func() { g.mu.Lock(); g.active--; g.mu.Unlock() }()
		select {
		case g.entered <- struct{}{}:
		default:
		}
		select {
		case <-g.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return g.LocalStore.OpenRange(ctx, key, start, end)
}

func inlineFixture(t *testing.T) (*media.Service, *social.Service, *pgxpool.Pool, *inlineDownloadGate, platform.Actor, int64, int64, string) {
	t.Helper()
	m, s, db, blobs := integratedMedia(t)
	_ = m // retain the full-FK fixture and replace only its test storage/processor.
	gate := &inlineDownloadGate{LocalStore: blobs.LocalStore, entered: make(chan struct{}, 16), release: make(chan struct{})}
	cfg := media.DefaultConfig(t.TempDir())
	cfg.ConcurrentJobs = 2
	p, err := media.NewProcessor(cfg, cleanScanner{}, cleanDocuments{})
	if err != nil {
		t.Fatal(err)
	}
	m = media.NewService(db, p, gate, media.TokenCodec{Key: []byte(strings.Repeat("x", 32))}, s)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.StopInlineVideoProcessing(ctx); err != nil {
			t.Error(err)
		}
	})
	owner := testdb.Actor(t, db, "generated-inline-owner", "adult")
	activity, thread := fixtureThread(t, s, db, owner)
	path := filepath.Join(t.TempDir(), "inline.mp4")
	if err = exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", path).Run(); err != nil {
		t.Fatal(err)
	}
	return m, s, db, gate, owner, activity, thread, path
}

func inlineCounts(t *testing.T, db *pgxpool.Pool) (pending, processing, ready int) {
	t.Helper()
	if err := db.QueryRow(context.Background(), `SELECT count(*) FILTER(WHERE status='pending'),count(*) FILTER(WHERE status='processing'),count(*) FILTER(WHERE status='ready') FROM media_attachment WHERE kind='video'`).Scan(&pending, &processing, &ready); err != nil {
		t.Fatal(err)
	}
	return
}

func TestNativeInlineVideoKickSingleFlightAfterCommitAndBoundedBatch(t *testing.T) {
	m, s, db, gate, owner, activity, thread, path := inlineFixture(t)
	ctx := context.Background()
	lifetime, cancelLife := context.WithCancel(ctx)
	defer cancelLife()
	m.SetInlineVideoProcessing(lifetime, false)
	for i := 0; i < 3; i++ {
		post, err := s.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "Queued synthetic clip"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = m.AttachToPost(ctx, owner, post, path, "inline.mp4", nil); err != nil {
			t.Fatal(err)
		}
	}
	if pending, processing, ready := inlineCounts(t, db); pending != 3 || processing != 0 || ready != 0 {
		t.Fatal("disabled inline mode processed uploads", pending, processing, ready)
	}
	m.SetInlineVideoProcessing(lifetime, true)
	// Enabling the option must not start a scheduler or drain old work.
	if pending, processing, ready := inlineCounts(t, db); pending != 3 || processing != 0 || ready != 0 {
		t.Fatal("configuration started autonomous processing")
	}
	p, err := m.PrepareThreadAttachment(ctx, owner, thread, path, "rollback.mp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Audit = func(context.Context, pgx.Tx, platform.Actor, string, string, any) error {
		return errors.New("synthetic later rollback")
	}
	_, err = s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{}, false, p.Publish)
	p.Finish(ctx, err == nil)
	if err == nil {
		t.Fatal("rollback fixture unexpectedly committed")
	}
	if pending, processing, ready := inlineCounts(t, db); pending != 3 || processing != 0 || ready != 0 {
		t.Fatal("rolled-back upload kicked processing")
	}
	s.Audit = platform.RecordAudit
	commitPrepared := func() {
		t.Helper()
		p, err := m.PrepareThreadAttachment(ctx, owner, thread, path, "commit.mp4", nil)
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{}, false, p.Publish)
		p.Finish(ctx, err == nil)
		if err != nil {
			t.Fatal(err)
		}
	}
	commitPrepared()
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("committed upload did not kick")
	}
	commitPrepared()
	if pending, processing, ready := inlineCounts(t, db); pending != 4 || processing != 1 || ready != 0 {
		t.Fatal("multiple upload kicks escaped single-flight guard", pending, processing, ready)
	}
	close(gate.release)
	deadline := time.Now().Add(15 * time.Second)
	for {
		pending, processing, ready := inlineCounts(t, db)
		if pending == 3 && processing == 0 && ready == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("inline batch did not drain exactly two", pending, processing, ready)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopctx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if err = m.StopInlineVideoProcessing(stopctx); err != nil {
		t.Fatal(err)
	}
}

func TestNativeInlineVideoKickUsesLifetimeAndWaitsOnShutdown(t *testing.T) {
	m, s, db, gate, owner, activity, thread, path := inlineFixture(t)
	lifetime, cancelLife := context.WithCancel(context.Background())
	m.SetInlineVideoProcessing(lifetime, true)
	request, cancelRequest := context.WithCancel(context.Background())
	p, err := m.PrepareThreadAttachment(request, owner, thread, path, "lifetime.mp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.WritePostAttached(request, owner, "activity", activity, social.PostInput{}, false, p.Publish)
	if err != nil {
		p.Finish(request, false)
		t.Fatal(err)
	}
	cancelRequest()
	p.Finish(request, true)
	select {
	case <-gate.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("request cancellation disabled application-owned work")
	}
	cancelLife()
	stopctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	if err = m.StopInlineVideoProcessing(stopctx); err != nil {
		t.Fatal("shutdown did not wait for canceled download", err)
	}
	gate.mu.Lock()
	active := gate.active
	gate.mu.Unlock()
	if active != 0 {
		t.Fatal("media shutdown leaked active storage work")
	}
	if pending, processing, ready := inlineCounts(t, db); pending != 0 || processing != 1 || ready != 0 {
		t.Fatal("canceled video was falsely ready or lost durable retry row", pending, processing, ready)
	}
}
