package recommendations

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	DB            *pgxpool.Pool
	Catalog       *catalog.Service
	Social        *social.Service
	Cursor        platform.CursorCodec
	mu            sync.Mutex
	createBudgets map[int64][]time.Time
}

func New(db *pgxpool.Pool, cat *catalog.Service, soc *social.Service) *Service {
	return &Service{DB: db, Catalog: cat, Social: soc, createBudgets: map[int64][]time.Time{}}
}
func auth(fn func(http.ResponseWriter, *http.Request, platform.Actor)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a, ok := platform.RequireActor(w, r)
		if !ok {
			return
		}
		ctx, cancel := platform.Timeout(r)
		defer cancel()
		fn(w, r.WithContext(ctx), a)
	}
}
func (s *Service) Register(mux *http.ServeMux) {
	s.registerSavedSearches(mux)
	for _, base := range []string{"/api/v1/recommendations/", "/api/recommendations/"} {
		mux.HandleFunc("GET "+base+"interests/{$}", auth(s.interests))
		mux.HandleFunc("PUT "+base+"interests/{$}", auth(s.interests))
		mux.HandleFunc("GET "+base+"topics/{$}", auth(s.topics))
		mux.HandleFunc("PUT "+base+"topics/{$}", auth(s.topics))
		mux.HandleFunc("GET "+base+"activities/{$}", auth(s.activities))
		mux.HandleFunc("GET "+base+"interests/options/{$}", auth(s.options))
	}
}
func HashEmbed(tokens []string) [64]float64 {
	var vec [64]float64
	for _, token := range tokens {
		digest := sha256.Sum256([]byte(token))
		idx := binary.BigEndian.Uint32(digest[:4]) % 64
		sign := -1.0
		if digest[4]&1 != 0 {
			sign = 1
		}
		vec[idx] += sign
	}
	sum := 0.0
	for _, v := range vec {
		sum += v * v
	}
	if sum > 0 {
		norm := math.Sqrt(sum)
		for i := range vec {
			vec[i] /= norm
		}
	}
	return vec
}
func vectorText(vec [64]float64) string {
	parts := make([]string, 64)
	for i, v := range vec {
		parts[i] = strconv.FormatFloat(v, 'g', -1, 64)
	}
	return "[" + strings.Join(parts, ",") + "]"
}
func typeTokens(ctx context.Context, q platform.Querier, ids []int64) (map[int64][]string, error) {
	rows, err := q.Query(ctx, `WITH RECURSIVE ancestry AS(SELECT t.id type_id,t.slug type_slug,c.id,c.parent_id,c.slug,1 depth FROM taxonomy_activitytype t JOIN taxonomy_activitycategory c ON c.id=t.category_id WHERE t.id=ANY($1) UNION ALL SELECT a.type_id,a.type_slug,c.id,c.parent_id,c.slug,a.depth+1 FROM ancestry a JOIN taxonomy_activitycategory c ON c.id=a.parent_id WHERE a.depth<16) SELECT type_id,type_slug,slug FROM ancestry ORDER BY type_id,depth`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var typ, cat string
		if err := rows.Scan(&id, &typ, &cat); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = []string{"type:" + typ}
		}
		out[id] = append(out[id], "cat:"+cat)
	}
	return out, rows.Err()
}
func (s *Service) UserVector(ctx context.Context, a platform.Actor) ([64]float64, error) {
	var empty [64]float64
	rows, err := s.DB.Query(ctx, `SELECT activity_type_id FROM recommendations_userinterest WHERE user_id=$1 UNION ALL SELECT a.activity_type_id FROM social_membership m JOIN social_activity a ON a.id=m.activity_id WHERE m.user_id=$1 AND m.state='member'`, a.ID)
	if err != nil {
		return empty, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return empty, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return empty, err
	}
	lookup, err := typeTokens(ctx, s.DB, ids)
	if err != nil {
		return empty, err
	}
	tokens := []string{}
	for _, id := range ids {
		tokens = append(tokens, lookup[id]...)
	}
	return HashEmbed(tokens), nil
}

