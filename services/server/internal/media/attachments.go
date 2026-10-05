package media

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

type Attachment struct {
	ID                                                int64      `json:"id"`
	PostID                                            int64      `json:"post"`
	Kind                                              string     `json:"kind"`
	Status                                            string     `json:"status"`
	ContentType                                       string     `json:"content_type"`
	ByteSize                                          int64      `json:"byte_size"`
	Width                                             int        `json:"width"`
	Height                                            int        `json:"height"`
	OriginalFilename                                  string     `json:"original_filename"`
	DurationSeconds                                   int        `json:"duration_seconds"`
	ExpiresAt                                         *time.Time `json:"expires_at"`
	PurgedAt                                          *time.Time `json:"purged_at"`
	CreatedAt                                         time.Time  `json:"created_at"`
	URL                                               string     `json:"url"`
	ThumbURL                                          string     `json:"thumb_url"`
	PosterURL                                         string     `json:"poster_url"`
	Blocked                                           bool       `json:"blocked"`
	Processing                                        bool       `json:"processing"`
	Failed                                            bool       `json:"failed"`
	Expired                                           bool       `json:"expired"`
	owner, thread, activity, group                    int64
	key, thumb, poster, posterType, sourceKey, digest string
	hidden                                            bool
	attempts                                          int
	started                                           *time.Time
}

const attachmentColumns = `a.id,a.post_id,a.kind,a.status,a.content_type,a.byte_size,a.width,a.height,a.original_filename,a.duration_seconds,a.expires_at,a.purged_at,a.created_at,a.uploader_id,p.thread_id,COALESCE(t.activity_id,0),COALESCE(t.group_id,0),a.storage_key,a.thumb_storage_key,a.poster_storage_key,a.poster_content_type,a.source_storage_key,a.sha256,p.is_hidden,a.processing_attempts,a.processing_started_at`

func attachmentScan(row pgx.Row) (Attachment, error) {
	var a Attachment
	e := row.Scan(&a.ID, &a.PostID, &a.Kind, &a.Status, &a.ContentType, &a.ByteSize, &a.Width, &a.Height, &a.OriginalFilename, &a.DurationSeconds, &a.ExpiresAt, &a.PurgedAt, &a.CreatedAt, &a.owner, &a.thread, &a.activity, &a.group, &a.key, &a.thumb, &a.poster, &a.posterType, &a.sourceKey, &a.digest, &a.hidden, &a.attempts, &a.started)
	return a, e
}
func (s *Service) attachment(ctx context.Context, q platform.Querier, id int64, lock bool) (Attachment, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF a,p"
	}
	return attachmentScan(q.QueryRow(ctx, `SELECT `+attachmentColumns+` FROM media_attachment a JOIN social_post p ON p.id=a.post_id JOIN social_thread t ON t.id=p.thread_id WHERE a.id=$1`+suffix, id))
}
func (s *Service) attachmentAllowed(ctx context.Context, q platform.Querier, a platform.Actor, att Attachment) error {
	fresh, e := currentMediaActor(ctx, q, a)
	if e != nil {
		return e
	}
	a = fresh
	// Quarantined sources are never served through any in-app path, even staff.
	if att.Status != "ready" || att.key == "" || att.PurgedAt != nil {
		return platform.ErrForbidden
	}
	if att.ExpiresAt != nil && !att.ExpiresAt.After(time.Now()) {
		return platform.ErrForbidden
	}
	if att.hidden && !a.IsStaff {
		return platform.ErrForbidden
	}
	if !a.IsStaff {
		blocked, e := platform.Blocked(ctx, q, a.ID, att.owner)
		if e != nil {
			return e
		}
		if blocked {
			return platform.ErrForbidden
		}
		return s.approved(ctx, q, a, att.thread, false)
	}
	return nil
}
func sanitizeFilename(name string) string {
	name = filepathBase(name)
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._ -", r) {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	name = trimText(b.String(), 116)
	if name == "" {
		name = "document"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}
	return name
}
func filepathBase(s string) string {
	s = strings.ReplaceAll(s, "\\", "/")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return s
}
func expiry(cohort string, ttl *int64) *time.Time {
	return expiryWithPolicy(DefaultPolicyConfig(), cohort, ttl)
}
func expiryWithPolicy(policy PolicyConfig, cohort string, ttl *int64) *time.Time {
	if ttl == nil || *ttl <= 0 {
		return nil
	}
	floor := int64(policy.EphemeralMinTTL / time.Second)
	if cohort == "child" || cohort == "teen" {
		floor = int64(policy.EphemeralMinTTLMinors / time.Second)
	}
	value := max(floor, *ttl)
	if value > 10*365*86400 {
		value = 10 * 365 * 86400
	}
	t := time.Now().Add(time.Duration(value) * time.Second)
	return &t
}
func (s *Service) postUploadGate(ctx context.Context, q platform.Querier, a platform.Actor, postID int64) (thread int64, cohort string, err error) {
	var owner int64
	var hidden bool
	err = q.QueryRow(ctx, `SELECT p.author_id,p.is_hidden,p.thread_id,COALESCE(act.cohort,g.cohort,'') FROM social_post p JOIN social_thread t ON t.id=p.thread_id LEFT JOIN social_activity act ON act.id=t.activity_id LEFT JOIN social_group g ON g.id=t.group_id WHERE p.id=$1`, postID).Scan(&owner, &hidden, &thread, &cohort)
	if err != nil {
		return
	}
	if owner != a.ID || hidden {
		err = platform.ErrForbidden
		return
	}
	err = s.approved(ctx, q, a, thread, true)
	return
}
func sniffFile(path string) (string, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", ErrRejected
	}
	defer f.Close()
	var head [16]byte
	n, e := f.Read(head[:])
	if e != nil && e != io.EOF {
		return "", ErrRejected
	}
	if n >= 5 && string(head[:5]) == "%PDF-" {
		return "file", nil
	}
	if n >= 12 && (string(head[4:8]) == "ftyp" && string(head[8:12]) != "avif" && string(head[8:12]) != "avis" && string(head[8:12]) != "mif1" || string(head[:4]) == "\x1a\x45\xdf\xa3") {
		return "video", nil
	}
	return "image", nil
}

