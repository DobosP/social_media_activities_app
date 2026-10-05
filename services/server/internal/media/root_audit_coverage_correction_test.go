package media_test

import (
	"context"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestRootAuditCorrectionDuplicateProfileRejectsAndRollsBackUploader(t *testing.T) {
	m, _, db, blobs := integratedMedia(t)
	ctx := context.Background()
	firstOwner := testdb.Actor(t, db, "root-audit-profile-first", "adult")
	rejectedOwner := testdb.Actor(t, db, "root-audit-profile-rejected", "adult")
	image := sourceImage(t)
	first, err := m.UploadPhoto(ctx, firstOwner, "profile", 0, image)
	if err != nil {
		t.Fatal("first fixture profile upload", err)
	}
	var firstKey string
	if err := db.QueryRow(ctx, `SELECT storage_key FROM media_photo WHERE id=$1 AND uploader_id=$2 AND kind='profile'`, first.ID, firstOwner.ID).Scan(&firstKey); err != nil {
		t.Fatal(err)
	}
	if _, err := m.UploadPhoto(ctx, rejectedOwner, "profile", 0, image); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("duplicate same-cohort content was admitted", err)
	}
	var rejectedProfiles, allProfiles int
	var retainedID int64
	var retainedKey string
	if err := db.QueryRow(ctx, `SELECT count(*) FROM media_photo WHERE uploader_id=$1 AND kind='profile'`, rejectedOwner.ID).Scan(&rejectedProfiles); err != nil || rejectedProfiles != 0 {
		t.Fatal("rejected uploader retained a profile row after duplicate failure", rejectedProfiles, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM media_photo WHERE kind='profile'`).Scan(&allProfiles); err != nil || allProfiles != 1 {
		t.Fatal("duplicate rejection did not preserve exact profile row count", allProfiles, err)
	}
	if err := db.QueryRow(ctx, `SELECT id,storage_key FROM media_photo WHERE uploader_id=$1 AND kind='profile'`, firstOwner.ID).Scan(&retainedID, &retainedKey); err != nil || retainedID != first.ID || retainedKey != firstKey {
		t.Fatal("failed duplicate changed first uploader's committed profile", err)
	}
	if size, err := blobs.Size(ctx, firstKey); err != nil || size == 0 {
		t.Fatal("failed duplicate reclaimed the first owner's physical image", err)
	}
}

func TestRootAuditCorrectionDistinctMemberVideoPendingThenReady(t *testing.T) {
	m, soc, db, _ := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "root-audit-video-owner", "adult")
	member := testdb.Actor(t, db, "root-audit-video-member", "adult")
	if owner.ID == member.ID {
		t.Fatal("fellow member must be a distinct authenticated actor")
	}
	act, _ := activity(t, soc, db, owner)
	// The original fixture installs an actual MEMBER row directly; use the
	// same membership state while exercising native fresh authority checks.
	if _, err := db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, act, member.ID); err != nil {
		t.Fatal(err)
	}
	post, err := soc.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic root audit clip"}, false)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "root-audit-video.mp4")
	codecCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := exec.CommandContext(codecCtx, "ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", path).Run(); err != nil {
		t.Fatal("synthetic video fixture", err)
	}
	att, err := m.AttachToPost(ctx, owner, post, path, "synthetic.mp4", nil)
	if err != nil || att.Status != "pending" {
		t.Fatal("video admission was not withheld pending processing", err)
	}
	pendingRows, err := m.ForPosts(ctx, member, []int64{post})
	if err != nil || len(pendingRows[post]) != 1 {
		t.Fatal("distinct fellow member cannot read pending placeholder", err)
	}
	pending := pendingRows[post][0]
	if pending.ID != att.ID || !pending.Processing || pending.URL != "" || pending.PosterURL != "" {
		t.Fatal("pending fellow-member view exposed video or lost processing flag")
	}
	if n, err := m.ProcessPendingVideos(ctx, 1); err != nil || n != 1 {
		t.Fatal("actual native video processing did not complete one clip", n, err)
	}
	readyRows, err := m.ForPosts(ctx, member, []int64{post})
	if err != nil || len(readyRows[post]) != 1 {
		t.Fatal("distinct fellow member cannot read same ready attachment", err)
	}
	ready := readyRows[post][0]
	if ready.ID != att.ID || ready.Processing || ready.Status != "ready" || ready.URL == "" || ready.PosterURL == "" {
		t.Fatal("ready fellow-member view lacks playable URL/poster or still reports processing")
	}
	mux := http.NewServeMux()
	m.Register(mux)
	video := request(mux, member, "GET", ready.URL)
	if video.Code != 200 || video.Header().Get("Content-Type") != "video/mp4" || video.Body.Len() == 0 {
		t.Fatal("ready fellow-member video URL is not playable through native media adapter", video.Code)
	}
	poster := request(mux, member, "GET", ready.PosterURL)
	if poster.Code != 200 || poster.Body.Len() == 0 {
		t.Fatal("ready fellow-member poster URL is not retrievable", poster.Code)
	}
}

func TestRootAuditCorrectionSameUploaderReportActionedThenDismissedReleasesBlob(t *testing.T) {
	m, soc, db, blobs := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "root-audit-evidence-owner", "adult")
	reporter := testdb.Actor(t, db, "root-audit-evidence-reporter", "adult")
	act, _ := activity(t, soc, db, owner)
	if _, err := db.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, act, reporter.ID); err != nil {
		t.Fatal(err)
	}
	post, err := soc.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic root audit evidence"}, false)
	if err != nil {
		t.Fatal(err)
	}
	ttl := int64(3600)
	att, err := m.AttachToPost(ctx, owner, post, sourceImage(t), "synthetic.png", &ttl)
	if err != nil {
		t.Fatal(err)
	}
	var key string
	if err := db.QueryRow(ctx, `SELECT storage_key FROM media_attachment WHERE id=$1`, att.ID).Scan(&key); err != nil || key == "" {
		t.Fatal("attachment lacks committed physical image", err)
	}
	if _, err := db.Exec(ctx, `UPDATE media_attachment SET expires_at=now()-interval '5 minutes' WHERE id=$1`, att.ID); err != nil {
		t.Fatal(err)
	}
	var reportID int64
	if err := db.QueryRow(ctx, `INSERT INTO safety_report(target_type_id,target_id,reporter_id,reason,detail,status,resolution,created_at,handled_by_id,handled_at) SELECT id,$1,$2,'other','Synthetic root audit report','actioned','',now(),NULL,NULL FROM django_content_type WHERE app_label='accounts' AND model='user' RETURNING id`, owner.ID, reporter.ID).Scan(&reportID); err != nil {
		t.Fatal("uploader-target report fixture", err)
	}
	if n, err := m.PurgeExpiredAttachments(ctx, 20); err != nil || n != 0 {
		t.Fatal("actioned uploader report did not preserve same attachment", n, err)
	}
	if _, err := m.DrainBlobDeletions(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var purged bool
	var heldKey string
	if err := db.QueryRow(ctx, `SELECT purged_at IS NOT NULL,storage_key FROM media_attachment WHERE id=$1`, att.ID).Scan(&purged, &heldKey); err != nil || purged || heldKey != key {
		t.Fatal("actioned report changed purge marker/physical identity", err)
	}
	if size, err := blobs.Size(ctx, key); err != nil || size == 0 {
		t.Fatal("actioned uploader report lost physical evidence", err)
	}
	if _, err := db.Exec(ctx, `UPDATE safety_report SET status='dismissed' WHERE id=$1`, reportID); err != nil {
		t.Fatal(err)
	}
	if n, err := m.PurgeExpiredAttachments(ctx, 20); err != nil || n != 1 {
		t.Fatal("dismissing same uploader report failed to release same attachment", n, err)
	}
	if _, err := m.DrainBlobDeletions(ctx, 20); err != nil {
		t.Fatal(err)
	}
	var retainedID int64
	if err := db.QueryRow(ctx, `SELECT id,purged_at IS NOT NULL,storage_key FROM media_attachment WHERE id=$1`, att.ID).Scan(&retainedID, &purged, &heldKey); err != nil || retainedID != att.ID || !purged || heldKey != "" {
		t.Fatal("dismissed report did not preserve provenance row with erased delivery key", err)
	}
	if _, err := blobs.Size(ctx, key); err == nil {
		t.Fatal("dismissed uploader report left physical image bytes after cleanup")
	}
}
