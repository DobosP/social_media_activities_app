package discovery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB              *pgxpool.Pool
	Catalog         *catalog.Service
	Social          *social.Service
	Recommendations *recommendations.Service
	Cursor          platform.CursorCodec
}

func New(db *pgxpool.Pool, cat *catalog.Service, soc *social.Service, rec *recommendations.Service) *Service {
	return &Service{DB: db, Catalog: cat, Social: soc, Recommendations: rec}
}
func (s *Service) Register(mux *http.ServeMux) {
	for _, base := range []string{"/api/v1/discovery/", "/api/discovery/"} {
		for path, fn := range map[string]http.HandlerFunc{"near-me": s.nearMe, "happening": s.happening, "activities": s.activities, "activity-deck": s.deck, "public/activities": s.publicActivities, "public/groups": s.publicGroups, "feed": s.feed} {
			handler := fn
			if path == "activities" || path == "activity-deck" || path == "feed" {
				handler = required(fn)
			}
			mux.HandleFunc("GET "+base+path+"/{$}", bounded(handler))
		}
	}
}
func bounded(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := platform.Timeout(r)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}
func required(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := platform.RequireActor(w, r); !ok {
			return
		}
		next(w, r)
	}
}
func truth(r *http.Request, key string) bool {
	value := strings.ToLower(r.URL.Query().Get(key))
	return value == "1" || value == "true" || value == "yes"
}
func query(ctx context.Context, db *pgxpool.Pool, sql string, args ...any) ([]map[string]any, error) {
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var item map[string]any
		if json.Unmarshal(raw, &item) != nil {
			return nil, platform.ErrInvalid
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
func (s *Service) page(r *http.Request, data []map[string]any, maxLimit int) (any, error) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		if len(data) > 100 {
			data = data[:100]
		}
		return data, nil
	}
	limit := platform.ParseLimit(r.URL.Query().Get("limit"), 50, maxLimit)
	offset := s.Cursor.Decode(r.URL.Query().Get("cursor"))
	if offset >= len(data) {
		return map[string]any{"next_cursor": "", "limit": limit, "results": []map[string]any{}}, nil
	}
	end := min(offset+limit, len(data))
	next := ""
	if end < len(data) {
		next = s.Cursor.Encode(end)
		if next == "" {
			return nil, platform.ErrForbidden
		}
	}
	return map[string]any{"next_cursor": next, "limit": limit, "results": data[offset:end]}, nil
}

// SQL pagination reads at most limit+1 and does not silently cap canonical feeds.
func (s *Service) window(r *http.Request) (limit, offset int) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		return 100, 0
	}
	return platform.ParseLimit(r.URL.Query().Get("limit"), 50, 200), s.Cursor.Decode(r.URL.Query().Get("cursor"))
}
func (s *Service) windowBody(r *http.Request, data []map[string]any, limit, offset int) (any, error) {
	if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
		if len(data) > limit {
			data = data[:limit]
		}
		return data, nil
	}
	next := ""
	if len(data) > limit {
		data = data[:limit]
		next = s.Cursor.Encode(offset + limit)
		if next == "" {
			return nil, platform.ErrForbidden
		}
	}
	return map[string]any{"next_cursor": next, "limit": limit, "results": data}, nil
}
func finish(w http.ResponseWriter, data any, err error) {
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, data)
}
func (s *Service) visuals(ctx context.Context, a platform.Actor, data []map[string]any) error {
	if len(data) == 0 {
		return nil
	}
	ids := make([]int64, len(data))
	for i, item := range data {
		ids[i] = itemID(item)
	}
	if s.Social.ActivityVisuals == nil {
		return platform.ErrForbidden
	}
	images, err := s.Social.ActivityVisuals(ctx, s.DB, a, ids)
	if err != nil {
		return err
	}
	for _, item := range data {
		id := itemID(item)
		image, ok := images[id]
		if !ok {
			return platform.ErrForbidden
		}
		item["visual"] = image
	}
	return nil
}