// AttachToPost attaches a file to an already-published post. Production code does
// not call it: the web thread handler uses PrepareThreadAttachment and publishes
// inside the post-creating transaction. Tests use it as a direct attachment path.
// It authorizes twice: before codec/storage work and in the publishing transaction.
// Video admission stores only quarantined source bytes and a pending row, then a
// worker finalizes.
func (s *Service) AttachToPost(ctx context.Context, a platform.Actor, postID int64, path, filename string, ttl *int64) (att Attachment, err error) {
	defer func() { s.recordFailure(ctx, a, "attachment", err) }()
	_, cohort, err := s.postUploadGate(ctx, s.db, a, postID)
	if err != nil {
		return att, err
	}
	kind, err := sniffFile(path)
	if err != nil {
		return att, err
	}
	if !s.attachmentModeAllowed(kind, cohort) {
		return att, platform.ErrForbidden
	}
	if kind == "video" && !s.processor.cfg.VideoEnabled {
		return att, platform.ErrForbidden
	}
	var m Manifest
	var sourceDir string
	if kind == "image" {
		m, err = s.processor.ProcessImage(ctx, path)
	} else if kind == "file" {
		m, err = s.processor.ProcessPDF(ctx, path)
	} else {
		var source, digest string
		var size int64
		sourceDir, source, digest, size, err = s.processor.stage(path, s.processor.cfg.VideoMaxBytes)
		if err == nil {
			err = s.processor.scan(ctx, digest, "")
		}
		if err == nil {
			m = Manifest{SourceSHA256: digest, SourceByteSize: size, Kind: "video", Status: "pending", PolicyVersion: PolicyVersion, ScannerClean: true, Main: Artifact{Path: source, SHA256: digest, ByteSize: size, ContentType: "application/octet-stream"}}
		}
	}
	if sourceDir != "" {
		defer cleanupDir(sourceDir)
	}
	if err != nil {
		return att, err
	}
	if kind != "video" {
		defer m.Cleanup()
	}
	key, err := s.storeArtifact(ctx, "attachments", m.Main)
	if err != nil {
		return att, err
	}
	thumb := ""
	if m.Thumbnail != nil {
		thumb, err = s.storeArtifact(ctx, "attachments", *m.Thumbnail)
		if err != nil {
			s.discard(ctx, key)
			return att, err
		}
	}
	published := false
	defer func() {
		if !published {
			s.discard(ctx, key, thumb)
		}
	}()
	err = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT id FROM social_post WHERE id=$1 FOR UPDATE`, postID); e != nil {
			return e
		}
		_, freshCohort, e := s.postUploadGate(ctx, tx, a, postID)
		if e != nil {
			return e
		}
		if kind != "image" && freshCohort != "adult" {
			return platform.ErrForbidden
		}
		exp := s.attachmentExpiry(freshCohort, ttl)
		mainKey, sourceKey, status, mime := key, "", "ready", m.Main.ContentType
		if kind == "video" {
			mainKey, sourceKey, status, mime = "", key, "pending", "video/mp4"
		}
		display := ""
		if kind == "file" {
			display = sanitizeFilename(filename)
		}
		var id int64
		e = tx.QueryRow(ctx, `INSERT INTO media_attachment(post_id,uploader_id,kind,status,storage_key,thumb_storage_key,poster_storage_key,poster_content_type,source_storage_key,content_type,byte_size,sha256,original_filename,width,height,exif_stripped,duration_seconds,processing_attempts,processing_started_at,expires_at,purged_at,created_at) VALUES($1,$2,$3,$4,$5,$6,'','',$7,$8,$9,$10,$11,$12,$13,$14,0,0,NULL,$15,NULL,now()) RETURNING id`, postID, a.ID, kind, status, mainKey, thumb, sourceKey, mime, m.Main.ByteSize, m.Main.SHA256, display, m.Main.Width, m.Main.Height, m.MetadataStripped, exp).Scan(&id)
		if e != nil {
			return e
		}
		att, e = s.attachment(ctx, tx, id, false)
		if e != nil {
			return e
		}
		if e = saveManifest(ctx, tx, "attachment", id, minimisedManifest(m, false)); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "media.attached", fmt.Sprintf("media.attachment:%d", id), map[string]any{"kind": kind})
	})
	if err != nil {
		return att, err
	}
	published = true
	if kind == "video" {
		s.kickVideoProcessing()
	}
	return s.attachmentURLs(ctx, s.db, a, att)
}
func (s *Service) attachmentURLs(ctx context.Context, q platform.Querier, a platform.Actor, att Attachment) (Attachment, error) {
	att.Blocked = att.Status == "blocked"
	att.Processing = att.Status == "pending" || att.Status == "processing"
	att.Failed = att.Status == "failed"
	att.Expired = att.PurgedAt != nil || att.ExpiresAt != nil && !att.ExpiresAt.After(time.Now())
	if att.Status != "ready" || att.Expired {
		return att, nil
	}
	if e := s.attachmentAllowed(ctx, q, a, att); e != nil {
		return att, e
	}
	var e error
	att.URL, e = s.url("attachment", att.ID, a, "main", att.ExpiresAt)
	if e != nil {
		return att, e
	}
	if att.Kind == "image" {
		att.ThumbURL, e = s.url("attachment", att.ID, a, "thumb", att.ExpiresAt)
	}
	if att.Kind == "video" && att.poster != "" {
		att.PosterURL, e = s.url("attachment", att.ID, a, "poster", att.ExpiresAt)
	}
	return att, e
}
func (s *Service) ForPosts(ctx context.Context, a platform.Actor, postIDs []int64) (map[int64][]Attachment, error) {
	out := map[int64][]Attachment{}
	if len(postIDs) == 0 {
		return out, nil
	}
	if len(postIDs) > 200 {
		return nil, platform.ErrInvalid
	}
	fresh, e := currentMediaActor(ctx, s.db, a)
	if e != nil {
		return nil, e
	}
	a = fresh
	rows, e := s.db.Query(ctx, `SELECT `+attachmentColumns+` FROM media_attachment a JOIN social_post p ON p.id=a.post_id JOIN social_thread t ON t.id=p.thread_id WHERE a.post_id=ANY($1) AND (NOT p.is_hidden OR $2) AND (a.status!='blocked' OR $2) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$3 AND b.blocked_id=a.uploader_id) OR (b.blocker_id=a.uploader_id AND b.blocked_id=$3)) ORDER BY a.created_at LIMIT 1001`, postIDs, a.IsStaff, a.ID)
	if e != nil {
		return nil, e
	}
	var attachments []Attachment
	for rows.Next() {
		att, e := attachmentScan(rows)
		if e != nil {
			rows.Close()
			return nil, e
		}
		attachments = append(attachments, att)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if len(attachments) > 1000 {
		return nil, platform.ErrInvalid
	}
	authorized := map[int64]bool{}
	for _, att := range attachments {
		if !a.IsStaff && !authorized[att.thread] {
			if e = s.approved(ctx, s.db, a, att.thread, false); e != nil {
				if errors.Is(e, platform.ErrForbidden) {
					continue
				}
				return nil, e
			}
			authorized[att.thread] = true
		}
		att, e = s.attachmentURLs(ctx, s.db, a, att)
		if e != nil {
			if errors.Is(e, platform.ErrForbidden) {
				continue
			}
			return nil, e
		}
		out[att.PostID] = append(out[att.PostID], att)
	}
	return out, nil
}
func (s *Service) DeleteAttachment(ctx context.Context, a platform.Actor, id int64) error {
	return platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		fresh, e := currentMediaActor(ctx, tx, a)
		if e != nil {
			return e
		}
		a = fresh
		att, e := s.attachment(ctx, tx, id, true)
		if e != nil {
			return e
		}
		if a.ID != att.owner && !a.IsStaff {
			return platform.ErrForbidden
		}
		if e = queueDelete(ctx, tx, att.key, att.thumb, att.poster, att.sourceKey); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM media_attachment WHERE id=$1`, id); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "media.attachment_deleted", fmt.Sprintf("media.attachment:%d", id), nil)
	})
}

