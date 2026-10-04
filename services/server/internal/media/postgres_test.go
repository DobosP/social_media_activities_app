package media_test

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var mediaTestDSN = flag.String("media-test-dsn", "", "Explicit disposable PostgreSQL fixture; never discover credentials in environment")

type cleanScanner struct{}

func (cleanScanner) Scan(context.Context, media.ScanInput) (media.Verdict, error) {
	return media.Verdict{Clean: true}, nil
}

type cleanDocuments struct{}

func (cleanDocuments) ScanDocument(context.Context, string, int64) (media.Verdict, error) {
	return media.Verdict{Clean: true}, nil
}
func mediaStore(t *testing.T) (*media.Service, *social.Service, *pgxpool.Pool, *media.LocalStore) {
	t.Helper()
	if *mediaTestDSN == "" {
		t.Skip("explicit disposable media-test-dsn not supplied")
	}
	for _, bin := range []string{"ffmpeg", "ffprobe", "prlimit", "avifenc"} {
		if _, e := exec.LookPath(bin); e != nil {
			t.Skip("native codec runtime absent")
		}
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, *mediaTestDSN)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("media_native_test_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	rows, e := admin.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' AND (tablename LIKE 'accounts_%' OR tablename LIKE 'social_%' OR tablename LIKE 'safety_%' OR tablename LIKE 'notifications_%' OR tablename LIKE 'connections_%' OR tablename LIKE 'communities_%' OR tablename LIKE 'taxonomy_%' OR tablename LIKE 'places_%' OR tablename LIKE 'media_%' OR tablename='django_content_type') ORDER BY tablename`)
	if e != nil {
		t.Fatal(e)
	}
	var tables []string
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, table := range tables {
		if _, e = admin.Exec(ctx, `CREATE TABLE `+schema+`.`+pgx.Identifier{table}.Sanitize()+` (LIKE public.`+pgx.Identifier{table}.Sanitize()+` INCLUDING ALL)`); e != nil {
			t.Fatal(table, e)
		}
	}
	for _, table := range []string{"taxonomy_activitycategory", "taxonomy_activitytype", "places_childvenueclass", "django_content_type"} {
		if _, e = admin.Exec(ctx, `INSERT INTO `+schema+`.`+table+` SELECT * FROM public.`+table); e != nil {
			t.Fatal(e)
		}
	}
	cfg, e := pgxpool.ParseConfig(*mediaTestDSN)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 4
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		_, e := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	if e = media.EnsureSchema(ctx, db); e != nil {
		t.Fatal("native schema", e)
	}
	processor, e := media.NewProcessor(media.DefaultConfig(t.TempDir()), cleanScanner{}, cleanDocuments{})
	if e != nil {
		t.Fatal(e)
	}
	blobs, e := media.NewLocalStore(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { blobs.Close() })
	socialService := social.New(db, platform.RecordAudit)
	service := media.NewService(db, processor, blobs, media.TokenCodec{Key: []byte("synthetic-test-key-32-bytes-long!!")}, socialService)
	return service, socialService, db, blobs
}
func user(t *testing.T, db *pgxpool.Pool, name, cohort string) platform.Actor {
	t.Helper()
	a := platform.Actor{Username: name, DisplayName: name, Cohort: cohort, AgeBand: "adult", Role: "user", IsActive: true, IdentityVerified: true}
	if cohort == "child" {
		a.AgeBand = "under_16"
	}
	e := db.QueryRow(context.Background(), `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,$2,$3,true,now(),'user',true,false,now()) RETURNING id,public_id::text`, name, a.AgeBand, cohort).Scan(&a.ID, &a.PublicID)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func activity(t *testing.T, s *social.Service, db *pgxpool.Pool, a platform.Actor) (int64, int64) {
	t.Helper()
	var place, typeID int64
	e := db.QueryRow(context.Background(), `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Synthetic media hall','osm','',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{}','','','Cluj-Napoca','','RO','','{}','','',now(),now(),'','','') RETURNING id`).Scan(&place)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.QueryRow(context.Background(), `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typeID); e != nil {
		t.Fatal(e)
	}
	id, e := s.CreateActivity(context.Background(), a, social.ActivityInput{Place: place, ActivityType: typeID, Title: "Synthetic media meetup", StartsAt: time.Now().Add(24 * time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	return id, place
}
func sourceImage(t *testing.T) string {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 128, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 128; x++ {
			im.SetNRGBA(x, y, color.NRGBA{uint8(x * 2), uint8(y * 2), uint8(x + y), 255})
		}
	}
	path := filepath.Join(t.TempDir(), "image.png")
	f, e := os.Create(path)
	if e != nil {
		t.Fatal(e)
	}
	e = png.Encode(f, im)
	f.Close()
	if e != nil {
		t.Fatal(e)
	}
	return path
}
func request(mux *http.ServeMux, actor platform.Actor, method, url string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, url, nil)
	if actor.ID > 0 {
		r = platform.WithActor(r, actor)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestPostgresMediaAuthorizationAndLifecycle(t *testing.T) {
	m, s, db, blobs := mediaStore(t)
	ctx := context.Background()
	owner := user(t, db, "media-owner", "adult")
	peer := user(t, db, "media-peer", "adult")
	child := user(t, db, "media-child", "child")
	act, place := activity(t, s, db, owner)
	post, e := s.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic media post"}, false)
	if e != nil {
		t.Fatal(e)
	}
	imagePath := sourceImage(t)
	photo, e := m.UploadPhoto(ctx, owner, "profile", 0, imagePath)
	if e != nil {
		t.Fatal("photo", e)
	}
	mux := http.NewServeMux()
	m.Register(mux)
	if response := request(mux, owner, "GET", photo.URL); response.Code != 200 || response.Header().Get("Content-Type") != "image/avif" {
		t.Fatalf("owner photo: %d %s", response.Code, response.Body.String())
	}
	if response := request(mux, peer, "GET", photo.URL); response.Code != 403 {
		t.Fatal("viewer token reused", response.Code)
	}
	if response := request(mux, child, "GET", fmt.Sprintf("/api/media/photos/%d/", photo.ID)); response.Code != 403 {
		t.Fatal("cohort photo leak", response.Code)
	}
	if _, e = m.UploadPhoto(ctx, peer, "profile", 0, imagePath); !errors.Is(e, platform.ErrInvalid) {
		t.Fatal("duplicate avatar", e)
	}
	cover, e := m.UploadActivityCover(ctx, owner, act, imagePath, "Synthetic contextual cover")
	if e != nil {
		t.Fatal("cover", e)
	}
	q := &countQuerier{Querier: db}
	visuals, e := m.ActivityVisuals(ctx, q, owner, []int64{act})
	if e != nil || q.queries != 1 || q.rows != 0 || visuals[act].(map[string]any)["kind"] != "activity_cover_photo" {
		t.Fatal("batch visual query contract", q.queries, q.rows, visuals, e)
	}
	if response := request(mux, platform.Actor{}, "GET", fmt.Sprintf("/api/media/activity-covers/%d/", act)); response.Code != 403 {
		t.Fatal("unlisted activity exposed", response.Code)
	}
	if e = s.SetActivityListing(ctx, owner, act, true); e != nil {
		t.Fatal(e)
	}
	response := request(mux, platform.Actor{}, "GET", fmt.Sprintf("/api/media/activity-covers/%d/", act))
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var publicCover media.Cover
	if e = json.Unmarshal(response.Body.Bytes(), &publicCover); e != nil {
		t.Fatal(e)
	}
	if response = request(mux, platform.Actor{}, "GET", publicCover.URL); response.Code != 200 {
		t.Fatal("public cover", response.Code)
	}
	if e = s.SetActivityListing(ctx, owner, act, false); e != nil {
		t.Fatal(e)
	}
	if response = request(mux, platform.Actor{}, "GET", publicCover.URL); response.Code != 403 {
		t.Fatal("withdrawn cover served", response.Code)
	}
	_ = cover
	pdfPath := filepath.Join(t.TempDir(), "test.pdf")
	if e = os.WriteFile(pdfPath, []byte("%PDF-1.7\nsynthetic opaque file"), 0600); e != nil {
		t.Fatal(e)
	}
	pdf, e := m.AttachToPost(ctx, owner, post, pdfPath, "../../document.pdf", nil)
	if e != nil {
		t.Fatal("PDF", e)
	}
	pdfResponse := request(mux, owner, "GET", pdf.URL)
	if pdfResponse.Code != 200 || pdfResponse.Header().Get("Content-Disposition") != `attachment; filename="document.pdf"` || pdfResponse.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("PDF execution/download gate", pdfResponse.Code, pdfResponse.Header())
	}
	ttl := int64(1)
	att, e := m.AttachToPost(ctx, owner, post, imagePath, "image.png", &ttl)
	if e != nil {
		t.Fatal("attachment", e)
	}
	if att.ExpiresAt == nil || time.Until(*att.ExpiresAt) < 59*time.Minute {
		t.Fatal("adult expiry floor")
	}
	if response = request(mux, owner, "GET", att.URL); response.Code != 200 {
		t.Fatal("member attachment", response.Code, response.Body.String())
	}
	if _, e = db.Exec(ctx, `UPDATE social_membership SET state='left' WHERE user_id=$1 AND activity_id=$2`, owner.ID, act); e != nil {
		t.Fatal(e)
	}
	if response = request(mux, owner, "GET", att.URL); response.Code != 403 {
		t.Fatal("removed member served", response.Code)
	}
	if _, e = db.Exec(ctx, `UPDATE social_membership SET state='member' WHERE user_id=$1 AND activity_id=$2`, owner.ID, act); e != nil {
		t.Fatal(e)
	}
	var key string
	if e = db.QueryRow(ctx, `SELECT storage_key FROM media_attachment WHERE id=$1`, att.ID).Scan(&key); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, `UPDATE media_attachment SET expires_at=now()-interval '1 second' WHERE id=$1`, att.ID); e != nil {
		t.Fatal(e)
	}
	if response = request(mux, owner, "GET", att.URL); response.Code != 403 {
		t.Fatal("expired served", response.Code)
	}
	if n, e := m.PurgeExpiredAttachments(ctx, 20); e != nil || n != 1 {
		t.Fatal("purge", n, e)
	}
	if n, e := m.DrainBlobDeletions(ctx, 20); e != nil || n < 1 {
		t.Fatal("delete outbox", n, e)
	}
	if _, e = blobs.Size(ctx, key); e == nil {
		t.Fatal("purged blob retained")
	}
	// Business venue upload requires a verified approved claim, not merely adult.
	if _, e = m.UploadPlaceCover(ctx, peer, place, imagePath, "venue"); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("unclaimed cover", e)
	}
	// Deleting a row outside this service still queues physical erasure, proving
	// the native SQL/cascade hook protects account and activity deletion paths.
	if e = db.QueryRow(ctx, `SELECT storage_key FROM media_photo WHERE id=$1`, photo.ID).Scan(&key); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, `DELETE FROM media_photo WHERE id=$1`, photo.ID); e != nil {
		t.Fatal(e)
	}
	if n, e := m.DrainBlobDeletions(ctx, 20); e != nil || n < 1 {
		t.Fatal("cascade cleanup", n, e)
	}
	if _, e = blobs.Size(ctx, key); e == nil {
		t.Fatal("deleted photo retained")
	}
}

type countQuerier struct {
	platform.Querier
	queries, rows int
}

func (q *countQuerier) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.queries++
	return q.Querier.Query(ctx, sql, args...)
}
func (q *countQuerier) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	q.rows++
	return q.Querier.QueryRow(ctx, sql, args...)
}

func TestPostgresVideoWithheldReadyAndEvidence(t *testing.T) {
	m, s, db, _ := mediaStore(t)
	ctx := context.Background()
	owner := user(t, db, "video-owner", "adult")
	act, _ := activity(t, s, db, owner)
	post, e := s.WritePost(ctx, owner, "activity", act, social.PostInput{Body: "Synthetic clip"}, false)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "source.mp4")
	command := exec.Command("ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", "color=c=blue:s=64x48:r=10", "-t", "1", "-c:v", "libx264", "-threads", "1", path)
	if e = command.Run(); e != nil {
		t.Fatal(e)
	}
	att, e := m.AttachToPost(ctx, owner, post, path, "source.mp4", nil)
	if e != nil || att.Status != "pending" || att.URL != "" {
		t.Fatal("withheld admission", att, e)
	}
	if n, e := m.ProcessPendingVideos(ctx, 1); e != nil || n != 1 {
		t.Fatal("worker", n, e)
	}
	byPost, e := m.ForPosts(ctx, owner, []int64{post})
	if e != nil || len(byPost[post]) != 1 {
		t.Fatal(e)
	}
	ready := byPost[post][0]
	if ready.Status != "ready" || ready.URL == "" || ready.PosterURL == "" {
		t.Fatal("ready output", ready)
	}
	mux := http.NewServeMux()
	m.Register(mux)
	response := request(mux, owner, "GET", ready.URL)
	if response.Code != 200 || response.Header().Get("Content-Type") != "video/mp4" {
		t.Fatal(response.Code, response.Body.String())
	}
	if response = request(mux, owner, "GET", ready.PosterURL); response.Code != 200 {
		t.Fatal("poster", response.Code)
	}
	if _, e = db.Exec(ctx, `UPDATE media_attachment SET expires_at=now()-interval '1 second' WHERE id=$1`, ready.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(ctx, `UPDATE social_post SET is_hidden=true,is_author_deleted=false WHERE id=$1`, post); e != nil {
		t.Fatal(e)
	}
	if n, e := m.PurgeExpiredAttachments(ctx, 20); e != nil || n != 0 {
		t.Fatal("evidence incorrectly purged", n, e)
	}
	if _, e = db.Exec(ctx, `UPDATE social_post SET is_author_deleted=true WHERE id=$1`, post); e != nil {
		t.Fatal(e)
	}
	if n, e := m.PurgeExpiredAttachments(ctx, 20); e != nil || n != 1 {
		t.Fatal("author withdrawal not purged", n, e)
	}
}
