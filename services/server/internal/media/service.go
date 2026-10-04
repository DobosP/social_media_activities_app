package media

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Authorizer interface {
	CanReadThread(context.Context, platform.Querier, platform.Actor, int64) (bool, error)
	CanWriteThread(context.Context, platform.Querier, platform.Actor, int64) (bool, error)
	CanViewProfilePhoto(context.Context, platform.Querier, platform.Actor, int64) (bool, error)
	CanSeeActivity(context.Context, platform.Querier, platform.Actor, int64) (bool, error)
}
type Service struct {
	db               *pgxpool.Pool
	processor        *Processor
	storage          Store
	tokens           TokenCodec
	auth             Authorizer
	ClosureThreshold int
	ClosureDecay     time.Duration
	Presign          bool
	inlineMu         sync.Mutex
	inlineContext    context.Context
	inlineCancel     context.CancelFunc
	inlineEnabled    bool
	inlineDone       chan struct{}
}

func NewService(db *pgxpool.Pool, p *Processor, storage Store, tokens TokenCodec, a Authorizer) *Service {
	return &Service{db: db, processor: p, storage: storage, tokens: tokens, auth: a, ClosureThreshold: 3, ClosureDecay: 14 * 24 * time.Hour}
}
func EnsureSchema(ctx context.Context, db *pgxpool.Pool) error {
	_, e := db.Exec(ctx, `
CREATE TABLE IF NOT EXISTS media_go_blobdeletion(storage_key varchar(160) PRIMARY KEY,created_at timestamptz NOT NULL DEFAULT now(),attempts integer NOT NULL DEFAULT 0,next_attempt_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS media_go_manifest(kind varchar(24) NOT NULL,row_id bigint NOT NULL,payload jsonb NOT NULL,created_at timestamptz NOT NULL DEFAULT now(),PRIMARY KEY(kind,row_id));
CREATE INDEX IF NOT EXISTS media_go_blobdeletion_due ON media_go_blobdeletion(next_attempt_at);
CREATE TABLE IF NOT EXISTS media_go_avatarattempt(user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,created_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX IF NOT EXISTS media_go_avatarattempt_user_time ON media_go_avatarattempt(user_id,created_at);
CREATE OR REPLACE FUNCTION media_go_queue_deleted_blobs() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE field_name text;object_key text;row_kind text;
BEGIN
FOREACH field_name IN ARRAY ARRAY['storage_key','thumb_storage_key','poster_storage_key','source_storage_key'] LOOP
object_key:=to_jsonb(OLD)->>field_name;
IF object_key IS NOT NULL AND object_key!='' THEN INSERT INTO media_go_blobdeletion(storage_key) VALUES(object_key) ON CONFLICT(storage_key) DO NOTHING; END IF;
END LOOP;
row_kind:=CASE TG_TABLE_NAME WHEN 'media_photo' THEN 'photo' WHEN 'media_attachment' THEN 'attachment' WHEN 'media_activitycover' THEN 'activity-cover' ELSE 'place-cover' END;
DELETE FROM media_go_manifest WHERE kind=row_kind AND row_id=OLD.id;
RETURN OLD;
END; $$;
DROP TRIGGER IF EXISTS media_go_blob_cleanup ON media_photo;
CREATE TRIGGER media_go_blob_cleanup BEFORE DELETE ON media_photo FOR EACH ROW EXECUTE FUNCTION media_go_queue_deleted_blobs();
DROP TRIGGER IF EXISTS media_go_blob_cleanup ON media_attachment;
CREATE TRIGGER media_go_blob_cleanup BEFORE DELETE ON media_attachment FOR EACH ROW EXECUTE FUNCTION media_go_queue_deleted_blobs();
DROP TRIGGER IF EXISTS media_go_blob_cleanup ON media_activitycover;
CREATE TRIGGER media_go_blob_cleanup BEFORE DELETE ON media_activitycover FOR EACH ROW EXECUTE FUNCTION media_go_queue_deleted_blobs();
DROP TRIGGER IF EXISTS media_go_blob_cleanup ON places_placecover;
CREATE TRIGGER media_go_blob_cleanup BEFORE DELETE ON places_placecover FOR EACH ROW EXECUTE FUNCTION media_go_queue_deleted_blobs();`)
	return e
}
func queueDelete(ctx context.Context, tx pgx.Tx, keys ...string) error {
	for _, k := range keys {
		if k != "" {
			if !validKey(k) {
				return ErrObject
			}
			if _, e := tx.Exec(ctx, `INSERT INTO media_go_blobdeletion(storage_key) VALUES($1) ON CONFLICT(storage_key) DO NOTHING`, k); e != nil {
				return e
			}
		}
	}
	return nil
}
func saveManifest(ctx context.Context, tx pgx.Tx, kind string, id int64, m Manifest) error {
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `INSERT INTO media_go_manifest(kind,row_id,payload) VALUES($1,$2,$3) ON CONFLICT(kind,row_id) DO UPDATE SET payload=excluded.payload,created_at=now()`, kind, id, b)
	return e
}
func randomKey(prefix, mime string) (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	ext := map[string]string{"image/avif": "avif", "image/webp": "webp", "image/png": "png", "application/pdf": "pdf", "video/mp4": "mp4", "application/octet-stream": "source"}[mime]
	if ext == "" {
		return "", ErrObject
	}
	return prefix + "/" + hex.EncodeToString(b[:]) + "." + ext, nil
}
func (s *Service) storeArtifact(ctx context.Context, prefix string, a Artifact) (string, error) {
	key, e := randomKey(prefix, a.ContentType)
	if e != nil {
		return "", e
	}
	if e = s.storage.Put(ctx, key, a.Path, a.ContentType); e != nil {
		// A remote PUT may commit before a response is lost. Retain the
		// allocated identity long enough to delete it or durably queue cleanup.
		s.discard(ctx, key)
		return "", e
	}
	return key, nil
}
func (s *Service) discard(ctx context.Context, keys ...string) {
	// Persist cleanup before a remote delete can exhaust its timeout. A
	// successful delete is idempotent, so an uncleared outbox row is safe.
	if s.db != nil {
		queued, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		pending := []string{}
		for _, key := range keys {
			if key != "" && validKey(key) {
				pending = append(pending, key)
			}
		}
		if len(pending) > 0 {
			_, _ = s.db.Exec(queued, `INSERT INTO media_go_blobdeletion(storage_key) SELECT unnest($1::text[]) ON CONFLICT(storage_key) DO NOTHING`, pending)
		}
		cancel()
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	for _, key := range keys {
		if key != "" {
			if e := s.storage.Delete(cleanup, key); e == nil && s.db != nil {
				_, _ = s.db.Exec(cleanup, `DELETE FROM media_go_blobdeletion WHERE storage_key=$1`, key)
			}
		}
	}
}
func (s *Service) approved(ctx context.Context, q platform.Querier, a platform.Actor, thread int64, write bool) error {
	fresh, e := currentMediaActor(ctx, q, a)
	if e != nil {
		return e
	}
	a = fresh
	if s.auth == nil {
		return platform.ErrForbidden
	}
	var ok bool
	var err error
	if write {
		ok, err = s.auth.CanWriteThread(ctx, q, a, thread)
	} else {
		ok, err = s.auth.CanReadThread(ctx, q, a, thread)
	}
	if err != nil {
		return err
	}
	if !ok {
		return platform.ErrForbidden
	}
	return nil
}
func (s *Service) publicPlace(ctx context.Context, q platform.Querier, id int64) (bool, error) {
	if s.ClosureThreshold < 1 || s.ClosureDecay <= 0 {
		return false, platform.ErrForbidden
	}
	var ok bool
	e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_place p WHERE p.id=$1 AND (p.source!='user' OR EXISTS(SELECT 1 FROM social_userplaceproposal u WHERE u.place_id=p.id AND u.status='published')) AND (SELECT count(*) FROM places_placeclosurereport c WHERE c.place_id=p.id AND c.created_at>=now()-($2::double precision*interval '1 second'))<$3)`, id, s.ClosureDecay.Seconds(), s.ClosureThreshold).Scan(&ok)
	return ok, e
}
func (s *Service) publicActivity(ctx context.Context, q platform.Querier, id int64) (bool, error) {
	var ok bool
	e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id WHERE a.id=$1 AND a.cohort='adult' AND a.is_publicly_listed AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=now() AND u.is_active)`, id).Scan(&ok)
	return ok, e
}
func (s *Service) activityVisible(ctx context.Context, q platform.Querier, a platform.Actor, id int64) error {
	if a.ID > 0 {
		fresh, e := currentMediaActor(ctx, q, a)
		if e != nil {
			return e
		}
		a = fresh
	}
	if a.IsStaff {
		return nil
	}
	if a.ID == 0 {
		ok, e := s.publicActivity(ctx, q, id)
		if e != nil {
			return e
		}
		if !ok {
			return platform.ErrForbidden
		}
		return nil
	}
	if s.auth == nil {
		return platform.ErrForbidden
	}
	ok, e := s.auth.CanSeeActivity(ctx, q, a, id)
	if e != nil {
		return e
	}
	if !ok {
		return platform.ErrForbidden
	}
	return nil
}

