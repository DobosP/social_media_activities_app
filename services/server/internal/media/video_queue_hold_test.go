package media

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
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

// An exhausted stale lease is held with its evidence for operator recovery.
// It is older than the fresh upload, so an oldest-first claim that did not
// fence it would select it on every drain and block all later videos.
func TestExhaustedStaleVideoLeaseHeldWithoutBlockingQueue(t *testing.T) {
	dsnFlag := flag.Lookup("media-test-dsn")
	if dsnFlag == nil || dsnFlag.Value.String() == "" {
		t.Skip("explicit disposable media-test-dsn not supplied")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit", "avifenc"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatal("required qualification codec unavailable", bin)
		}
	}
	db := testdb.New(t, dsnFlag.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	ctx := context.Background()
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	soc := social.New(db, platform.RecordAudit)
	owner := testdb.Actor(t, db, "queue-hold-owner", "adult")
	place := testdb.Place(t, db, "Synthetic queue-hold venue", "osm")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	activity, err := soc.CreateActivity(ctx, owner, social.ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic queue-hold activity", StartsAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	heldPost, err := soc.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "Synthetic held video"}, false)
	if err != nil {
		t.Fatal(err)
	}
	freshPost, err := soc.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "Synthetic fresh video"}, false)
	if err != nil {
		t.Fatal(err)
	}
	retryPost, err := soc.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "Synthetic retried video"}, false)
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore(filepath.Join(t.TempDir(), "private-blobs"))
	if err != nil {
		t.Fatal(err)
	}
	processor, err := NewProcessor(DefaultConfig(t.TempDir()), cleanScanner{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(db, processor, store, TokenCodec{Key: []byte(strings.Repeat("s", 48))}, soc)
	maxAttempts := service.policy.VideoMaxAttempts
	source := []byte("opaque synthetic held video source")
	input := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(input, source, 0600); err != nil {
		t.Fatal(err)
	}
	key := "attachments/queue-hold-source.mp4"
	if err := store.Put(ctx, key, input, "video/mp4"); err != nil {
		t.Fatal(err)
	}
	var held int64
	if err := db.QueryRow(ctx, `INSERT INTO media_attachment(post_id,uploader_id,kind,storage_key,thumb_storage_key,content_type,byte_size,sha256,original_filename,width,height,exif_stripped,created_at,expires_at,purged_at,duration_seconds,poster_content_type,poster_storage_key,processing_attempts,processing_started_at,source_storage_key,status) VALUES($1,$2,'video','','','video/mp4',$3,$4,'fixture.mp4',0,0,false,now()-interval '2 hours',NULL,NULL,0,'','',$6,now()-interval '1 hour',$5,'processing') RETURNING id`, heldPost, owner.ID, len(source), strings.Repeat("a", 64), key, maxAttempts).Scan(&held); err != nil {
		t.Fatal(err)
	}
	codecCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	clip := func(name, colour string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), name)
		if err := exec.CommandContext(codecCtx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c="+colour+":s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", path).Run(); err != nil {
			t.Fatal("synthetic video fixture", err)
		}
		return path
	}
	fresh, err := service.AttachToPost(ctx, owner, freshPost, clip("queue-hold-fresh.mp4", "blue"), "fresh.mp4", nil)
	if err != nil || fresh.Status != "pending" {
		t.Fatal("fresh video admission was not withheld pending processing", err)
	}
	// The fence is exact: a stale lease one attempt below the budget is still
	// reclaimed for its last attempt.
	retry, err := service.AttachToPost(ctx, owner, retryPost, clip("queue-hold-retry.mp4", "red"), "retry.mp4", nil)
	if err != nil || retry.Status != "pending" {
		t.Fatal("retried video admission was not withheld pending processing", err)
	}
	if _, err := db.Exec(ctx, `UPDATE media_attachment SET status='processing',processing_attempts=$2,processing_started_at=now()-interval '1 hour' WHERE id=$1`, retry.ID, maxAttempts-1); err != nil {
		t.Fatal(err)
	}
	if processed, err := service.ProcessPendingVideos(ctx, 3); processed != 2 || err != nil {
		t.Fatal("exhausted held lease blocked later videos", processed, err)
	}
	for _, id := range []int64{fresh.ID, retry.ID} {
		var state, remaining string
		if err := db.QueryRow(ctx, `SELECT status,source_storage_key FROM media_attachment WHERE id=$1`, id).Scan(&state, &remaining); err != nil || state != "ready" || remaining != "" {
			t.Fatalf("later video did not reach ready: id=%d state=%s error=%v", id, state, err)
		}
	}
	var state, sourceKey string
	var attempts, queued int
	if err := db.QueryRow(ctx, `SELECT status,source_storage_key,processing_attempts,(SELECT count(*) FROM media_go_blobdeletion WHERE storage_key=$2) FROM media_attachment WHERE id=$1`, held, key).Scan(&state, &sourceKey, &attempts, &queued); err != nil || state != "processing" || sourceKey != key || attempts != maxAttempts || queued != 0 {
		t.Fatalf("held exhausted lease changed evidence: state=%s attempts=%d queued=%d error=%v", state, attempts, queued, err)
	}
	if size, err := store.Size(ctx, key); err != nil || size != int64(len(source)) {
		t.Fatal("held exhausted lease lost retained source bytes")
	}
	var audits int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE target_ref=$1`, fmt.Sprintf("media.attachment:%d", held)).Scan(&audits); err != nil || audits != 0 {
		t.Fatal("held exhausted lease received an outcome audit", audits, err)
	}
}
