package media_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type integrationBlobs struct {
	*media.LocalStore
	keys               []string
	refuseDelete       bool
	failPutAfterCommit bool
	blockDelete        bool
}

func (s *integrationBlobs) Put(ctx context.Context, key, path, mime string) error {
	if err := s.LocalStore.Put(ctx, key, path, mime); err != nil {
		return err
	}
	s.keys = append(s.keys, key)
	if s.failPutAfterCommit {
		return media.ErrObject
	}
	return nil
}
func (s *integrationBlobs) Delete(ctx context.Context, key string) error {
	if s.blockDelete {
		<-ctx.Done()
		return ctx.Err()
	}
	if s.refuseDelete {
		return media.ErrObject
	}
	return s.LocalStore.Delete(ctx, key)
}

func integratedMedia(t *testing.T) (*media.Service, *social.Service, *pgxpool.Pool, *integrationBlobs) {
	t.Helper()
	if *mediaTestDSN == "" {
		t.Skip("explicit disposable media-test-dsn not supplied")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit", "avifenc"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Fatal("required qualification codec unavailable", bin)
		}
	}
	db := testdb.New(t, *mediaTestDSN, nil)
	ctx := context.Background()
	if err := catalog.New(db).Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := media.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	p, err := media.NewProcessor(media.DefaultConfig(t.TempDir()), cleanScanner{}, cleanDocuments{})
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := media.NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blobs.Close() })
	store := &integrationBlobs{LocalStore: blobs}
	s := social.New(db, platform.RecordAudit)
	return media.NewService(db, p, store, media.TokenCodec{Key: []byte(strings.Repeat("x", 32))}, s), s, db, store
}
func fixtureThread(t *testing.T, s *social.Service, db *pgxpool.Pool, owner platform.Actor) (int64, int64) {
	t.Helper()
	id, _ := activity(t, s, db, owner)
	var thread int64
	if err := db.QueryRow(context.Background(), `SELECT id FROM social_thread WHERE activity_id=$1`, id).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	return id, thread
}
func mediaCount(t *testing.T, db *pgxpool.Pool, table string) int {
	t.Helper()
	var count int
	if err := db.QueryRow(context.Background(), `SELECT count(*) FROM `+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestNativePreparedAttachmentRollbackCleanupAndOneShotPublication(t *testing.T) {
	m, s, db, blobs := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-prepared-owner", "adult")
	activity, thread := fixtureThread(t, s, db, owner)
	path := sourceImage(t)
	beforePosts, beforeAudits := mediaCount(t, db, "social_post"), mediaCount(t, db, "safety_auditlog")
	if post, err := s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{Body: "text requires its promised attachment"}, false, func(context.Context, pgx.Tx, int64) error { return nil }); err == nil || post != 0 {
		t.Fatal("no-op attachment callback committed text post", post, err)
	}
	p, err := m.PrepareThreadAttachment(ctx, owner, thread, path, "fixture.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	blobs.refuseDelete = true
	s.Audit = func(context.Context, pgx.Tx, platform.Actor, string, string, any) error {
		return errors.New("synthetic later audit failure")
	}
	post, err := s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{}, false, p.Publish)
	p.Finish(ctx, err == nil)
	if err == nil || post != 0 || mediaCount(t, db, "social_post") != beforePosts || mediaCount(t, db, "media_attachment") != 0 || mediaCount(t, db, "safety_auditlog") != beforeAudits {
		t.Fatal("post/media/audit not atomic", post, err)
	}
	if mediaCount(t, db, "media_go_blobdeletion") != len(blobs.keys) {
		t.Fatal("failed physical cleanup not durable")
	}
	blobs.refuseDelete = false
	if n, err := m.DrainBlobDeletions(ctx, 10); err != nil || n != len(blobs.keys) {
		t.Fatal("rollback deletion drain", n, err)
	}
	for _, key := range blobs.keys {
		if _, err := blobs.Size(ctx, key); err == nil {
			t.Fatal("rollback blob survived")
		}
	}
	s.Audit = platform.RecordAudit
	p, err = m.PrepareThreadAttachment(ctx, owner, thread, path, "fixture.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	post, err = s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{}, false, p.Publish)
	if err != nil {
		p.Finish(ctx, false)
		t.Fatal(err)
	}
	if err = platform.Transaction(ctx, db, func(tx pgx.Tx) error { return p.Publish(ctx, tx, post) }); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("prepared media published twice", err)
	}
	p.Finish(ctx, true)
	if mediaCount(t, db, "media_attachment") != 1 {
		t.Fatal("duplicate publication")
	}
	items, err := m.ForPosts(ctx, owner, []int64{post})
	if err != nil || len(items[post]) != 1 || items[post][0].URL == "" {
		t.Fatal("committed attachment absent", err)
	}
	p, err = m.PrepareThreadAttachment(ctx, owner, thread, path, "abandoned.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	abandoned := blobs.keys[len(blobs.keys)-1]
	p.Finish(ctx, true) // No publication occurred, so no durable row owns these bytes.
	if _, err = blobs.Size(ctx, abandoned); err == nil {
		t.Fatal("unpublished preparation leaked")
	}
	p, err = m.PrepareThreadAttachment(ctx, owner, thread, path, "revoked.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `UPDATE social_membership SET state='removed' WHERE activity_id=$1 AND user_id=$2`, activity, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{}, false, p.Publish); err == nil {
		t.Fatal("revoked member published prepared media")
	}
	p.Finish(ctx, false)
}

func TestNativeLicensedCoverPreservesCreditsExistingCoverAndWithdrawal(t *testing.T) {
	m, _, db, blobs := integratedMedia(t)
	ctx := context.Background()
	place := testdb.Place(t, db, "generated-licensed-place", "osm")
	page := "https://commons.wikimedia.org/wiki/File:Generated_fixture.png"
	path := sourceImage(t)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// A valid image with bounded trailing bytes proves the reviewed Commons
	// 8 MiB contract is distinct from the user-upload 5 MiB ceiling.
	if err = os.WriteFile(path, append(raw, bytes.Repeat([]byte{0}, (5<<20)+1-len(raw))...), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := m.ImportLicensedPlaceCover(ctx, place, path, "Generated Author", "CC BY 4.0", page, "Generated hall")
	if err != nil || id == 0 {
		t.Fatal("licensed import", id, err)
	}
	var credit, license, url string
	if err = db.QueryRow(ctx, `SELECT attribution,license_name,source_page_url FROM places_placecover WHERE id=$1`, id).Scan(&credit, &license, &url); err != nil || credit != "Generated Author" || license != "CC BY 4.0" || url != page {
		t.Fatal("license provenance changed", err)
	}
	n := len(blobs.keys)
	if other, err := m.ImportLicensedPlaceCover(ctx, place, sourceImage(t), "Other", "CC0", page, "Other"); err != nil || other != 0 || len(blobs.keys) != n {
		t.Fatal("existing cover changed", other, err)
	}
	visuals, err := m.PlaceVisuals(ctx, db, []int64{place})
	if err != nil || visuals[place] == nil {
		t.Fatal("licensed public visual", err)
	}
	visual := visuals[place].(map[string]any)
	if visual["kind"] != "place_cover_photo" || visual["license_name"] != license || visual["attribution"] != credit || visual["source_page_url"] != page {
		t.Fatal("public credits missing", visual)
	}
	mux := http.NewServeMux()
	m.Register(mux)
	if response := request(mux, platform.Actor{}, "GET", visual["url"].(string)); response.Code != 200 || response.Header().Get("Content-Type") != "image/avif" {
		t.Fatal("anonymous licensed image", response.Code)
	}
	for i := 0; i < 3; i++ {
		reporter := testdb.Actor(t, db, fmt.Sprintf("generated-closure-%d", i), "adult")
		if _, err = db.Exec(ctx, `INSERT INTO places_placeclosurereport(place_id,reporter_id,created_at) VALUES($1,$2,now())`, place, reporter.ID); err != nil {
			t.Fatal(err)
		}
	}
	visuals, err = m.PlaceVisuals(ctx, db, []int64{place})
	if err != nil || len(visuals) != 0 {
		t.Fatal("withdrawn place visual survives", err)
	}
	if response := request(mux, platform.Actor{}, "GET", visual["url"].(string)); response.Code == 200 {
		t.Fatal("cached signed cover survives withdrawal")
	}
}

func TestNativeVideoTransitionsNotifyOnlyCommittedIdentifiersAndFreshPermissions(t *testing.T) {
	m, s, db, _ := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-video-owner", "adult")
	activity, thread := fixtureThread(t, s, db, owner)
	var schema string
	if err := db.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		t.Fatal(err)
	}
	listener, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Release()
	if _, err = listener.Exec(ctx, `LISTEN `+pgx.Identifier{chat.Channel(schema)}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	defer listener.Exec(ctx, `UNLISTEN *`)
	path := filepath.Join(t.TempDir(), "fixture.mp4")
	if err = exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", path).Run(); err != nil {
		t.Fatal(err)
	}
	p, err := m.PrepareThreadAttachment(ctx, owner, thread, path, "fixture.mp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	post, err := s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{}, false, p.Publish)
	p.Finish(ctx, err == nil)
	if err != nil {
		t.Fatal(err)
	}
	wait := func(event string) {
		t.Helper()
		timeout, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		notification, err := listener.Conn().WaitForNotification(timeout)
		if err != nil || !strings.Contains(notification.Payload, `"event":"`+event+`"`) || strings.Contains(notification.Payload, "storage") || strings.Contains(notification.Payload, "source") || strings.Contains(notification.Payload, "url") {
			t.Fatal("unsafe/missing committed video notification", event, err)
		}
	}
	wait("message")
	items, err := m.ForPosts(ctx, owner, []int64{post})
	if err != nil || len(items[post]) != 1 || !items[post][0].Processing || items[post][0].URL != "" {
		t.Fatal("video source exposed", err)
	}
	if n, err := m.ProcessPendingVideos(ctx, 1); err != nil || n != 1 {
		t.Fatal("video process", n, err)
	}
	wait("attachments")
	items, err = m.ForPosts(ctx, owner, []int64{post})
	if err != nil || len(items[post]) != 1 || items[post][0].URL == "" || items[post][0].PosterURL == "" {
		t.Fatal("ready video missing", err)
	}
	stranger := testdb.Actor(t, db, "generated-former-staff", "adult")
	stranger.IsStaff = true
	if err := m.DeleteAttachment(ctx, stranger, items[post][0].ID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("stale staff deleted attachment", err)
	}
	p, err = m.PrepareThreadAttachment(ctx, owner, thread, path, "attempts.mp4", nil)
	if err != nil {
		t.Fatal(err)
	}
	failedPost, err := s.WritePostAttached(ctx, owner, "activity", activity, social.PostInput{}, false, p.Publish)
	p.Finish(ctx, err == nil)
	if err != nil {
		t.Fatal(err)
	}
	wait("message")
	if _, err = db.Exec(ctx, `UPDATE media_attachment SET processing_attempts=3 WHERE post_id=$1`, failedPost); err != nil {
		t.Fatal(err)
	}
	if n, err := m.ProcessPendingVideos(ctx, 1); err != nil || n != 0 {
		t.Fatal("exhausted video admission", n, err)
	}
	wait("attachments")
	failed, err := m.ForPosts(ctx, owner, []int64{failedPost})
	if err != nil || len(failed[failedPost]) != 1 || !failed[failedPost][0].Failed || failed[failedPost][0].URL != "" || failed[failedPost][0].PosterURL != "" {
		t.Fatal("failed video exposed or state notification absent", err)
	}
	if _, err = db.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ForPosts(ctx, owner, []int64{post}); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("inactive actor fetched media", err)
	}
	if _, err = os.Stat(path); err != nil {
		t.Fatal("caller fixture input removed", err)
	}
}

func TestNativeAmbiguousBlobWriteQueuesPhysicalCleanup(t *testing.T) {
	m, s, db, blobs := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-ambiguous-write", "adult")
	_, thread := fixtureThread(t, s, db, owner)
	blobs.failPutAfterCommit, blobs.blockDelete = true, true
	if p, err := m.PrepareThreadAttachment(ctx, owner, thread, sourceImage(t), "fixture.png", nil); err == nil || p != nil {
		t.Fatal("ambiguous write published", err)
	}
	if len(blobs.keys) != 1 || mediaCount(t, db, "media_go_blobdeletion") != 1 || mediaCount(t, db, "media_attachment") != 0 {
		t.Fatal("ambiguous committed object escaped cleanup")
	}
	blobs.failPutAfterCommit, blobs.blockDelete = false, false
	if n, err := m.DrainBlobDeletions(ctx, 10); err != nil || n != 1 {
		t.Fatal("ambiguous cleanup drain", n, err)
	}
	if _, err := blobs.Size(ctx, blobs.keys[0]); err == nil {
		t.Fatal("ambiguous blob remained")
	}
}
