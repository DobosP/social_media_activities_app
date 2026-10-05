package media_test

import (
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// structuredImage is a deterministic grid of nine 16px columns whose adjacent
// columns always differ by at least 104 grey levels. Its stored dHash is
// therefore non-empty and survives a lossy JPEG re-encode; a smooth ramp such as
// sourceImage can yield an empty dHash and make fingerprint assertions vacuous.
// A positive jpegQuality writes the re-encoded near-duplicate instead of PNG.
func structuredImage(t *testing.T, jpegQuality int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 144, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 144; x++ {
			v := uint8(20 + 26*((x/16*4)%9))
			img.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	name := "structured.png"
	if jpegQuality > 0 {
		name = "structured.jpg"
	}
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if jpegQuality > 0 {
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: jpegQuality})
	} else {
		err = png.Encode(f, img)
	}
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
func manifestKeys(t *testing.T, db *pgxpool.Pool, kind string, id int64) (perceptual, source bool) {
	t.Helper()
	if err := db.QueryRow(context.Background(), `SELECT payload ? 'perceptual_hash',payload ? 'source_sha256' FROM media_go_manifest WHERE kind=$1 AND row_id=$2`, kind, id).Scan(&perceptual, &source); err != nil {
		t.Fatal("manifest", kind, id, err)
	}
	return perceptual, source
}
func photoFingerprint(t *testing.T, db *pgxpool.Pool, id int64) string {
	t.Helper()
	var phash string
	if err := db.QueryRow(context.Background(), `SELECT phash FROM media_photo WHERE id=$1`, id).Scan(&phash); err != nil {
		t.Fatal(err)
	}
	return phash
}

// Ports apps/media/tests/test_profile_unique.py::test_same_image_allowed_across_cohorts.
func TestNativeProfileUniquenessAllowsSameImageAcrossCohorts(t *testing.T) {
	m, _, db, _ := integratedMedia(t)
	ctx := context.Background()
	adult := testdb.Actor(t, db, "generated-xc-adult", "adult")
	child := testdb.Actor(t, db, "generated-xc-child", "child")
	teen := testdb.Actor(t, db, "generated-xc-teen", "teen")
	img := sourceImage(t)
	if _, err := m.UploadPhoto(ctx, adult, "profile", 0, img); err != nil {
		t.Fatal("adult profile upload", err)
	}
	photo, err := m.UploadPhoto(ctx, child, "profile", 0, img)
	if err != nil || photo.Kind != "profile" {
		t.Fatal("same image refused across the cohort wall", err)
	}
	if _, err = m.UploadPhoto(ctx, teen, "profile", 0, img); err != nil {
		t.Fatal("same image refused for a third cohort", err)
	}
	if mediaCount(t, db, "media_photo") != 3 {
		t.Fatal("each cohort should hold its own profile row")
	}
	// The scan scope is the committed cohort, never the request snapshot: a
	// stale "adult" snapshot whose row is now unassigned is compared only with
	// unassigned avatars, so the adult-held image is accepted.
	stale := testdb.Actor(t, db, "generated-xc-stale", "adult")
	if _, err = db.Exec(ctx, `UPDATE accounts_user SET age_band='unknown',cohort='unassigned' WHERE id=$1`, stale.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = m.UploadPhoto(ctx, stale, "profile", 0, img); err != nil {
		t.Fatal("upload was scoped by the stale request cohort", err)
	}
	if mediaCount(t, db, "media_photo") != 4 {
		t.Fatal("unassigned upload should hold its own profile row")
	}
}

// Ports apps/media/tests/test_perceptual.py::test_profile_near_duplicate_rejected_within_cohort_only.
func TestNativeProfileNearDuplicateRejectedWithinCohortOnly(t *testing.T) {
	m, _, db, _ := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-ph-owner", "adult")
	copycat := testdb.Actor(t, db, "generated-ph-copycat", "adult")
	child := testdb.Actor(t, db, "generated-ph-child", "child")
	teen := testdb.Actor(t, db, "generated-ph-teen", "teen")
	original := structuredImage(t, 0)
	reencoded := structuredImage(t, 60)
	if _, err := m.UploadPhoto(ctx, owner, "profile", 0, original); err != nil {
		t.Fatal("owner profile upload", err)
	}
	if _, err := m.UploadPhoto(ctx, copycat, "profile", 0, reencoded); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("same-cohort re-encoded near-duplicate admitted", err)
	}
	photo, err := m.UploadPhoto(ctx, child, "profile", 0, original)
	if err != nil {
		t.Fatal("other-cohort original refused across the cohort wall", err)
	}
	if phash := photoFingerprint(t, db, photo.ID); len(phash) != 16 {
		t.Fatal("other-cohort avatar lacks its perceptual fingerprint", phash)
	}
	if _, err = m.UploadPhoto(ctx, teen, "profile", 0, reencoded); err != nil {
		t.Fatal("other-cohort near-duplicate refused across the cohort wall", err)
	}
	var copies int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM media_photo WHERE uploader_id=$1`, copycat.ID).Scan(&copies); err != nil || copies != 0 {
		t.Fatal("rejected near-duplicate stored a row", copies, err)
	}
}

func TestNativeNonProfileMediaStoresNoFingerprint(t *testing.T) {
	m, s, db, _ := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-minimise-owner", "adult")
	act, thread := fixtureThread(t, s, db, owner)
	img := structuredImage(t, 0)
	profile, err := m.UploadPhoto(ctx, owner, "profile", 0, img)
	if err != nil {
		t.Fatal("profile", err)
	}
	threadPhoto, err := m.UploadPhoto(ctx, owner, "thread", thread, img)
	if err != nil {
		t.Fatal("thread photo", err)
	}
	post, err := s.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic minimisation post"}, false)
	if err != nil {
		t.Fatal(err)
	}
	att, err := m.AttachToPost(ctx, owner, post, img, "structured.png", nil)
	if err != nil {
		t.Fatal("attachment", err)
	}
	prepared, err := m.PrepareThreadAttachment(ctx, owner, thread, img, "prepared.png", nil)
	if err != nil {
		t.Fatal("prepared attachment", err)
	}
	preparedPost, err := s.WritePostAttached(ctx, owner, "activity", act, social.PostInput{}, false, prepared.Publish)
	prepared.Finish(ctx, err == nil)
	if err != nil {
		t.Fatal("prepared publication", err)
	}
	var preparedID int64
	if err = db.QueryRow(ctx, `SELECT id FROM media_attachment WHERE post_id=$1`, preparedPost).Scan(&preparedID); err != nil {
		t.Fatal(err)
	}
	cover, err := m.UploadActivityCover(ctx, owner, act, img, "Synthetic minimisation cover")
	if err != nil {
		t.Fatal("activity cover", err)
	}
	// The avatar keeps its fingerprint, proving the fixture carries one at all.
	if phash := photoFingerprint(t, db, profile.ID); len(phash) != 16 {
		t.Fatal("profile fingerprint missing", phash)
	}
	if perceptual, _ := manifestKeys(t, db, "photo", profile.ID); !perceptual {
		t.Fatal("profile manifest lost its fingerprint")
	}
	if phash := photoFingerprint(t, db, threadPhoto.ID); phash != "" {
		t.Fatal("private thread photo stored a fingerprint", phash)
	}
	for _, row := range []struct {
		kind string
		id   int64
	}{{"photo", threadPhoto.ID}, {"attachment", att.ID}, {"attachment", preparedID}, {"activity-cover", cover.ID}} {
		if perceptual, source := manifestKeys(t, db, row.kind, row.id); perceptual || source {
			t.Fatal("non-profile manifest kept a fingerprint or original digest", row.kind, row.id, perceptual, source)
		}
	}
	var uploads, digests int
	if err = db.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE data ? 'source_sha256') FROM safety_auditlog WHERE event IN ('media.uploaded','media.attached','media.activity_cover_uploaded')`).Scan(&uploads, &digests); err != nil {
		t.Fatal(err)
	}
	if uploads != 5 || digests != 0 {
		t.Fatal("success audit rows carry the original digest", uploads, digests)
	}
}