const card = `jsonb_build_object('id',a.id,'title',a.title,'cohort',a.cohort,'starts_at',a.starts_at,'status',a.status,'activity_type',t.slug,'place_id',a.place_id,'distance_m',DISTANCE,'description',left(a.description,280),'place_name',p.name)`
const joins = ` FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id LEFT JOIN places_place p ON p.id=a.place_id `

func (s *Service) activityCards(ctx context.Context, a platform.Actor, near catalog.Near, activity string, public, beginners bool, from, to any, limit, offset int) ([]map[string]any, error) {
	where := social.ActivityVisibilitySQL() + ` AND a.status='open' AND a.starts_at>=now()`
	if public {
		where = `($1::bigint>=0) AND ($2::text IS NOT NULL) AND a.cohort='adult' AND a.is_publicly_listed AND a.status='open' AND NOT a.is_hidden AND a.starts_at>=now() AND u.is_active`
	}
	if !public && (a.ID < 1 || a.Cohort == "" || a.Cohort == "unassigned") {
		return []map[string]any{}, nil
	}
	where += ` AND ($3::text='' OR t.slug=$3) AND (NOT $4 OR a.beginners_welcome) AND ($5::timestamptz IS NULL OR a.starts_at>=$5) AND ($6::timestamptz IS NULL OR a.starts_at<$6) AND ($7::double precision IS NULL OR $8::double precision IS NULL OR (p.location IS NOT NULL AND ($9::double precision IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography,$9))))`
	distance := "NULL"
	order := "a.starts_at,a.id"
	if near.Lon != nil && near.Lat != nil {
		distance = `round(ST_Distance(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography)::numeric,1)`
		order = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography),a.starts_at,a.id`
	}
	projection := strings.ReplaceAll(card, "DISTANCE", distance)
	data, err := query(ctx, s.DB, `SELECT `+projection+joins+` WHERE `+where+` ORDER BY `+order+` LIMIT $10 OFFSET $11`, a.ID, a.Cohort, activity, beginners, from, to, near.Lon, near.Lat, near.Radius, limit, offset)
	if err != nil {
		return nil, err
	}
	if err := s.visuals(ctx, a, data); err != nil {
		return nil, err
	}
	return data, nil
}
func (s *Service) ActivityCards(ctx context.Context, a platform.Actor, near catalog.Near, activity string, public, beginners bool, from, to any, limit int) ([]map[string]any, error) {
	data, err := s.activityCards(ctx, a, near, activity, public, beginners, from, to, limit, 0)
	for _, item := range data {
		delete(item, "description")
		delete(item, "place_name")
	}
	return data, err
}
func stripCards(data []map[string]any) {
	for _, item := range data {
		delete(item, "description")
		delete(item, "place_name")
	}
}
func nearRequest(r *http.Request) catalog.Near {
	near, err := catalog.ParseNear(r.URL.Query(), false)
	if err != nil {
		return catalog.Near{}
	}
	return near
}
func (s *Service) activities(w http.ResponseWriter, r *http.Request) {
	a, _ := platform.ActorFrom(r)
	limit, offset := s.window(r)
	data, err := s.activityCards(r.Context(), a, nearRequest(r), r.URL.Query().Get("activity"), false, false, nil, nil, limit+1, offset)
	if err != nil {
		finish(w, nil, err)
		return
	}
	stripCards(data)
	value, err := s.windowBody(r, data, limit, offset)
	finish(w, value, err)
}
func (s *Service) publicActivities(w http.ResponseWriter, r *http.Request) {
	a, _ := platform.ActorFrom(r)
	from, err := catalog.Bound(r.URL.Query().Get("from"), false)
	if err != nil {
		finish(w, nil, err)
		return
	}
	to, err := catalog.Bound(r.URL.Query().Get("to"), true)
	if err != nil {
		finish(w, nil, err)
		return
	}
	limit, offset := s.window(r)
	data, err := s.activityCards(r.Context(), a, nearRequest(r), r.URL.Query().Get("activity"), true, false, from, to, limit+1, offset)
	if err != nil {
		finish(w, nil, err)
		return
	}
	stripCards(data)
	value, err := s.windowBody(r, data, limit, offset)
	finish(w, value, err)
}
func (s *Service) PublicGroups(ctx context.Context, activity string, limit, offset int) ([]map[string]any, error) {
	return query(ctx, s.DB, `SELECT jsonb_build_object('id',g.id,'title',g.title,'cohort',g.cohort,'city',ar.name,'activity_type',t.slug,'description',left(g.description,280)) FROM social_group g JOIN accounts_user u ON u.id=g.owner_id LEFT JOIN communities_area ar ON ar.id=g.area_id LEFT JOIN taxonomy_activitytype t ON t.id=g.activity_type_id WHERE g.cohort='adult' AND g.is_publicly_listed AND g.status='active' AND NOT g.is_hidden AND u.is_active AND ($1::text='' OR t.slug=$1) ORDER BY g.title,g.id LIMIT $2 OFFSET $3`, activity, limit, offset)
}
func (s *Service) publicGroups(w http.ResponseWriter, r *http.Request) {
	limit, offset := s.window(r)
	data, err := s.PublicGroups(r.Context(), r.URL.Query().Get("activity"), limit+1, offset)
	if err != nil {
		finish(w, nil, err)
		return
	}
	value, err := s.windowBody(r, data, limit, offset)
	finish(w, value, err)
}

type deckCursor struct {
	Seed   string `json:"seed"`
	Offset int    `json:"offset"`
}

func ShuffleKey(seed string, id int64) string {
	sum := sha256.Sum256([]byte(seed + ":" + strconv.FormatInt(id, 10)))
	return hex.EncodeToString(sum[:])
}
func (s *Service) Deck(ctx context.Context, a platform.Actor, seed, token string, limit int, near catalog.Near, activity string, beginners bool) (map[string]any, error) {
	if seed == "" {
		seed = "default"
	}
	runes := []rune(seed)
	if len(runes) > 128 {
		seed = string(runes[:128])
	}
	limit = max(1, min(limit, 24))
	data, err := s.activityCards(ctx, a, near, activity, false, beginners, nil, nil, max(limit*10, 240), 0)
	if err != nil {
		return nil, err
	}
	sort.Slice(data, func(i, j int) bool {
		return ShuffleKey(seed, itemID(data[i])) < ShuffleKey(seed, itemID(data[j]))
	})
	offset := 0
	var cursor deckCursor
	if s.Cursor.UnsignJSON("discovery.activity_deck_cursor", token, &cursor, 0) == nil && cursor.Seed == seed {
		offset = max(0, cursor.Offset)
	}
	if offset > len(data) {
		offset = len(data)
	}
	end := min(offset+limit, len(data))
	next := ""
	if end < len(data) {
		next = s.Cursor.SignJSON("discovery.activity_deck_cursor", deckCursor{seed, end})
		if next == "" {
			return nil, platform.ErrForbidden
		}
	}
	items := data[offset:end]
	for _, item := range items {
		id := strconv.FormatInt(itemID(item), 10)
		item["actions"] = map[string]string{"detail_url": "/api/v1/social/activities/" + id + "/", "web_url": "/activities/" + id + "/"}
		delete(item, "cohort")
		delete(item, "status")
	}
	return map[string]any{"deck_seed": seed, "next_cursor": next, "items": items}, nil
}
func (s *Service) deck(w http.ResponseWriter, r *http.Request) {
	a, _ := platform.ActorFrom(r)
	data, err := s.Deck(r.Context(), a, r.URL.Query().Get("seed"), r.URL.Query().Get("cursor"), platform.ParseLimit(r.URL.Query().Get("limit"), 12, 24), nearRequest(r), r.URL.Query().Get("activity"), truth(r, "beginners"))
	finish(w, data, err)
}

func itemID(item map[string]any) int64 {
	switch value := item["id"].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case int:
		return int64(value)
	}
	return 0
}
