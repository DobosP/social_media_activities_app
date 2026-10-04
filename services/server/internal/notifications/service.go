package notifications

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed reasons.json
var reasonJSON []byte
var reasons = func() map[string]string { var v map[string]string; _ = json.Unmarshal(reasonJSON, &v); return v }()

type Service struct {
	DB     *pgxpool.Pool
	Cursor platform.CursorCodec
}

func New(db *pgxpool.Pool, cursor platform.CursorCodec) *Service { return &Service{db, cursor} }
func (s *Service) Register(mux *http.ServeMux) {
	for _, prefix := range []string{"/api/notifications", "/api/v1/notifications"} {
		registerRoute(mux, "GET "+prefix+"/", s.list)
		registerRoute(mux, "POST "+prefix+"/{id}/read/", s.read)
		registerRoute(mux, "POST "+prefix+"/read-all/", s.readAll)
	}
}
func (s *Service) projection(ctx context.Context, id, actor int64) (map[string]any, error) {
	var kind, title, body, url string
	var read bool
	var created any
	err := s.DB.QueryRow(ctx, `SELECT kind,title,body,url,read_at IS NOT NULL,created_at FROM notifications_notification WHERE id=$1 AND recipient_id=$2`, id, actor).Scan(&kind, &title, &body, &url, &read, &created)
	return map[string]any{"id": id, "kind": kind, "title": title, "body": body, "url": url, "is_read": read, "reason": reasons[kind], "created_at": created}, err
}
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	limit := 100
	offset := 0
	versioned := strings.HasPrefix(r.URL.Path, "/api/v1/")
	if versioned {
		limit = platform.ParseLimit(r.URL.Query().Get("limit"), 50, 200)
		offset = s.Cursor.Decode(r.URL.Query().Get("cursor"))
	}
	unread := r.URL.Query().Get("unread") == "1" || r.URL.Query().Get("unread") == "true"
	rows, err := s.DB.Query(r.Context(), `SELECT id,kind,title,body,url,read_at IS NOT NULL,created_at FROM notifications_notification WHERE recipient_id=$1 AND (NOT $2 OR read_at IS NULL) ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4`, a.ID, unread, limit+1, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	items := []any{}
	for rows.Next() {
		var id int64
		var kind, title, body, url string
		var read bool
		var created any
		if err = rows.Scan(&id, &kind, &title, &body, &url, &read, &created); err != nil {
			rows.Close()
			platform.Fail(w, err)
			return
		}
		items = append(items, map[string]any{"id": id, "kind": kind, "title": title, "body": body, "url": url, "is_read": read, "reason": reasons[kind], "created_at": created})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		platform.Fail(w, err)
		return
	}
	var count int64
	if err = s.DB.QueryRow(r.Context(), `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND read_at IS NULL`, a.ID).Scan(&count); err != nil {
		platform.Fail(w, err)
		return
	}
	result := map[string]any{"unread_count": count, "results": items}
	hasNext := len(items) > limit
	if hasNext {
		items = items[:limit]
		result["results"] = items
	}
	if versioned {
		next := ""
		if hasNext {
			next = s.Cursor.Encode(offset + limit)
		}
		result["limit"] = limit
		result["next_cursor"] = next
	}
	platform.JSON(w, 200, result)
}

// PurgeRead deletes a bounded oldest-first batch while retaining unseen notices
// and the non-mutable DSA moderation/system records.
func (s *Service) PurgeRead(ctx context.Context, days, batchSize int) (int64, error) {
	if days <= 0 || batchSize <= 0 {
		return 0, nil
	}
	result, err := s.DB.Exec(ctx, `DELETE FROM notifications_notification WHERE id IN (SELECT id FROM notifications_notification WHERE read_at IS NOT NULL AND created_at<now()-make_interval(days=>$1) AND kind NOT IN ('moderation','system') ORDER BY created_at,id LIMIT $2)`, days, batchSize)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
func (s *Service) read(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	result, err := s.DB.Exec(r.Context(), `UPDATE notifications_notification SET read_at=coalesce(read_at,now()) WHERE id=$1 AND recipient_id=$2`, id, a.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		platform.Fail(w, platform.ErrNotFound)
		return
	}
	item, err := s.projection(r.Context(), id, a.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, item)
}
func (s *Service) readAll(w http.ResponseWriter, r *http.Request) {
	a, ok := platform.RequireActor(w, r)
	if !ok {
		return
	}
	result, err := s.DB.Exec(r.Context(), `UPDATE notifications_notification SET read_at=now() WHERE recipient_id=$1 AND read_at IS NULL`, a.ID)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"marked_read": result.RowsAffected()})
}

func registerRoute(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, handler)
}