// ReadUpload streams one bounded multipart file into private scratch. A caller
// chooses the body ceiling for its exact surface; metadata fields are bounded.
func (s *Service) ReadUpload(w http.ResponseWriter, r *http.Request, maxBytes int64) (path, filename string, fields map[string]string, err error) {
	if s.processor == nil || maxBytes < 1 || maxBytes > 80<<20 {
		return "", "", nil, ErrProcessing
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+(1<<20))
	reader, e := r.MultipartReader()
	if e != nil {
		return "", "", nil, platform.ErrInvalid
	}
	fields = map[string]string{}
	var file *os.File
	defer func() {
		if file != nil {
			file.Close()
		}
		if err != nil && path != "" {
			os.Remove(path)
		}
	}()
	for count := 0; ; count++ {
		if count > 12 {
			return path, filename, fields, platform.ErrInvalid
		}
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return path, filename, fields, platform.ErrInvalid
		}
		name := part.FormName()
		if name == "file" {
			if file != nil || part.FileName() == "" {
				part.Close()
				return path, filename, fields, platform.ErrInvalid
			}
			file, e = os.CreateTemp(s.processor.cfg.ScratchDir, "upload-")
			if e != nil {
				part.Close()
				return path, filename, fields, ErrProcessing
			}
			path = file.Name()
			filename = filepath.Base(part.FileName())
			n, e := io.Copy(file, io.LimitReader(&contextReader{ctx: r.Context(), r: part}, maxBytes+1))
			part.Close()
			if e != nil || n == 0 || n > maxBytes {
				return path, filename, fields, ErrRejected
			}
			if e = file.Close(); e != nil {
				return path, filename, fields, ErrProcessing
			}
		} else {
			if name == "" || len(name) > 40 || len(fields) > 8 || part.FileName() != "" {
				part.Close()
				return path, filename, fields, platform.ErrInvalid
			}
			b, e := io.ReadAll(io.LimitReader(part, 2049))
			part.Close()
			if e != nil || len(b) > 2048 {
				return path, filename, fields, platform.ErrInvalid
			}
			if _, exists := fields[name]; exists {
				return path, filename, fields, platform.ErrInvalid
			}
			fields[name] = string(b)
		}
	}
	if file == nil {
		return "", "", fields, platform.ErrInvalid
	}
	return path, filename, fields, nil
}

