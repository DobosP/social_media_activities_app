package media

import (
	"context"
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

// A drain whose deadline is shorter than one stale-lease window would have its
// encode cut and one attempt spent; the third cut erases a valid upload. Such a
// drain takes no claim and leaves the row for a caller that can finish it.
func TestPostgresVideoDrainTakesNoClaimItsDeadlineCannotFinish(t *testing.T) {
	dsnFlag := flag.Lookup("media-test-dsn")
	if dsnFlag == nil || dsnFlag.Value.String() == "" {
		t.Skip("explicit disposable media-test-dsn not supplied")
	}
	db := testdb.New(t, dsnFlag.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	owner := testdb.Actor(t, db, "deadline-guard-owner", "adult")
	place := testdb.Place(t, db, "Synthetic deadline-guard venue", "osm")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic deadline-guard activity", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	post, err := soc.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "Synthetic pending video"}, false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(filepath.Join(t.TempDir(), "private-blobs"))
	if err != nil {
		t.Fatal(err)
	}
	source := []byte("opaque synthetic pending video source")
	input := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(input, source, 0600); err != nil {
		t.Fatal(err)
	}
	key := "attachments/deadline-guard-source.mp4"
	if err := store.Put(ctx, key, input, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	var attachment int64
	if err := db.QueryRow(ctx, `INSERT INTO media_attachment(post_id,uploader_id,kind,storage_key,thumb_storage_key,content_type,byte_size,sha256,original_filename,width,height,exif_stripped,created_at,expires_at,purged_at,duration_seconds,poster_content_type,poster_storage_key,processing_attempts,processing_started_at,source_storage_key,status) VALUES($1,$2,'video','','','video/mp4',$3,$4,'fixture.mp4',0,0,false,now(),NULL,NULL,0,'','',0,NULL,$5,'pending') RETURNING id`, post, owner.ID, len(source), strings.Repeat("a", 64), key).Scan(&attachment); err != nil {
		t.Fatal(err)
	}
	processor, err := NewProcessor(DefaultConfig(t.TempDir()), cleanScanner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db, processor, store, TokenCodec{Key: []byte(strings.Repeat("s", 48))}, soc)
	state := func() (string, int) {
		t.Helper()
		var status string
		var attempts int
		if err := db.QueryRow(ctx, `SELECT status,processing_attempts FROM media_attachment WHERE id=$1`, attachment).Scan(&status, &attempts); err != nil {
			t.Fatal(err)
		}
		return status, attempts
	}
	short, cancel := context.WithTimeout(ctx, service.policy.VideoStaleProcessing-time.Minute)
	defer cancel()
	if processed, err := service.ProcessPendingVideos(short, 2); processed != 0 || err != nil {
		t.Fatal("short-deadline drain did not return cleanly", processed, err)
	}
	if status, attempts := state(); status != "pending" || attempts != 0 {
		t.Fatal("short-deadline drain claimed a video it could not finish", status, attempts)
	}
	if size, err := store.Size(ctx, key); err != nil || size != int64(len(source)) {
		t.Fatal("short-deadline drain touched the source bytes")
	}
	// A deadline that covers a full window still claims: the attempt is spent
	// by this call, whatever the codec then decides about the opaque bytes.
	long, cancelLong := context.WithTimeout(ctx, service.policy.VideoStaleProcessing+time.Minute)
	defer cancelLong()
	_, _ = service.ProcessPendingVideos(long, 1)
	if _, attempts := state(); attempts != 1 {
		t.Fatal("drain with a sufficient deadline did not claim", attempts)
	}
}
