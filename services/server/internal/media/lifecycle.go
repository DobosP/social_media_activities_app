package media

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// PurgeExpiredAttachments preserves moderation evidence, including unresolved
// reports on posts/activities/groups/uploaders and unlifted REMOVE actions.
// An author's own deletion releases the hide hold only without a standing REMOVE.
// Held rows stay unmarked, so candidates are keyset-paged and each page reports
// the same hold rule in SQL: a held row is skipped without a transaction and can
// no longer fill the window. The locked per-row re-check stays authoritative.
func (s *Service) PurgeExpiredAttachments(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, platform.ErrInvalid
	}
	purged, scanned := 0, 0
	var afterAt time.Time
	var afterID int64
	for purged < limit && scanned < limit*purgeScanFactor {
		page, e := s.expiredAttachmentPage(ctx, afterAt, afterID, min(limit*4, limit*purgeScanFactor-scanned))
		if e != nil {
			return purged, e
		}
		if len(page) == 0 {
			break
		}
		scanned += len(page)
		afterAt, afterID = page[len(page)-1].expires, page[len(page)-1].id
		for _, candidate := range page {
			if candidate.held {
				continue
			}
			id, changed := candidate.id, false
			e = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
				att, e := s.attachment(ctx, tx, id, true)
				if errors.Is(e, pgx.ErrNoRows) {
					return nil
				}
				if e != nil {
					return e
				}
				if att.PurgedAt != nil || att.ExpiresAt == nil || att.ExpiresAt.After(time.Now()) || att.Status == "blocked" || att.Status == "processing" {
					return nil
				}
				held, e := attachmentHeld(ctx, tx, att)
				if e != nil {
					return e
				}
				if held {
					return nil
				}
				if e = queueDelete(ctx, tx, att.key, att.thumb, att.poster, att.sourceKey); e != nil {
					return e
				}
				if _, e = tx.Exec(ctx, `UPDATE media_attachment SET purged_at=now(),storage_key='',thumb_storage_key='',poster_storage_key='',source_storage_key='' WHERE id=$1`, id); e != nil {
					return e
				}
				if e = platform.RecordAudit(ctx, tx, platform.Actor{}, "media.attachment_purged", fmt.Sprintf("media.attachment:%d", id), map[string]any{"reason": "expired"}); e != nil {
					return e
				}
				changed = true
				return nil
			})
			if e != nil {
				if ctx.Err() != nil {
					return purged, ctx.Err()
				}
				continue
			}
			if changed {
				purged++
			}
			if purged >= limit {
				break
			}
		}
	}
	return purged, nil
}

// purgeScanFactor bounds one call to limit*200 examined rows (100,000 at the
// scheduled 500), in pages of limit*4. A held row costs only its share of one
// bounded page statement, so the budget reaches far past held evidence; rows
// that are purged, lose the locked re-check or fail their transaction spend it too.
const purgeScanFactor = 200

type purgeCandidate struct {
	id      int64
	expires time.Time
	held    bool
}