type Photo struct {
	ID          int64     `json:"id"`
	Kind        string    `json:"kind"`
	Thread      *int64    `json:"thread"`
	ContentType string    `json:"content_type"`
	ByteSize    int64     `json:"byte_size"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	ScanStatus  string    `json:"scan_status"`
	CreatedAt   time.Time `json:"created_at"`
	URL         string    `json:"url"`
	owner       int64
	key, thumb  string
}

const photoColumns = `id,kind,thread_id,content_type,byte_size,width,height,scan_status,created_at,uploader_id,storage_key,thumb_storage_key`

func photoScan(row pgx.Row) (Photo, error) {
	var p Photo
	e := row.Scan(&p.ID, &p.Kind, &p.Thread, &p.ContentType, &p.ByteSize, &p.Width, &p.Height, &p.ScanStatus, &p.CreatedAt, &p.owner, &p.key, &p.thumb)
	return p, e
}
func (s *Service) photoAllowed(ctx context.Context, q platform.Querier, a platform.Actor, p Photo) error {
	fresh, e := currentMediaActor(ctx, q, a)
	if e != nil {
		return e
	}
	a = fresh
	if p.ScanStatus != "clean" || p.key == "" {
		return platform.ErrForbidden
	}
	if p.Kind == "thread" && p.Thread != nil {
		blocked, e := platform.Blocked(ctx, q, a.ID, p.owner)
		if e != nil {
			return e
		}
		if blocked {
			return platform.ErrForbidden
		}
		return s.approved(ctx, q, a, *p.Thread, false)
	}
	if p.owner == a.ID {
		return nil
	}
	if s.auth == nil {
		return platform.ErrForbidden
	}
	ok, e := s.auth.CanViewProfilePhoto(ctx, q, a, p.owner)
	if e != nil {
		return e
	}
	if !ok {
		return platform.ErrForbidden
	}
	return nil
}
func (s *Service) url(kind string, id int64, a platform.Actor, variant string, expires *time.Time) (string, error) {
	now := time.Now()
	deadline := now.Add(5 * time.Minute)
	if expires != nil && expires.Before(deadline) {
		deadline = *expires
	}
	ref := Reference{Key: fmt.Sprintf("%s/%d", kind, id), ViewerID: a.ID, Variant: variant, Expires: deadline.Unix(), Public: a.ID == 0}
	token, e := s.tokens.Sign(ref, now)
	if e != nil {
		return "", e
	}
	route := map[string]string{"photo": "file", "attachment": "attachment", "activity-cover": "activity-cover-file", "place-cover": "place-cover-file"}[kind]
	if route == "" {
		return "", ErrObject
	}
	return "/api/media/" + route + "/" + token + "/", nil
}
func (s *Service) UploadPhoto(ctx context.Context, a platform.Actor, kind string, threadID int64, path string) (p Photo, err error) {
	defer func() { s.recordFailure(ctx, a, kind, err) }()
	if kind != "profile" && kind != "thread" {
		return p, platform.ErrInvalid
	}
	if kind == "thread" {
		if err = s.approved(ctx, s.db, a, threadID, true); err != nil {
			return p, err
		}
	}
	if s.processor == nil || s.storage == nil {
		return p, ErrProcessing
	}
	if kind == "profile" {
		if err = s.avatarAttempt(ctx, a); err != nil {
			return p, err
		}
	}
	m, err := s.processor.ProcessImage(ctx, path)
	if err != nil {
		return p, err
	}
	defer m.Cleanup()
	key, err := s.storeArtifact(ctx, "photos", m.Main)
	if err != nil {
		return p, err
	}
	thumb := ""
	if m.Thumbnail != nil {
		thumb, err = s.storeArtifact(ctx, "photos", *m.Thumbnail)
		if err != nil {
			s.discard(ctx, key)
			return p, err
		}
	}
	published := false
	defer func() {
		if !published {
			s.discard(ctx, key, thumb)
		}
	}()
	err = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		if kind == "thread" {
			if e := s.approved(ctx, tx, a, threadID, true); e != nil {
				return e
			}
		}
		if kind == "profile" {
			if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951211)`); e != nil {
				return e
			}
			var changes int
			if e := tx.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE actor_id=$1 AND event='media.uploaded' AND data->>'kind'='profile' AND created_at>now()-interval '1 hour'`, a.ID).Scan(&changes); e != nil {
				return e
			}
			if changes >= 20 {
				return ErrThrottled
			}
			rows, e := tx.Query(ctx, `SELECT sha256,phash FROM media_photo WHERE kind='profile' AND uploader_id!=$1 ORDER BY created_at DESC LIMIT 10000`, a.ID)
			if e != nil {
				return e
			}
			duplicate := false
			for rows.Next() {
				var digest, phash string
				if e = rows.Scan(&digest, &phash); e != nil {
					rows.Close()
					return e
				}
				if digest == m.Main.SHA256 {
					duplicate = true
				}
				if m.PerceptualHash != "" && phash != "" {
					distance, e := Hamming(m.PerceptualHash, phash)
					if e == nil && distance <= 8 {
						duplicate = true
					}
				}
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return e
			}
			if duplicate {
				return platform.ErrInvalid
			}
			var oldKey, oldThumb string
			e = tx.QueryRow(ctx, `SELECT storage_key,thumb_storage_key FROM media_photo WHERE uploader_id=$1 AND kind='profile' FOR UPDATE`, a.ID).Scan(&oldKey, &oldThumb)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			if e == nil {
				if _, e = tx.Exec(ctx, `DELETE FROM media_photo WHERE uploader_id=$1 AND kind='profile'`, a.ID); e != nil {
					return e
				}
				if e = queueDelete(ctx, tx, oldKey, oldThumb); e != nil {
					return e
				}
			}
		}
		var thread any
		if kind == "thread" {
			thread = threadID
		}
		if e := tx.QueryRow(ctx, `INSERT INTO media_photo(kind,thread_id,uploader_id,storage_key,thumb_storage_key,content_type,byte_size,sha256,phash,width,height,scan_status,exif_stripped,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,'clean',true,now()) RETURNING `+photoColumns, kind, thread, a.ID, key, thumb, m.Main.ContentType, m.Main.ByteSize, m.Main.SHA256, m.PerceptualHash, m.Main.Width, m.Main.Height).Scan(&p.ID, &p.Kind, &p.Thread, &p.ContentType, &p.ByteSize, &p.Width, &p.Height, &p.ScanStatus, &p.CreatedAt, &p.owner, &p.key, &p.thumb); e != nil {
			return e
		}
		if e := saveManifest(ctx, tx, "photo", p.ID, m); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "media.uploaded", fmt.Sprintf("media.photo:%d", p.ID), map[string]any{"kind": kind, "source_sha256": m.SourceSHA256})
	})
	if err == nil {
		published = true
		p.URL, err = s.url("photo", p.ID, a, "main", nil)
	}
	return p, err
}
func (s *Service) avatarAttempt(ctx context.Context, a platform.Actor) error {
	return platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('media-avatar:'||$1::bigint::text,0))`, a.ID); e != nil {
			return e
		}
		var n int
		if e := tx.QueryRow(ctx, `SELECT count(*) FROM media_go_avatarattempt WHERE user_id=$1 AND created_at>now()-interval '1 hour'`, a.ID).Scan(&n); e != nil {
			return e
		}
		if n >= 20 {
			return ErrThrottled
		}
		if _, e := tx.Exec(ctx, `DELETE FROM media_go_avatarattempt WHERE user_id=$1 AND created_at<=now()-interval '1 hour'`, a.ID); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO media_go_avatarattempt(user_id) VALUES($1)`, a.ID)
		return e
	})
}

type Cover struct {
	ID          int64     `json:"id"`
	Activity    int64     `json:"activity"`
	ContentType string    `json:"content_type"`
	ByteSize    int64     `json:"byte_size"`
	Width       int       `json:"width"`
	Height      int       `json:"height"`
	AltText     string    `json:"alt_text"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	URL         string    `json:"url"`
	key, thumb  string
}

