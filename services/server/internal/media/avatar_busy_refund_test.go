package media

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// A profile-photo upload refused because the codec queue is full did no image
// work, so it must not spend one of the hourly avatar attempts. A real
// rejection still counts.
func TestPostgresBusyAvatarUploadSpendsNoAttempt(t *testing.T) {
	dsnFlag := flag.Lookup("media-test-dsn")
	if dsnFlag == nil || dsnFlag.Value.String() == "" {
		t.Skip("explicit disposable media-test-dsn not supplied")
	}
	db := testdb.New(t, dsnFlag.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	owner := testdb.Actor(t, db, "busy-avatar-owner", "adult")
	store, err := NewLocalStore(filepath.Join(t.TempDir(), "private-blobs"))
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig(t.TempDir())
	cfg.ImageQueueWait = 50 * time.Millisecond
	processor, err := NewProcessor(cfg, cleanScanner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db, processor, store, TokenCodec{Key: []byte(strings.Repeat("s", 48))}, social.New(db, platform.RecordAudit))
	upload := filepath.Join(t.TempDir(), "avatar.bin")
	if err := os.WriteFile(upload, []byte("opaque synthetic avatar bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	attempts := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM media_go_avatarattempt WHERE user_id=$1`, owner.ID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i := 0; i < cap(processor.jobs); i++ {
		processor.jobs <- struct{}{}
	}
	if _, err := service.UploadPhoto(ctx, owner, "profile", 0, upload); !errors.Is(err, ErrBusy) {
		t.Fatal("full codec queue was not a busy refusal", err)
	}
	if n := attempts(); n != 0 {
		t.Fatal("busy refusal spent an avatar attempt", n)
	}
	for i := 0; i < cap(processor.jobs); i++ {
		<-processor.jobs
	}
	if _, err := service.UploadPhoto(ctx, owner, "profile", 0, upload); err == nil || errors.Is(err, ErrBusy) {
		t.Fatal("opaque bytes were not rejected by the codec", err)
	}
	if n := attempts(); n != 1 {
		t.Fatal("a real rejection did not count as an avatar attempt", n)
	}
}