func TestNativeMediaSchemaScrubsStoredNonProfileFingerprints(t *testing.T) {
	_, s, db, _ := integratedMedia(t)
	ctx := context.Background()
	owner := testdb.Actor(t, db, "generated-scrub-owner", "adult")
	_, thread := fixtureThread(t, s, db, owner)
	digest := strings.Repeat("ab", 32)
	insert := `INSERT INTO media_photo(kind,storage_key,content_type,byte_size,sha256,width,height,scan_status,exif_stripped,created_at,thread_id,uploader_id,phash,thumb_storage_key) VALUES($1,$2,'image/avif',1,$3,1,1,'clean',true,now(),$4,$5,$6,'') RETURNING id`
	var profileID, threadID int64
	if err := db.QueryRow(ctx, insert, "profile", "generated-test/scrub-profile.avif", digest, nil, owner.ID, "a5a5a5a5a5a5a5a5").Scan(&profileID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, insert, "thread", "generated-test/scrub-thread.avif", digest, thread, owner.ID, "0f0f0f0f0f0f0f0f").Scan(&threadID); err != nil {
		t.Fatal(err)
	}
	payload := `{"kind":"image","perceptual_hash":"0f0f0f0f0f0f0f0f","source_sha256":"` + digest + `"}`
	video := `{"kind":"video","perceptual_hash":"0f0f0f0f0f0f0f0f","source_sha256":"` + digest + `"}`
	for _, row := range []struct {
		kind    string
		id      int64
		payload string
	}{{"photo", profileID, payload}, {"photo", threadID, payload}, {"activity-cover", 900000001, payload}, {"attachment", 900000002, video}} {
		if _, err := db.Exec(ctx, `INSERT INTO media_go_manifest(kind,row_id,payload) VALUES($1,$2,$3::jsonb)`, row.kind, row.id, row.payload); err != nil {
			t.Fatal(err)
		}
	}
	// Bootstrap runs on every start, so the scrub must be repeatable.
	for i := 0; i < 2; i++ {
		if err := media.EnsureSchema(ctx, db); err != nil {
			t.Fatal("schema scrub", i, err)
		}
	}
	if phash := photoFingerprint(t, db, threadID); phash != "" {
		t.Fatal("stored thread fingerprint survived the scrub", phash)
	}
	if phash := photoFingerprint(t, db, profileID); phash != "a5a5a5a5a5a5a5a5" {
		t.Fatal("scrub changed an avatar fingerprint", phash)
	}
	if perceptual, source := manifestKeys(t, db, "photo", profileID); !perceptual || !source {
		t.Fatal("scrub changed the avatar manifest", perceptual, source)
	}
	if perceptual, source := manifestKeys(t, db, "photo", threadID); perceptual || source {
		t.Fatal("thread manifest kept a fingerprint or original digest", perceptual, source)
	}
	if perceptual, source := manifestKeys(t, db, "activity-cover", 900000001); perceptual || source {
		t.Fatal("cover manifest kept a fingerprint or original digest", perceptual, source)
	}
	if perceptual, source := manifestKeys(t, db, "attachment", 900000002); perceptual || !source {
		t.Fatal("video manifest must keep only its worker digest", perceptual, source)
	}
}