const coverColumns = `id,activity_id,content_type,byte_size,width,height,alt_text,created_at,updated_at,storage_key,thumb_storage_key`

func coverScan(row pgx.Row) (Cover, error) {
	var c Cover
	e := row.Scan(&c.ID, &c.Activity, &c.ContentType, &c.ByteSize, &c.Width, &c.Height, &c.AltText, &c.CreatedAt, &c.UpdatedAt, &c.key, &c.thumb)
	return c, e
}
func (s *Service) manageCover(ctx context.Context, q platform.Querier, a platform.Actor, id int64, upload bool) error {
	fresh, e := currentMediaActor(ctx, q, a)
	if e != nil {
		return e
	}
	a = fresh
	var owner int64
	var open bool
	e = q.QueryRow(ctx, `SELECT owner_id,status='open' AND NOT is_hidden AND starts_at>now() FROM social_activity WHERE id=$1`, id).Scan(&owner, &open)
	if e != nil {
		return e
	}
	if a.ID != owner && !a.IsStaff {
		return platform.ErrForbidden
	}
	if upload && !open {
		return platform.ErrForbidden
	}
	return nil
}
func (s *Service) UploadActivityCover(ctx context.Context, a platform.Actor, activityID int64, path, alt string) (c Cover, err error) {
	defer func() { s.recordFailure(ctx, a, "activity_cover", err) }()
	if err = s.manageCover(ctx, s.db, a, activityID, true); err != nil {
		return c, err
	}
	m, err := s.processor.ProcessImage(ctx, path)
	if err != nil {
		return c, err
	}
	defer m.Cleanup()
	key, err := s.storeArtifact(ctx, "activity-covers", m.Main)
	if err != nil {
		return c, err
	}
	thumb := ""
	if m.Thumbnail != nil {
		thumb, err = s.storeArtifact(ctx, "activity-covers", *m.Thumbnail)
		if err != nil {
			s.discard(ctx, key)
			return c, err
		}
	}
	published := false
	defer func() {
		if !published {
			s.discard(ctx, key, thumb)
		}
	}()
	alt = trimText(alt, 140)
	err = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT id FROM social_activity WHERE id=$1 FOR UPDATE`, activityID); e != nil {
			return e
		}
		if e := s.manageCover(ctx, tx, a, activityID, true); e != nil {
			return e
		}
		var old, oldThumb string
		e := tx.QueryRow(ctx, `SELECT storage_key,thumb_storage_key FROM media_activitycover WHERE activity_id=$1 FOR UPDATE`, activityID).Scan(&old, &oldThumb)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		c, e = coverScan(tx.QueryRow(ctx, `INSERT INTO media_activitycover(activity_id,uploader_id,storage_key,thumb_storage_key,content_type,byte_size,sha256,width,height,exif_stripped,alt_text,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,true,$10,now(),now()) ON CONFLICT(activity_id) DO UPDATE SET uploader_id=excluded.uploader_id,storage_key=excluded.storage_key,thumb_storage_key=excluded.thumb_storage_key,content_type=excluded.content_type,byte_size=excluded.byte_size,sha256=excluded.sha256,width=excluded.width,height=excluded.height,exif_stripped=true,alt_text=excluded.alt_text,updated_at=now() RETURNING `+coverColumns, activityID, a.ID, key, thumb, m.Main.ContentType, m.Main.ByteSize, m.Main.SHA256, m.Main.Width, m.Main.Height, alt))
		if e != nil {
			return e
		}
		if e = queueDelete(ctx, tx, old, oldThumb); e != nil {
			return e
		}
		if e = saveManifest(ctx, tx, "activity-cover", c.ID, m); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "media.activity_cover_uploaded", fmt.Sprintf("social.activity:%d", activityID), map[string]any{"cover_id": c.ID, "source_sha256": m.SourceSHA256})
	})
	if err == nil {
		published = true
		c.URL, err = s.url("activity-cover", c.ID, a, "main", nil)
	}
	return c, err
}
func trimText(s string, limit int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > limit {
		r = r[:limit]
	}
	return string(r)
}
func (s *Service) ActivityVisual(ctx context.Context, q platform.Querier, a platform.Actor, id int64) (any, error) {
	fallback := map[string]any{"kind": "generated_accent"}
	var coverID int64
	var alt, title, key string
	e := q.QueryRow(ctx, `SELECT c.id,c.alt_text,a.title,c.storage_key FROM media_activitycover c JOIN social_activity a ON a.id=c.activity_id WHERE a.id=$1`, id).Scan(&coverID, &alt, &title, &key)
	if errors.Is(e, pgx.ErrNoRows) {
		return fallback, nil
	}
	if e != nil {
		return nil, e
	}
	if key == "" {
		return fallback, nil
	}
	if e = s.activityVisible(ctx, q, a, id); e != nil {
		if errors.Is(e, platform.ErrForbidden) {
			return fallback, nil
		}
		return nil, e
	}
	url, e := s.url("activity-cover", coverID, a, "thumb", nil)
	if e != nil {
		return nil, e
	}
	if alt == "" {
		alt = title
	}
	return map[string]any{"kind": "activity_cover_photo", "url": url, "alt": alt}, nil
}

// ActivityVisuals keeps feed/card media to one bounded cover+visibility query.
// The ordinary authenticated clause belongs to the social authorization owner;
// media adds only the existing staff/public-cover rules. No caller-supplied SQL.
func (s *Service) ActivityVisuals(ctx context.Context, q platform.Querier, a platform.Actor, ids []int64) (map[int64]any, error) {
	out := map[int64]any{}
	if len(ids) > 200 {
		return nil, platform.ErrInvalid
	}
	for _, id := range ids {
		if id <= 0 {
			return nil, platform.ErrInvalid
		}
		out[id] = map[string]any{"kind": "generated_accent"}
	}
	if len(ids) == 0 {
		return out, nil
	}
	clause := "false"
	if a.ID > 0 {
		owner, ok := s.auth.(interface{ ActivityCoverVisibilitySQL() string })
		if !ok {
			return nil, platform.ErrForbidden
		}
		clause = owner.ActivityCoverVisibilitySQL()
		if strings.TrimSpace(clause) == "" {
			return nil, platform.ErrForbidden
		}
	}
	rows, e := q.Query(ctx, `SELECT c.activity_id,c.id,c.alt_text,a.title FROM media_activitycover c JOIN social_activity a ON a.id=c.activity_id JOIN accounts_user u ON u.id=a.owner_id LEFT JOIN accounts_user v ON v.id=$2 WHERE a.id=ANY($1) AND c.storage_key!='' AND (($2::bigint=0 AND a.cohort='adult' AND a.is_publicly_listed AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=now() AND u.is_active) OR ($2::bigint>0 AND v.is_active AND (v.is_staff OR (`+clause+`))))`, ids, a.ID)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var activityID, coverID int64
		var alt, title string
		if e = rows.Scan(&activityID, &coverID, &alt, &title); e != nil {
			return nil, e
		}
		url, e := s.url("activity-cover", coverID, a, "thumb", nil)
		if e != nil {
			return nil, e
		}
		if alt == "" {
			alt = title
		}
		out[activityID] = map[string]any{"kind": "activity_cover_photo", "url": url, "alt": alt}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return out, nil
}
func rowID(r *http.Request, name string) (int64, error) {
	n, e := strconv.ParseInt(r.PathValue(name), 10, 64)
	if e != nil || n <= 0 {
		return 0, platform.ErrInvalid
	}
	return n, nil
}
func mediaFail(w http.ResponseWriter, e error) {
	if errors.Is(e, ErrThrottled) {
		w.Header().Set("Retry-After", "3600")
		platform.Error(w, 429, "Too many avatar changes; please try again later.")
		return
	}
	if errors.Is(e, ErrBlocked) || errors.Is(e, ErrRejected) {
		platform.Error(w, 403, "Media failed safety or format checks.")
		return
	}
	if errors.Is(e, ErrScanner) || errors.Is(e, ErrProcessing) || errors.Is(e, ErrObject) {
		platform.Error(w, 503, "Media processing is unavailable.")
		return
	}
	platform.Fail(w, e)
}
func (s *Service) recordFailure(ctx context.Context, a platform.Actor, kind string, cause error) {
	if s.db == nil || cause == nil {
		return
	}
	reason := ""
	data := map[string]any{"kind": kind}
	if errors.Is(cause, ErrScanner) {
		reason = "no_scanner"
	}
	var blocked *BlockedError
	if errors.As(cause, &blocked) {
		reason = "scan_match"
		data["sha256"] = blocked.SourceSHA256
	}
	if reason == "" {
		return
	}
	data["reason"] = reason
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = platform.Transaction(cleanup, s.db, func(tx pgx.Tx) error { return platform.RecordAudit(cleanup, tx, a, "media.upload_blocked", "", data) })
}