// RecomputeEmbeddingTx is the native Activity post-save signal. The host wires
// it into social.AfterActivitySave so new warm recommendations appear at commit.
func (s *Service) RecomputeEmbeddingTx(ctx context.Context, tx pgx.Tx, id int64) error {
	var typ int64
	if err := tx.QueryRow(ctx, `SELECT activity_type_id FROM social_activity WHERE id=$1`, id).Scan(&typ); err != nil {
		return err
	}
	tokens, err := typeTokens(ctx, tx, []int64{typ})
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO recommendations_activityembedding(activity_id,vector,updated_at) VALUES($1,$2::text::vector,now()) ON CONFLICT(activity_id) DO UPDATE SET vector=EXCLUDED.vector,updated_at=now()`, id, vectorText(HashEmbed(tokens[typ])))
	return err
}
func (s *Service) RecomputeEmbedding(ctx context.Context, id int64) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error { return s.RecomputeEmbeddingTx(ctx, tx, id) })
}

// Backfill works in fixed batches and reuses one vector per type: three reads/
// writes per 500 activities instead of repeating taxonomy lookups for each row.
func (s *Service) RecomputeEmbeddings(ctx context.Context) (int, error) {
	after := int64(0)
	total := 0
	for {
		processed := 0
		next := after
		err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			rows, err := tx.Query(ctx, `SELECT id,activity_type_id FROM social_activity WHERE id>$1 ORDER BY id LIMIT 500`, after)
			if err != nil {
				return err
			}
			ids, types := []int64{}, []int64{}
			for rows.Next() {
				var id, typ int64
				if err := rows.Scan(&id, &typ); err != nil {
					rows.Close()
					return err
				}
				ids = append(ids, id)
				types = append(types, typ)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			if len(ids) == 0 {
				return nil
			}
			tokens, err := typeTokens(ctx, tx, types)
			if err != nil {
				return err
			}
			cache := map[int64]string{}
			vectors := make([]string, len(ids))
			for i, typ := range types {
				value, ok := cache[typ]
				if !ok {
					value = vectorText(HashEmbed(tokens[typ]))
					cache[typ] = value
				}
				vectors[i] = value
			}
			if _, err := tx.Exec(ctx, `INSERT INTO recommendations_activityembedding(activity_id,vector,updated_at) SELECT id,embedding::vector,now() FROM unnest($1::bigint[],$2::text[]) AS batch(id,embedding) ON CONFLICT(activity_id) DO UPDATE SET vector=EXCLUDED.vector,updated_at=now()`, ids, vectors); err != nil {
				return err
			}
			processed = len(ids)
			next = ids[len(ids)-1]
			return nil
		})
		if err != nil {
			return total, err
		}
		if processed == 0 {
			return total, nil
		}
		total += processed
		after = next
	}
}
func (s *Service) InterestSlugs(ctx context.Context, a platform.Actor) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT t.slug FROM recommendations_userinterest i JOIN taxonomy_activitytype t ON t.id=i.activity_type_id WHERE i.user_id=$1 ORDER BY t.slug`, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}
func (s *Service) SetInterests(ctx context.Context, a platform.Actor, slugs []string) ([]string, error) {
	if len(slugs) > 1000 {
		return nil, platform.ErrInvalid
	}
	known := []string{}
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if a.ID < 1 || !a.IsActive {
			return platform.ErrForbidden
		}
		if _, err := tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, a.ID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,slug FROM taxonomy_activitytype WHERE slug=ANY($1) AND is_active ORDER BY slug`, slugs)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			var slug string
			if err := rows.Scan(&id, &slug); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			known = append(known, slug)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM recommendations_userinterest WHERE user_id=$1`, a.ID); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.Exec(ctx, `INSERT INTO recommendations_userinterest(user_id,activity_type_id,created_at) VALUES($1,$2,now())`, a.ID, id); err != nil {
				return err
			}
		}
		if err := accounts.RefreshAvatarFingerprint(ctx, tx, a); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, a, "recommendations.interests_updated", "accounts.user:"+strconv.FormatInt(a.ID, 10), nil)
	})
	return known, err
}
func (s *Service) TopicSlugs(ctx context.Context, a platform.Actor) ([]string, error) {
	rows, err := s.DB.Query(ctx, `SELECT c.slug FROM recommendations_topicpreference p JOIN taxonomy_activitycategory c ON c.id=p.category_id WHERE p.user_id=$1 ORDER BY c.slug`, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var slug string
		if err := rows.Scan(&slug); err != nil {
			return nil, err
		}
		out = append(out, slug)
	}
	return out, rows.Err()
}
func (s *Service) SetTopics(ctx context.Context, a platform.Actor, slugs []string) ([]string, error) {
	if len(slugs) > 1000 {
		return nil, platform.ErrInvalid
	}
	known := []string{}
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if a.ID < 1 || !a.IsActive {
			return platform.ErrForbidden
		}
		rows, err := tx.Query(ctx, `SELECT id,slug FROM taxonomy_activitycategory WHERE parent_id IS NULL AND slug=ANY($1) ORDER BY slug`, slugs)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			var slug string
			if err := rows.Scan(&id, &slug); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			known = append(known, slug)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM recommendations_topicpreference WHERE user_id=$1`, a.ID); err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := tx.Exec(ctx, `INSERT INTO recommendations_topicpreference(user_id,category_id,created_at) VALUES($1,$2,now())`, a.ID, id); err != nil {
				return err
			}
		}
		return platform.RecordAudit(ctx, tx, a, "recommendations.topics_updated", "accounts.user:"+strconv.FormatInt(a.ID, 10), nil)
	})
	return known, err
}
func ignored(input, known []string) []string {
	set := map[string]bool{}
	for _, v := range known {
		set[v] = true
	}
	out := []string{}
	for _, v := range input {
		if !set[v] {
			out = append(out, v)
		}
	}
	return out
}
func (s *Service) interests(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	if r.Method == "GET" {
		slugs, err := s.InterestSlugs(r.Context(), a)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		platform.JSON(w, 200, map[string]any{"interests": slugs})
		return
	}
	var in struct {
		Interests []string `json:"interests"`
		Types     []string `json:"activity_types"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	if in.Interests == nil {
		in.Interests = in.Types
	}
	known, err := s.SetInterests(r.Context(), a, in.Interests)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"interests": known, "ignored": ignored(in.Interests, known)})
}
func (s *Service) topics(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	if r.Method == "GET" {
		known, err := s.TopicSlugs(r.Context(), a)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		platform.JSON(w, 200, map[string]any{"topics": known})
		return
	}
	var in struct {
		Topics []string `json:"topics"`
	}
	if err := platform.Decode(w, r, &in); err != nil {
		platform.Fail(w, err)
		return
	}
	known, err := s.SetTopics(r.Context(), a, in.Topics)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"topics": known, "ignored": ignored(in.Topics, known)})
}
func (s *Service) options(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	rows, err := s.DB.Query(r.Context(), `SELECT slug,name FROM taxonomy_activitytype WHERE is_active ORDER BY name`)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var slug, name string
		if err := rows.Scan(&slug, &name); err != nil {
			platform.Fail(w, err)
			return
		}
		out = append(out, map[string]string{"slug": slug, "name": name})
	}
	if err := rows.Err(); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]any{"options": out})
}
func (s *Service) activities(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	limit := platform.ParseLimit(r.URL.Query().Get("limit"), 20, 50)
	near, err := catalog.ParseNear(r.URL.Query(), false)
	if err != nil {
		near = catalog.Near{}
	}
	if near.Lon != nil && near.Lat != nil && near.Radius == nil {
		radius := 10000.0
		near.Radius = &radius
	}
	records, err := s.Recommend(r.Context(), a, limit, near, false)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	data := []map[string]any{}
	for _, item := range records {
		value := item.Data
		if item.Cosine != nil {
			value["match_score"] = math.RoundToEven((1-*item.Cosine)*10000) / 10000
		}
		data = append(data, value)
	}
	platform.JSON(w, 200, map[string]any{"results": data})
}

