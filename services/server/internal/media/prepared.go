package media

import (
	"context"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"sync"
)

// PreparedAttachment owns scanned artifacts until the caller's domain transaction
// commits. Publish must share the transaction that creates the post; Finish(false)
// discards its private blobs when any later post/audit/notification step fails.
type PreparedAttachment struct {
	service                    *Service
	actor                      platform.Actor
	thread                     int64
	kind, key, thumb, filename string
	ttl                        *int64
	manifest                   Manifest
	sourceDir                  string
	mu                         sync.Mutex
	published                  bool
	finished                   bool
}

func (s *Service) PrepareThreadAttachment(ctx context.Context, a platform.Actor, thread int64, path, filename string, ttl *int64) (prepared *PreparedAttachment, err error) {
	defer func() { s.recordFailure(ctx, a, "attachment", err) }()
	if err = s.approved(ctx, s.db, a, thread, true); err != nil {
		return nil, err
	}
	var cohort string
	if err = s.db.QueryRow(ctx, `SELECT coalesce(act.cohort,g.cohort,'') FROM social_thread t LEFT JOIN social_activity act ON act.id=t.activity_id LEFT JOIN social_group g ON g.id=t.group_id WHERE t.id=$1`, thread).Scan(&cohort); err != nil {
		return nil, err
	}
	kind, err := sniffFile(path)
	if err != nil {
		return nil, err
	}
	if kind != "image" && cohort != "adult" || kind == "video" && !s.processor.cfg.VideoEnabled {
		return nil, platform.ErrForbidden
	}
	p := &PreparedAttachment{service: s, actor: a, thread: thread, kind: kind, filename: filename, ttl: ttl}
	if ttl != nil {
		value := *ttl
		p.ttl = &value
	}
	defer func() {
		if err != nil {
			p.Finish(ctx, false)
		}
	}()
	switch kind {
	case "image":
		p.manifest, err = s.processor.ProcessImage(ctx, path)
	case "file":
		p.manifest, err = s.processor.ProcessPDF(ctx, path)
	case "video":
		var source, digest string
		var size int64
		p.sourceDir, source, digest, size, err = s.processor.stage(path, s.processor.cfg.VideoMaxBytes)
		if err == nil {
			err = s.processor.scan(ctx, digest, "")
		}
		if err == nil {
			p.manifest = Manifest{SourceSHA256: digest, SourceByteSize: size, Kind: "video", Status: "pending", PolicyVersion: PolicyVersion, ScannerClean: true, Main: Artifact{Path: source, SHA256: digest, ByteSize: size, ContentType: "application/octet-stream"}}
		}
	}
	if err != nil {
		return nil, err
	}
	p.key, err = s.storeArtifact(ctx, "attachments", p.manifest.Main)
	if err != nil {
		return nil, err
	}
	if p.manifest.Thumbnail != nil {
		p.thumb, err = s.storeArtifact(ctx, "attachments", *p.manifest.Thumbnail)
		if err != nil {
			return nil, err
		}
	}
	return p, nil
}
func (p *PreparedAttachment) Publish(ctx context.Context, tx pgx.Tx, post int64) error {
	if p == nil {
		return platform.ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished || p.published || p.key == "" || tx == nil {
		return platform.ErrInvalid
	}
	thread, cohort, err := p.service.postUploadGate(ctx, tx, p.actor, post)
	if err != nil {
		return err
	}
	if thread != p.thread || p.kind != "image" && cohort != "adult" {
		return platform.ErrForbidden
	}
	mainKey, sourceKey, status, mime := p.key, "", "ready", p.manifest.Main.ContentType
	if p.kind == "video" {
		mainKey, sourceKey, status, mime = "", p.key, "pending", "video/mp4"
	}
	display := ""
	if p.kind == "file" {
		display = sanitizeFilename(p.filename)
	}
	m := p.manifest
	var id int64
	if err = tx.QueryRow(ctx, `INSERT INTO media_attachment(post_id,uploader_id,kind,status,storage_key,thumb_storage_key,poster_storage_key,poster_content_type,source_storage_key,content_type,byte_size,sha256,original_filename,width,height,exif_stripped,duration_seconds,processing_attempts,processing_started_at,expires_at,purged_at,created_at) VALUES($1,$2,$3,$4,$5,$6,'','',$7,$8,$9,$10,$11,$12,$13,$14,0,0,NULL,$15,NULL,now()) RETURNING id`, post, p.actor.ID, p.kind, status, mainKey, p.thumb, sourceKey, mime, m.Main.ByteSize, m.Main.SHA256, display, m.Main.Width, m.Main.Height, m.MetadataStripped, expiry(cohort, p.ttl)).Scan(&id); err != nil {
		return err
	}
	if err = saveManifest(ctx, tx, "attachment", id, m); err != nil {
		return err
	}
	if err := platform.RecordAudit(ctx, tx, p.actor, "media.attached", fmt.Sprintf("media.attachment:%d", id), map[string]any{"kind": p.kind, "source_sha256": m.SourceSHA256}); err != nil {
		return err
	}
	p.published = true
	return nil
}
func (p *PreparedAttachment) Finish(ctx context.Context, committed bool) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	if !committed || !p.published {
		p.service.discard(ctx, p.key, p.thumb)
	}
	if p.sourceDir != "" {
		_ = cleanupDir(p.sourceDir)
	}
	_ = p.manifest.Cleanup()
	if committed && p.published && p.kind == "video" {
		p.service.kickVideoProcessing()
	}
}