// expiredAttachmentPage evaluates the hold rule for exactly one keyset page, so
// no statement's cost grows with the number of held rows behind the cursor.
func (s *Service) expiredAttachmentPage(ctx context.Context, afterAt time.Time, afterID int64, n int) ([]purgeCandidate, error) {
	query := `SELECT x.id,x.expires_at,` + attachmentHoldSQL("x.post_id", "x.activity_id", "x.group_id", "x.uploader_id") + ` FROM (SELECT a.id,a.expires_at,a.post_id,a.uploader_id,COALESCE(t.activity_id,0) AS activity_id,COALESCE(t.group_id,0) AS group_id FROM media_attachment a JOIN social_post sp ON sp.id=a.post_id JOIN social_thread t ON t.id=sp.thread_id WHERE a.expires_at<=now() AND a.purged_at IS NULL AND a.status NOT IN('blocked','processing')`
	args := []any{n}
	if afterID > 0 {
		query += ` AND (a.expires_at,a.id)>($2,$3)`
		args = append(args, afterAt, afterID)
	}
	rows, err := s.db.Query(ctx, query+` ORDER BY a.expires_at,a.id LIMIT $1) x ORDER BY x.expires_at,x.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var page []purgeCandidate
	for rows.Next() {
		var c purgeCandidate
		if err = rows.Scan(&c.id, &c.expires, &c.held); err != nil {
			return nil, err
		}
		page = append(page, c)
	}
	return page, rows.Err()
}

// attachmentHoldSQL is the one hold rule, over SQL expressions for an
// attachment's post, activity, group and uploader. The purge candidate query
// and the locked per-row re-check share it so they cannot diverge. Outer
// queries must not alias a table as p, m, c or r.
func attachmentHoldSQL(post, activity, group, owner string) string {
	return `(EXISTS(SELECT 1 FROM social_post p WHERE p.id=` + post + ` AND p.is_hidden AND (NOT p.is_author_deleted OR EXISTS(SELECT 1 FROM safety_moderationaction m JOIN django_content_type c ON c.id=m.target_type_id WHERE c.app_label='social' AND c.model='post' AND m.target_id=p.id AND m.action='remove' AND m.lifted_at IS NULL)))
OR EXISTS(SELECT 1 FROM social_activity WHERE id=` + activity + ` AND is_hidden)
OR EXISTS(SELECT 1 FROM social_group WHERE id=` + group + ` AND is_hidden)
OR EXISTS(SELECT 1 FROM safety_report r JOIN django_content_type c ON c.id=r.target_type_id WHERE r.status!='dismissed' AND ((c.app_label='social' AND c.model='post' AND r.target_id=` + post + `) OR (c.app_label='social' AND c.model='activity' AND r.target_id=` + activity + `) OR (c.app_label='social' AND c.model='group' AND r.target_id=` + group + `) OR (c.app_label='accounts' AND c.model='user' AND r.target_id=` + owner + `))))`
}
func attachmentHeld(ctx context.Context, q platform.Querier, att Attachment) (bool, error) {
	var held bool
	e := q.QueryRow(ctx, `SELECT `+attachmentHoldSQL("$1", "$2", "$3", "$4"), att.PostID, att.activity, att.group, att.owner).Scan(&held)
	return held, e
}

// DrainBlobDeletions makes physical erasure durable and idempotent. One failure
// backs off its own key; independent keys keep progressing. Object keys stay
// private in the outbox and are never logged or sent as notifications.
func (s *Service) DrainBlobDeletions(ctx context.Context, limit int) (int, error) {
	if limit < 1 || limit > 1000 {
		return 0, platform.ErrInvalid
	}
	done := 0
	for n := 0; n < limit; n++ {
		var key string
		var attempts int
		e := platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
			e := tx.QueryRow(ctx, `SELECT storage_key,attempts FROM media_go_blobdeletion WHERE next_attempt_at<=now() ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&key, &attempts)
			if e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `UPDATE media_go_blobdeletion SET attempts=attempts+1,next_attempt_at=now()+interval '5 minutes' WHERE storage_key=$1`, key)
			return e
		})
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			return done, e
		}
		e = s.storage.Delete(ctx, key)
		if e == nil {
			if e = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
				if _, e := tx.Exec(ctx, `DELETE FROM media_go_blobdeletion WHERE storage_key=$1`, key); e != nil {
					return e
				}
				return platform.RecordAudit(ctx, tx, platform.Actor{}, "erasure.blob_cleanup", "", map[string]any{"blob_count": 1})
			}); e != nil {
				return done, e
			}
			done++
		} else {
			delay := min(24*time.Hour, time.Duration(1<<min(attempts, 8))*time.Minute)
			if _, e = s.db.Exec(ctx, `UPDATE media_go_blobdeletion SET next_attempt_at=now()+($2::double precision*interval '1 second') WHERE storage_key=$1`, key, delay.Seconds()); e != nil {
				return done, e
			}
		}
	}
	return done, nil
}