func loadMediaActor(ctx context.Context, q platform.Querier, id int64) (platform.Actor, error) {
	var a platform.Actor
	e := q.QueryRow(ctx, `SELECT id,public_id::text,username,display_name,age_band,cohort,role,is_identity_verified,is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1`, id).Scan(&a.ID, &a.PublicID, &a.Username, &a.DisplayName, &a.AgeBand, &a.Cohort, &a.Role, &a.IdentityVerified, &a.IsActive, &a.IsStaff, &a.IsSuperuser)
	return a, e
}
func currentMediaActor(ctx context.Context, q platform.Querier, a platform.Actor) (platform.Actor, error) {
	if a.ID < 1 {
		return platform.Actor{}, platform.ErrForbidden
	}
	fresh, e := loadMediaActor(ctx, q, a.ID)
	if e != nil {
		return platform.Actor{}, e
	}
	if !fresh.IsActive {
		return platform.Actor{}, platform.ErrForbidden
	}
	return fresh, nil
}
func (s *Service) ProcessPendingVideos(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 50 {
		return 0, platform.ErrInvalid
	}
	if !s.processor.scannerEffective() {
		return 0, nil
	}
	completed := 0
	for n := 0; n < limit; n++ {
		// Never claim a video the caller's deadline cannot finish: a cut encode
		// spends one of its attempts and the last one erases a valid upload.
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < s.policy.VideoStaleProcessing {
			break
		}
		var att Attachment
		terminal := false
		err := platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
			var id int64
			// An exhausted stale lease stays held for operator recovery; fence it
			// out of the claim so the oldest held row cannot block later videos.
			e := tx.QueryRow(ctx, `SELECT id FROM media_attachment WHERE kind='video' AND purged_at IS NULL AND source_storage_key!='' AND (status='pending' OR (status='processing' AND processing_started_at<now()-$1*interval '1 second' AND processing_attempts<$2)) ORDER BY created_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, s.policy.VideoStaleProcessing.Seconds(), s.policy.VideoMaxAttempts).Scan(&id)
			if e != nil {
				return e
			}
			fresh, e := s.attachment(ctx, tx, id, false)
			if e != nil {
				return e
			}
			if fresh.attempts >= s.policy.VideoMaxAttempts {
				// A stale lease has no committed failure outcome. In particular,
				// a failed audit transaction may have rolled back scanner deferral.
				// Preserve its evidence for operator recovery instead of inferring
				// a terminal content failure from the last claim's debit. The
				// claim already fences these rows; this re-check stays defensive.
				if fresh.Status == "processing" {
					return ErrProcessing
				}
				terminal = true
				if e = queueDelete(ctx, tx, fresh.key, fresh.thumb, fresh.poster, fresh.sourceKey); e != nil {
					return e
				}
				if _, e = tx.Exec(ctx, `UPDATE media_attachment SET status='failed',storage_key='',thumb_storage_key='',poster_storage_key='',source_storage_key='',processing_started_at=NULL WHERE id=$1`, id); e != nil {
					return e
				}
				if e = platform.RecordAudit(ctx, tx, platform.Actor{}, "media.video_failed", fmt.Sprintf("media.attachment:%d", id), map[string]any{"reason": "attempts_exhausted"}); e != nil {
					return e
				}
				return chat.Publish(ctx, tx, chat.Event{Kind: "chat", Event: "attachments", RoomID: fresh.thread, MessageID: fresh.PostID})
			}
			if _, e = tx.Exec(ctx, `UPDATE media_attachment SET status='processing',processing_attempts=processing_attempts+1,processing_started_at=now() WHERE id=$1`, id); e != nil {
				return e
			}
			att, e = s.attachment(ctx, tx, id, false)
			return e
		})
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return completed, err
		}
		if terminal {
			continue
		}
		if err = s.processClaim(ctx, att); err != nil {
			if ctx.Err() != nil {
				return completed, ctx.Err()
			}
			if errors.Is(err, errClaimFinalization) {
				return completed, ErrProcessing
			}
			if errors.Is(err, ErrScanner) {
				return completed, nil
			}
			continue
		}
		completed++
	}
	return completed, nil
}
func (s *Service) download(ctx context.Context, key string, size int64) (path string, err error) {
	if size <= 0 || size > 80<<20 {
		return "", ErrObject
	}
	file, e := os.CreateTemp(s.processor.cfg.ScratchDir, "worker-source-")
	if e != nil {
		return "", ErrProcessing
	}
	path = file.Name()
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(path)
		}
	}()
	for start := int64(0); start < size; {
		end := min(size-1, start+(1<<20)-1)
		b, e := s.storage.OpenRange(ctx, key, start, end)
		if e != nil {
			return path, e
		}
		if _, e = file.Write(b); e != nil {
			return path, ErrProcessing
		}
		start = end + 1
	}
	return path, file.Close()
}
func (s *Service) processClaim(ctx context.Context, att Attachment) (err error) {
	// A live worker must finish before another worker may reclaim its stale row.
	ctx, cancel := context.WithTimeout(ctx, s.policy.VideoStaleProcessing-time.Second)
	defer cancel()
	path, err := s.download(ctx, att.sourceKey, att.ByteSize)
	if err != nil {
		return s.failClaim(ctx, att, err)
	}
	defer os.Remove(path)
	m, err := s.processor.ProcessVideo(ctx, path)
	if err != nil {
		return s.failClaim(ctx, att, err)
	}
	defer m.Cleanup()
	if m.SourceSHA256 != att.digest {
		return s.failClaim(ctx, att, ErrRejected)
	}
	key, err := s.storeArtifact(ctx, "attachments", m.Main)
	if err != nil {
		return s.failClaim(ctx, att, err)
	}
	poster, err := s.storeArtifact(ctx, "attachments", *m.Poster)
	if err != nil {
		s.discard(ctx, key)
		return s.failClaim(ctx, att, err)
	}
	published := false
	defer func() {
		if !published {
			s.discard(ctx, key, poster)
		}
	}()
	err = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		fresh, e := s.attachment(ctx, tx, att.ID, true)
		if e != nil {
			return e
		}
		if fresh.Status != "processing" || fresh.sourceKey != att.sourceKey || fresh.started == nil || att.started == nil || !fresh.started.Equal(*att.started) || fresh.PurgedAt != nil {
			return platform.ErrForbidden
		}
		owner, e := loadMediaActor(ctx, tx, att.owner)
		if e != nil {
			return e
		}
		_, cohort, e := s.postUploadGate(ctx, tx, owner, att.PostID)
		if e != nil {
			return e
		}
		if cohort != "adult" || fresh.ExpiresAt != nil && !fresh.ExpiresAt.After(time.Now()) {
			return platform.ErrForbidden
		}
		_, e = tx.Exec(ctx, `UPDATE media_attachment SET status='ready',storage_key=$2,poster_storage_key=$3,poster_content_type=$4,source_storage_key='',sha256=$5,byte_size=$6,width=$7,height=$8,duration_seconds=$9,exif_stripped=true,processing_started_at=NULL WHERE id=$1`, att.ID, key, poster, m.Poster.ContentType, m.Main.SHA256, m.Main.ByteSize, m.Main.Width, m.Main.Height, int(math.Ceil(m.DurationSeconds)))
		if e != nil {
			return e
		}
		if e = queueDelete(ctx, tx, att.sourceKey); e != nil {
			return e
		}
		if e = saveManifest(ctx, tx, "attachment", att.ID, minimisedManifest(m, false)); e != nil {
			return e
		}
		if e := platform.RecordAudit(ctx, tx, platform.Actor{}, "media.video_ready", fmt.Sprintf("media.attachment:%d", att.ID), map[string]any{"attachment_id": att.ID}); e != nil {
			return e
		}
		return chat.Publish(ctx, tx, chat.Event{Kind: "chat", Event: "attachments", RoomID: att.thread, MessageID: att.PostID})
	})
	if err != nil {
		return s.failClaim(ctx, att, err)
	}
	published = true
	return nil
}

var errClaimFinalization = errors.New("video claim finalization failed")

func (s *Service) failClaim(ctx context.Context, att Attachment, cause error) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	blocked := errors.Is(cause, ErrBlocked)
	err := platform.Transaction(cleanup, s.db, func(tx pgx.Tx) error {
		fresh, e := s.attachment(cleanup, tx, att.ID, true)
		if e != nil {
			return e
		}
		if fresh.Status != "processing" || fresh.started == nil || att.started == nil || !fresh.started.Equal(*att.started) {
			return nil
		}
		if errors.Is(cause, ErrScanner) {
			// Scanner availability is an admission prerequisite, not a content
			// failure. Restore this claim's debit and preserve quarantined bytes
			// so a provider outage cannot exhaust attempts or erase evidence.
			if _, e = tx.Exec(cleanup, `UPDATE media_attachment SET status='pending',processing_attempts=GREATEST(0,processing_attempts-1),processing_started_at=NULL WHERE id=$1`, att.ID); e != nil {
				return e
			}
			return platform.RecordAudit(cleanup, tx, platform.Actor{}, "media.video_deferred", fmt.Sprintf("media.attachment:%d", att.ID), map[string]string{"reason": "scanner_unavailable"})
		}
		status := "processing"
		if blocked {
			status = "blocked"
		} else if fresh.attempts >= s.policy.VideoMaxAttempts || errors.Is(cause, ErrRejected) || errors.Is(cause, platform.ErrForbidden) {
			status = "failed"
		}
		if status == "failed" {
			if e = queueDelete(cleanup, tx, fresh.key, fresh.thumb, fresh.poster, fresh.sourceKey); e != nil {
				return e
			}
			_, e = tx.Exec(cleanup, `UPDATE media_attachment SET status='failed',storage_key='',thumb_storage_key='',poster_storage_key='',source_storage_key='',processing_started_at=NULL WHERE id=$1`, att.ID)
		} else if status == "blocked" {
			_, e = tx.Exec(cleanup, `UPDATE media_attachment SET status='blocked',processing_started_at=NULL WHERE id=$1`, att.ID)
		}
		if e != nil {
			return e
		}
		if e := platform.RecordAudit(cleanup, tx, platform.Actor{}, "media.video_processing_failed", fmt.Sprintf("media.attachment:%d", att.ID), map[string]any{"blocked": blocked, "attempt": fresh.attempts, "status": status}); e != nil {
			return e
		}
		if status == "failed" || status == "blocked" {
			return chat.Publish(cleanup, tx, chat.Event{Kind: "chat", Event: "attachments", RoomID: fresh.thread, MessageID: fresh.PostID})
		}
		return nil
	})
	if err != nil {
		return errors.Join(errClaimFinalization, ErrProcessing)
	}
	return cause
}