// SetWardTopics is shared soft steering for an ACTIVE guardian and CHILD ward;
// it does not confer participation or edit the hard guardrail category envelope.
func (s *Service) SetWardTopics(ctx context.Context, guardian platform.Actor, wardID int64, slugs []string) ([]string, error) {
	if guardian.ID < 1 || !guardian.IsActive || len(slugs) > 1000 {
		return nil, platform.ErrForbidden
	}
	known := []string{}
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var relationship int64
		if err := tx.QueryRow(ctx, `SELECT id FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active' FOR UPDATE`, guardian.ID, wardID).Scan(&relationship); err != nil {
			return err
		}
		var cohort string
		if err := tx.QueryRow(ctx, `SELECT cohort FROM accounts_user WHERE id=$1 FOR UPDATE`, wardID).Scan(&cohort); err != nil {
			return err
		}
		if cohort != "child" {
			return platform.ErrForbidden
		}
		rows, err := tx.Query(ctx, `SELECT id,slug FROM taxonomy_activitycategory WHERE parent_id IS NULL AND slug=ANY($1) ORDER BY slug`, slugs)
		if err != nil {
			return err
		}
		ids := []int64{}
		for rows.Next() {
			var id int64
			var slug string
			if err := rows.Scan(&id, &slug); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			known = append(known, slug)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM recommendations_topicpreference WHERE user_id=$1`, wardID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO recommendations_topicpreference(user_id,category_id,created_at) SELECT $1,category,now() FROM unnest($2::bigint[])category`, wardID, ids); err != nil {
			return err
		}
		return platform.RecordAudit(ctx, tx, guardian, "recommendations.ward_topics_updated", "accounts.user:"+strconv.FormatInt(wardID, 10), nil)
	})
	return known, err
}
