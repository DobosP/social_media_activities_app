package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB           *pgxpool.Pool
	PlaceVisual  func(context.Context, platform.Querier, int64) (any, error)
	PlaceVisuals func(context.Context, platform.Querier, []int64) (map[int64]any, error)
	Cursor       platform.CursorCodec
	Now          func() time.Time
	Budgets      *budgets.Store
	RatePolicies map[string]budgets.Policy
}

func New(db *pgxpool.Pool) *Service {
	return &Service{DB: db, Now: time.Now, Budgets: budgets.New(db)}
}
func (s *Service) Register(mux *http.ServeMux) {
	for _, base := range []string{"/api", "/api/v1"} {
		registerRoute(mux, "GET "+base+"/events/", s.events)
		registerRoute(mux, "GET "+base+"/events/{id}/", s.event)
		registerRoute(mux, "GET "+base+"/places/", s.places)
		registerRoute(mux, "GET "+base+"/places/{id}/", s.place)
		registerRoute(mux, "GET "+base+"/taxonomy/categories/", s.categories)
		registerRoute(mux, "GET "+base+"/taxonomy/categories/{slug}/", s.category)
		registerRoute(mux, "GET "+base+"/taxonomy/activities/", s.types)
		registerRoute(mux, "GET "+base+"/taxonomy/activities/{slug}/", s.typeDetail)
	}
}
func registerRoute(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := validateQuery(r.URL.RawQuery); err != nil {
			platform.Fail(w, err)
			return
		}
		ctx, cancel := platform.Timeout(r)
		defer cancel()
		handler(w, r.WithContext(ctx))
	})
}
func rows(ctx context.Context, db platform.Querier, query string, args ...any) ([]json.RawMessage, error) {
	result := []json.RawMessage{}
	r, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for r.Next() {
		var row json.RawMessage
		if err = r.Scan(&row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, r.Err()
}
func object(ctx context.Context, db platform.Querier, query string, args ...any) (json.RawMessage, error) {
	var row json.RawMessage
	err := db.QueryRow(ctx, query, args...).Scan(&row)
	return row, err
}

const PublicPlaceSQL = `(p.source<>'user' OR EXISTS(SELECT 1 FROM social_userplaceproposal pp WHERE pp.place_id=p.id AND pp.status='published')) AND NOT EXISTS(SELECT 1 FROM places_placeclosurereport cr WHERE cr.place_id=p.id AND cr.created_at>=now()-interval '14 days' GROUP BY cr.place_id HAVING count(*)>=3)`
const publicEventSQL = `NOT e.is_tombstone AND NOT e.is_import_held AND (e.place_id IS NULL OR (` + PublicPlaceSQL + `))`

var eventProjection = `jsonb_build_object('id',e.id,'title',e.title,'description',e.description,'starts_at',e.starts_at,'ends_at',e.ends_at,'url',e.url,'source',e.source,'source_category',e.source_category,'lifecycle_status',e.lifecycle_status,'source_confidence',e.source_confidence,'source_recurrence',e.source_recurrence,'source_timezone',e.source_timezone,'source_price_min',e.source_price_min::text,'source_price_max',e.source_price_max::text,'source_currency',e.source_currency,'source_is_free',e.source_is_free,'source_availability',e.source_availability,'attribution',e.attribution,'license_name',e.license_name,'provenance_url',e.provenance_url,'attribution_credit',` + creditSQL("e") + `,'place',e.place_id,'place_name',coalesce(p.name,''),'activity',t.slug)`

func page(r *http.Request, count int64, data []json.RawMessage, limit, offset int) map[string]any {
	link := func(n int) string {
		u := *r.URL
		q := u.Query()
		q.Set("limit", strconv.Itoa(limit))
		q.Set("offset", strconv.Itoa(n))
		u.RawQuery = q.Encode()
		u.Host = r.Host
		u.Scheme = "http"
		if r.TLS != nil {
			u.Scheme = "https"
		}
		return u.String()
	}
	var next, previous any
	if int64(offset+limit) < count {
		next = link(offset + limit)
	}
	if offset > 0 {
		previous = link(max(0, offset-limit))
	}
	return map[string]any{"count": count, "next": next, "previous": previous, "results": data}
}
func paging(r *http.Request) (int, int) {
	limit := platform.ParseLimit(r.URL.Query().Get("limit"), 50, 200)
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	return limit, max(0, min(offset, 1000000))
}
func parseBound(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		location, _ := time.LoadLocation("Europe/Bucharest")
		t, err = time.ParseInLocation("2006-01-02", raw, location)
	}
	return &t, err
}
func (s *Service) categories(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging(r)
	var count int64
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM taxonomy_activitycategory`).Scan(&count); err != nil {
		platform.Fail(w, err)
		return
	}
	data, err := rows(r.Context(), s.DB, `SELECT jsonb_build_object('slug',c.slug,'name',c.name,'parent',p.slug,'description',c.description) FROM taxonomy_activitycategory c LEFT JOIN taxonomy_activitycategory p ON p.id=c.parent_id ORDER BY c.slug LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, page(r, count, data, limit, offset))
}
func (s *Service) category(w http.ResponseWriter, r *http.Request) {
	data, err := object(r.Context(), s.DB, `SELECT jsonb_build_object('slug',c.slug,'name',c.name,'parent',p.slug,'description',c.description) FROM taxonomy_activitycategory c LEFT JOIN taxonomy_activitycategory p ON p.id=c.parent_id WHERE c.slug=$1`, r.PathValue("slug"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, data)
}

const typeProjection = `jsonb_build_object('slug',t.slug,'name',t.name,'category',c.slug,'parent',p.slug,'aliases',t.aliases,'is_active',t.is_active,'wellness',t.wellness,'family_friendly',t.family_friendly,'related',coalesce((SELECT jsonb_agg(jsonb_build_object('target',dst.slug,'kind',r.kind,'symmetric',r.symmetric,'note',r.note) ORDER BY r.id) FROM taxonomy_activityrelation r JOIN taxonomy_activitytype dst ON dst.id=r.target_id WHERE r.source_id=t.id),'[]'::jsonb))`

func (s *Service) types(w http.ResponseWriter, r *http.Request) {
	limit, offset := paging(r)
	var count int64
	if err := s.DB.QueryRow(r.Context(), `SELECT count(*) FROM taxonomy_activitytype`).Scan(&count); err != nil {
		platform.Fail(w, err)
		return
	}
	data, err := rows(r.Context(), s.DB, `SELECT `+typeProjection+` FROM taxonomy_activitytype t LEFT JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitytype p ON p.id=t.parent_id ORDER BY t.slug LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, page(r, count, data, limit, offset))
}
func (s *Service) typeDetail(w http.ResponseWriter, r *http.Request) {
	data, err := object(r.Context(), s.DB, `SELECT `+typeProjection+` FROM taxonomy_activitytype t LEFT JOIN taxonomy_activitycategory c ON c.id=t.category_id LEFT JOIN taxonomy_activitytype p ON p.id=t.parent_id WHERE t.slug=$1`, r.PathValue("slug"))
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, data)
}
