package media

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func (s *Service) Register(mux *http.ServeMux) {
	for _, prefix := range []string{"/api/v1/media/", "/api/media/"} {
		mux.HandleFunc(exactRoute("POST "+prefix+"photos/"), s.uploadPhotoHTTP)
		mux.HandleFunc(exactRoute("GET "+prefix+"photos/{id}/"), s.getPhotoHTTP)
		mux.HandleFunc(exactRoute("DELETE "+prefix+"photos/{id}/"), s.deletePhotoHTTP)
		mux.HandleFunc(exactRoute("GET "+prefix+"threads/{thread}/photos/"), s.threadPhotosHTTP)
		mux.HandleFunc(exactRoute("GET "+prefix+"activity-covers/{activity}/"), s.getCoverHTTP)
		mux.HandleFunc(exactRoute("PUT "+prefix+"activity-covers/{activity}/"), s.putCoverHTTP)
		mux.HandleFunc(exactRoute("DELETE "+prefix+"activity-covers/{activity}/"), s.deleteCoverHTTP)
		for _, kind := range []string{"file", "attachment", "activity-cover-file", "place-cover-file"} {
			mux.HandleFunc(exactRoute("GET "+prefix+kind+"/{token}/"), s.serveHTTP(kind))
		}
	}
}
func (s *Service) uploadPhotoHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	path, _, fields, e := s.ReadUpload(w, r, s.processor.cfg.ImageMaxBytes)
	if e != nil {
		mediaFail(w, e)
		return
	}
	defer os.Remove(path)
	kind := fields["kind"]
	if kind == "" {
		kind = "profile"
	}
	thread, _ := strconv.ParseInt(fields["thread"], 10, 64)
	p, e := s.UploadPhoto(r.Context(), a, kind, thread, path)
	if e != nil {
		mediaFail(w, e)
		return
	}
	platform.JSON(w, 201, p)
}
func (s *Service) getPhotoHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := rowID(r, "id")
	if e != nil {
		mediaFail(w, e)
		return
	}
	p, e := photoScan(s.db.QueryRow(r.Context(), `SELECT `+photoColumns+` FROM media_photo WHERE id=$1`, id))
	if e != nil {
		mediaFail(w, e)
		return
	}
	if e = s.photoAllowed(r.Context(), s.db, a, p); e != nil {
		mediaFail(w, e)
		return
	}
	p.URL, e = s.url("photo", p.ID, a, "main", nil)
	if e != nil {
		mediaFail(w, e)
		return
	}
	platform.JSON(w, 200, p)
}
func (s *Service) deletePhotoHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := rowID(r, "id")
	if e != nil {
		mediaFail(w, e)
		return
	}
	e = platform.Transaction(r.Context(), s.db, func(tx pgx.Tx) error {
		p, e := photoScan(tx.QueryRow(r.Context(), `SELECT `+photoColumns+` FROM media_photo WHERE id=$1 FOR UPDATE`, id))
		if e != nil {
			return e
		}
		if a.ID != p.owner && !a.IsStaff {
			return platform.ErrForbidden
		}
		if e = queueDelete(r.Context(), tx, p.key, p.thumb); e != nil {
			return e
		}
		if _, e = tx.Exec(r.Context(), `DELETE FROM media_photo WHERE id=$1`, id); e != nil {
			return e
		}
		return platform.RecordAudit(r.Context(), tx, a, "media.deleted", fmt.Sprintf("media.photo:%d", id), nil)
	})
	if e != nil {
		mediaFail(w, e)
		return
	}
	platform.JSON(w, 204, nil)
}
func (s *Service) threadPhotosHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := rowID(r, "thread")
	if e != nil {
		mediaFail(w, e)
		return
	}
	if e = s.approved(r.Context(), s.db, a, id, false); e != nil {
		mediaFail(w, e)
		return
	}
	rows, e := s.db.Query(r.Context(), `SELECT `+photoColumns+` FROM media_photo WHERE thread_id=$1 AND scan_status='clean' AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=media_photo.uploader_id) OR (b.blocker_id=media_photo.uploader_id AND b.blocked_id=$2)) ORDER BY created_at DESC LIMIT 1001`, id, a.ID)
	if e != nil {
		mediaFail(w, e)
		return
	}
	defer rows.Close()
	photos := []Photo{}
	for rows.Next() {
		p, e := photoScan(rows)
		if e != nil {
			mediaFail(w, e)
			return
		}
		photos = append(photos, p)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		mediaFail(w, e)
		return
	}
	for i := range photos {
		photos[i].URL, e = s.url("photo", photos[i].ID, a, "main", nil)
		if e != nil {
			mediaFail(w, e)
			return
		}
	}
	if len(photos) > 1000 {
		platform.Error(w, 413, "Thread media exceeds the response budget.")
		return
	}
	platform.JSON(w, 200, photos)
}
func (s *Service) getCoverHTTP(w http.ResponseWriter, r *http.Request) {
	a, _ := platform.ActorFrom(r)
	id, e := rowID(r, "activity")
	if e != nil {
		mediaFail(w, e)
		return
	}
	c, e := coverScan(s.db.QueryRow(r.Context(), `SELECT `+coverColumns+` FROM media_activitycover WHERE activity_id=$1`, id))
	if e != nil {
		mediaFail(w, e)
		return
	}
	if e = s.activityVisible(r.Context(), s.db, a, id); e != nil {
		mediaFail(w, e)
		return
	}
	c.URL, e = s.url("activity-cover", c.ID, a, "main", nil)
	if e != nil {
		mediaFail(w, e)
		return
	}
	platform.JSON(w, 200, c)
}
func (s *Service) putCoverHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := rowID(r, "activity")
	if e != nil {
		mediaFail(w, e)
		return
	}
	if e = s.manageCover(r.Context(), s.db, a, id, true); e != nil {
		mediaFail(w, e)
		return
	}
	path, _, fields, e := s.ReadUpload(w, r, s.processor.cfg.ImageMaxBytes)
	if e != nil {
		mediaFail(w, e)
		return
	}
	defer os.Remove(path)
	c, e := s.UploadActivityCover(r.Context(), a, id, path, fields["alt_text"])
	if e != nil {
		mediaFail(w, e)
		return
	}
	platform.JSON(w, 200, c)
}
func (s *Service) deleteCoverHTTP(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, e := rowID(r, "activity")
	if e != nil {
		mediaFail(w, e)
		return
	}
	e = platform.Transaction(r.Context(), s.db, func(tx pgx.Tx) error {
		if e := s.manageCover(r.Context(), tx, a, id, false); e != nil {
			return e
		}
		c, e := coverScan(tx.QueryRow(r.Context(), `SELECT `+coverColumns+` FROM media_activitycover WHERE activity_id=$1 FOR UPDATE`, id))
		if e != nil {
			return e
		}
		if e = queueDelete(r.Context(), tx, c.key, c.thumb); e != nil {
			return e
		}
		if _, e = tx.Exec(r.Context(), `DELETE FROM media_activitycover WHERE id=$1`, c.ID); e != nil {
			return e
		}
		return platform.RecordAudit(r.Context(), tx, a, "media.activity_cover_deleted", fmt.Sprintf("social.activity:%d", id), nil)
	})
	if e != nil {
		mediaFail(w, e)
		return
	}
	platform.JSON(w, 204, nil)
}