func (s *Service) managePlace(ctx context.Context, q platform.Querier, a platform.Actor, place int64, publication bool) (name, website string, err error) {
	a, err = currentMediaActor(ctx, q, a)
	if err != nil {
		return "", "", err
	}
	if publication {
		ok, e := s.publicPlace(ctx, q, place)
		if e != nil {
			return "", "", e
		}
		if !ok {
			return "", "", platform.ErrForbidden
		}
	}
	if a.IsStaff {
		err = q.QueryRow(ctx, `SELECT COALESCE((SELECT name FROM places_partner WHERE place_id=$1 AND is_verified AND is_active ORDER BY name,id LIMIT 1),''),COALESCE((SELECT NULLIF(website,'') FROM places_partner WHERE place_id=$1 AND is_verified AND is_active ORDER BY name,id LIMIT 1),(SELECT website FROM places_place WHERE id=$1),'')`, place).Scan(&name, &website)
		return
	}
	if a.Cohort != "adult" {
		return "", "", platform.ErrForbidden
	}
	if err = platform.Participate(ctx, q, a); err != nil {
		return
	}
	err = q.QueryRow(ctx, `SELECT p.name,COALESCE(NULLIF(p.website,''),v.website,'') FROM places_placeclaim c JOIN places_partner p ON p.id=c.partner_id AND p.place_id=c.place_id JOIN places_place v ON v.id=c.place_id WHERE c.place_id=$1 AND c.claimant_id=$2 AND c.status='approved' AND c.kind='business' AND p.is_verified AND p.is_active AND p.kind='business' ORDER BY c.decided_at DESC,c.id DESC LIMIT 1`, place, a.ID).Scan(&name, &website)
	if errors.Is(err, pgx.ErrNoRows) {
		err = platform.ErrForbidden
	}
	return
}
func (s *Service) UploadPlaceCover(ctx context.Context, a platform.Actor, placeID int64, path, alt string) (id int64, err error) {
	defer func() { s.recordFailure(ctx, a, "place_cover", err) }()
	_, _, err = s.managePlace(ctx, s.db, a, placeID, true)
	if err != nil {
		return 0, err
	}
	m, err := s.processor.ProcessImage(ctx, path)
	if err != nil {
		return 0, err
	}
	defer m.Cleanup()
	key, err := s.storeArtifact(ctx, "place-covers", m.Main)
	if err != nil {
		return 0, err
	}
	published := false
	defer func() {
		if !published {
			s.discard(ctx, key)
		}
	}()
	err = platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, `SELECT id FROM places_place WHERE id=$1 FOR UPDATE`, placeID); e != nil {
			return e
		}
		name, website, e := s.managePlace(ctx, tx, a, placeID, true)
		if e != nil {
			return e
		}
		attribution := "Official image"
		if name != "" {
			attribution += ": " + name
		}
		if !strings.HasPrefix(website, "https://") && !strings.HasPrefix(website, "http://") {
			website = ""
		}
		var old string
		e = tx.QueryRow(ctx, `SELECT storage_key FROM places_placecover WHERE place_id=$1 FOR UPDATE`, placeID).Scan(&old)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		e = tx.QueryRow(ctx, `INSERT INTO places_placecover(place_id,source,uploaded_by_id,storage_key,content_type,byte_size,sha256,width,height,exif_stripped,attribution,license_name,source_page_url,alt_text,created_at,updated_at) VALUES($1,'business',$2,$3,$4,$5,$6,$7,$8,true,$9,'Used with permission',$10,$11,now(),now()) ON CONFLICT(place_id) DO UPDATE SET source='business',uploaded_by_id=excluded.uploaded_by_id,storage_key=excluded.storage_key,content_type=excluded.content_type,byte_size=excluded.byte_size,sha256=excluded.sha256,width=excluded.width,height=excluded.height,exif_stripped=true,attribution=excluded.attribution,license_name=excluded.license_name,source_page_url=excluded.source_page_url,alt_text=excluded.alt_text,updated_at=now() RETURNING id`, placeID, a.ID, key, m.Main.ContentType, m.Main.ByteSize, m.Main.SHA256, m.Main.Width, m.Main.Height, trimText(attribution, 255), website, trimText(alt, 140)).Scan(&id)
		if e != nil {
			return e
		}
		if e = queueDelete(ctx, tx, old); e != nil {
			return e
		}
		if e = saveManifest(ctx, tx, "place-cover", id, m); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "media.place_cover_uploaded", fmt.Sprintf("places.place:%d", placeID), map[string]any{"cover_id": id, "source_sha256": m.SourceSHA256})
	})
	if err == nil {
		published = true
	}
	return id, err
}
func (s *Service) DeletePlaceCover(ctx context.Context, a platform.Actor, placeID int64) error {
	return platform.Transaction(ctx, s.db, func(tx pgx.Tx) error {
		if _, _, e := s.managePlace(ctx, tx, a, placeID, false); e != nil {
			return e
		}
		var id int64
		var key string
		if e := tx.QueryRow(ctx, `SELECT id,storage_key FROM places_placecover WHERE place_id=$1 FOR UPDATE`, placeID).Scan(&id, &key); e != nil {
			return e
		}
		if e := queueDelete(ctx, tx, key); e != nil {
			return e
		}
		if _, e := tx.Exec(ctx, `DELETE FROM places_placecover WHERE id=$1`, id); e != nil {
			return e
		}
		return platform.RecordAudit(ctx, tx, a, "media.place_cover_deleted", fmt.Sprintf("places.place:%d", placeID), nil)
	})
}
func (s *Service) PlaceCoverURL(ctx context.Context, placeID int64) (string, error) {
	var id int64
	if e := s.db.QueryRow(ctx, `SELECT id FROM places_placecover WHERE place_id=$1 AND storage_key!=''`, placeID).Scan(&id); e != nil {
		return "", e
	}
	allowed, e := s.publicPlace(ctx, s.db, placeID)
	if e != nil {
		return "", e
	}
	if !allowed {
		return "", platform.ErrForbidden
	}
	return s.url("place-cover", id, platform.Actor{}, "main", nil)
}