func (s *Service) serveHTTP(route string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := platform.ActorFrom(r)
		publicRoute := route == "activity-cover-file" || route == "place-cover-file"
		if !ok && !publicRoute {
			platform.Error(w, 401, "Authentication required.")
			return
		}
		ref, e := s.tokens.Verify(r.PathValue("token"), a.ID, time.Now())
		if e != nil && publicRoute {
			ref, e = s.tokens.Verify(r.PathValue("token"), 0, time.Now())
		}
		if e != nil {
			mediaFail(w, platform.ErrForbidden)
			return
		}
		parts := strings.Split(ref.Key, "/")
		if len(parts) != 2 {
			mediaFail(w, platform.ErrForbidden)
			return
		}
		id, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || id < 1 {
			mediaFail(w, platform.ErrForbidden)
			return
		}
		expected := map[string]string{"file": "photo", "attachment": "attachment", "activity-cover-file": "activity-cover", "place-cover-file": "place-cover"}[route]
		if parts[0] != expected {
			mediaFail(w, platform.ErrForbidden)
			return
		}
		key, mime, download := "", "", ""
		var expires *time.Time
		switch route {
		case "file":
			p, e := photoScan(s.db.QueryRow(r.Context(), `SELECT `+photoColumns+` FROM media_photo WHERE id=$1`, id))
			if e != nil {
				mediaFail(w, e)
				return
			}
			if e = s.photoAllowed(r.Context(), s.db, a, p); e != nil {
				mediaFail(w, e)
				return
			}
			key, mime = p.key, p.ContentType
			if ref.Variant == "thumb" && p.thumb != "" {
				key = p.thumb
			}
			if ref.Variant == "poster" {
				mediaFail(w, platform.ErrForbidden)
				return
			}
		case "activity-cover-file":
			c, e := coverScan(s.db.QueryRow(r.Context(), `SELECT `+coverColumns+` FROM media_activitycover WHERE id=$1`, id))
			if e != nil {
				mediaFail(w, e)
				return
			}
			viewer := a
			if ref.Public {
				viewer = platform.Actor{}
			}
			if e = s.activityVisible(r.Context(), s.db, viewer, c.Activity); e != nil {
				mediaFail(w, e)
				return
			}
			key, mime = c.key, c.ContentType
			if ref.Variant == "thumb" && c.thumb != "" {
				key = c.thumb
			}
			if ref.Variant == "poster" {
				mediaFail(w, platform.ErrForbidden)
				return
			}
		case "place-cover-file":
			var place int64
			e = s.db.QueryRow(r.Context(), `SELECT place_id,storage_key,content_type FROM places_placecover WHERE id=$1`, id).Scan(&place, &key, &mime)
			if e != nil {
				mediaFail(w, e)
				return
			}
			allowed, e := s.publicPlace(r.Context(), s.db, place)
			if e != nil {
				mediaFail(w, e)
				return
			}
			if !ref.Public || !allowed || ref.Variant != "main" {
				mediaFail(w, platform.ErrForbidden)
				return
			}
		case "attachment":
			att, e := s.attachment(r.Context(), s.db, id, false)
			if e != nil {
				mediaFail(w, e)
				return
			}
			if e = s.attachmentAllowed(r.Context(), s.db, a, att); e != nil {
				mediaFail(w, e)
				return
			}
			key, mime, expires = att.key, att.ContentType, att.ExpiresAt
			if ref.Variant == "thumb" && att.thumb != "" {
				key = att.thumb
			}
			if ref.Variant == "poster" {
				if att.poster == "" {
					mediaFail(w, platform.ErrForbidden)
					return
				}
				key, mime = att.poster, att.posterType
			}
			if att.Kind == "file" {
				download = sanitizeFilename(att.OriginalFilename)
			}
		}
		if key == "" || !validMIME(mime) {
			mediaFail(w, platform.ErrForbidden)
			return
		}
		ttl := s.policy.PresignedTTL
		if expires != nil {
			remaining := time.Until(*expires)
			if remaining < ttl {
				ttl = remaining
			}
			if ttl < time.Second {
				mediaFail(w, platform.ErrForbidden)
				return
			}
		}
		if s.Presign {
			url, e := s.storage.PresignGet(r.Context(), key, ttl, mime, download)
			if e != nil {
				mediaFail(w, e)
				return
			}
			if url != "" {
				w.Header().Set("Cache-Control", "private, no-store")
				w.Header().Set("X-Content-Type-Options", "nosniff")
				http.Redirect(w, r, url, 307)
				return
			}
		}
		ctx := r.Context()
		var cancel context.CancelFunc
		if expires != nil {
			ctx, cancel = context.WithDeadline(ctx, *expires)
			defer cancel()
		}
		s.stream(w, r.WithContext(ctx), key, mime, download)
	}
}
func (s *Service) stream(w http.ResponseWriter, r *http.Request, key, mime, download string) {
	total, e := s.storage.Size(r.Context(), key)
	if e != nil || total <= 0 {
		mediaFail(w, ErrObject)
		return
	}
	start, end := int64(0), total-1
	partial := false
	rangeValue := r.Header.Get("Range")
	if rangeValue != "" && !strings.Contains(rangeValue, ",") {
		a, b, e := singleRange(rangeValue, total)
		if e != nil {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", total))
			w.WriteHeader(416)
			return
		}
		start, end, partial = a, b, true
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if download != "" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+download+`"`)
	}
	status := 200
	if partial {
		status = 206
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, total))
	}
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return
	}
	// Proxied media outlives the short server WriteTimeout, sized to its length.
	platform.ExtendDeadlines(w, 0, platform.TransferWriteTimeout(end-start+1))
	for start <= end {
		last := min(end, start+(1<<20)-1)
		b, e := s.storage.OpenRange(r.Context(), key, start, last)
		if e != nil {
			return
		}
		if _, e = w.Write(b); e != nil {
			return
		}
		start = last + 1
	}
}
func singleRange(header string, total int64) (int64, int64, error) {
	if !strings.HasPrefix(header, "bytes=") || total <= 0 {
		return 0, 0, ErrObject
	}
	parts := strings.Split(strings.TrimPrefix(header, "bytes="), "-")
	if len(parts) != 2 || len(parts[0]) > 18 || len(parts[1]) > 18 {
		return 0, 0, ErrObject
	}
	if parts[0] == "" {
		n, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || n <= 0 {
			return 0, 0, ErrObject
		}
		return max(0, total-n), total - 1, nil
	}
	start, e := strconv.ParseInt(parts[0], 10, 64)
	if e != nil || start < 0 || start >= total {
		return 0, 0, ErrObject
	}
	end := total - 1
	if parts[1] != "" {
		end, e = strconv.ParseInt(parts[1], 10, 64)
		if e != nil || end < start {
			return 0, 0, ErrObject
		}
		end = min(end, total-1)
	}
	return start, end, nil
}

func exactRoute(pattern string) string { return strings.TrimSuffix(pattern, "/") + "/{$}" }
