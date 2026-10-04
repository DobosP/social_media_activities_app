package recommendations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
)

type SavedSearchInput struct {
	ActivityType *int64 `json:"activity_type"`
	Category     *int64 `json:"category"`
	City         string `json:"city"`
	Beginners    bool   `json:"beginners"`
	CostBand     string `json:"cost_band"`
	CoarseWindow string `json:"coarse_window"`
}

const savedProjection = `jsonb_build_object('id',ss.id,'activity_type',t.slug,'category',c.slug,'area',ar.slug,'beginners',ss.beginners,'cost_band',ss.cost_band,'coarse_window',ss.coarse_window,'created_at',ss.created_at)`
const savedJoin = ` FROM saved_searches_savedsearch ss LEFT JOIN taxonomy_activitytype t ON t.id=ss.activity_type_id LEFT JOIN taxonomy_activitycategory c ON c.id=ss.category_id LEFT JOIN communities_area ar ON ar.id=ss.area_id `

func (s *Service) SavedSearches(ctx context.Context, a platform.Actor) ([]json.RawMessage, error) {
	rows, err := s.DB.Query(ctx, `SELECT `+savedProjection+savedJoin+` WHERE ss.user_id=$1 ORDER BY ss.created_at DESC,ss.id DESC`, a.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []json.RawMessage{}
	for rows.Next() {
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, rows.Err()
}
func (s *Service) SavedSearch(ctx context.Context, a platform.Actor, id int64) (json.RawMessage, error) {
	var raw json.RawMessage
	err := s.DB.QueryRow(ctx, `SELECT `+savedProjection+savedJoin+` WHERE ss.user_id=$1 AND ss.id=$2`, a.ID, id).Scan(&raw)
	return raw, err
}
func (s *Service) CreateSavedSearch(ctx context.Context, a platform.Actor, in SavedSearchInput) (int64, error) {
	in.City = strings.TrimSpace(in.City)
	if (in.ActivityType == nil) == (in.Category == nil) || utf8.RuneCountInString(in.City) > 128 || !map[string]bool{"": true, "unspecified": true, "free": true, "low": true, "paid": true}[in.CostBand] || !map[string]bool{"": true, "weekday_daytime": true, "weekday_evening": true, "weekend_daytime": true, "weekend_evening": true}[in.CoarseWindow] {
		return 0, platform.ErrInvalid
	}
	var id int64
	err := budgets.Reserve(func(reserve func() error) error {
		return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			if err := platform.Participate(ctx, tx, a); err != nil {
				return err
			}
			if a.Cohort == "" || a.Cohort == "unassigned" {
				return platform.ErrForbidden
			}
			var locked int64
			if err := tx.QueryRow(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, a.ID).Scan(&locked); err != nil {
				return err
			}
			var valid bool
			if in.ActivityType != nil {
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitytype WHERE id=$1 AND is_active)`, *in.ActivityType).Scan(&valid); err != nil {
					return err
				}
			} else {
				if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM taxonomy_activitycategory WHERE id=$1)`, *in.Category).Scan(&valid); err != nil {
					return err
				}
			}
			if !valid {
				return platform.ErrInvalid
			}
			if err := reserve(); err != nil {
				return err
			}
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM saved_searches_savedsearch WHERE user_id=$1`, a.ID).Scan(&count); err != nil {
				return err
			}
			if count >= 20 {
				return platform.ErrInvalid
			}
			var area *int64
			if in.City != "" {
				value, err := social.EnsureCityArea(ctx, tx, in.City)
				if err != nil {
					return err
				}
				area = &value
			}
			var duplicate bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM saved_searches_savedsearch WHERE user_id=$1 AND activity_type_id IS NOT DISTINCT FROM $2::bigint AND category_id IS NOT DISTINCT FROM $3::bigint AND area_id IS NOT DISTINCT FROM $4::bigint AND beginners=$5 AND cost_band=$6 AND coarse_window=$7)`, a.ID, in.ActivityType, in.Category, area, in.Beginners, in.CostBand, in.CoarseWindow).Scan(&duplicate); err != nil {
				return err
			}
			if duplicate {
				return platform.ErrInvalid
			}
			if err := tx.QueryRow(ctx, `INSERT INTO saved_searches_savedsearch(user_id,cohort,activity_type_id,category_id,area_id,beginners,cost_band,coarse_window,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now()) RETURNING id`, a.ID, a.Cohort, in.ActivityType, in.Category, area, in.Beginners, in.CostBand, in.CoarseWindow).Scan(&id); err != nil {
				return err
			}
			return platform.RecordAudit(ctx, tx, a, "saved_search.created", fmt.Sprintf("saved_searches.savedsearch:%d", id), map[string]any{})
		})
	}, func() (budgets.Decision, error) {
		policy, err := budgets.Resolve(s.RatePolicies, "saved_search_create", budgets.Policy{Limit: 20, Window: time.Hour})
		if err != nil {
			return budgets.Decision{}, err
		}
		return s.Budgets.Actor(ctx, a.ID, "recommendations.saved_search_create", policy)
	})
	if errors.Is(err, budgets.ErrDenied) {
		err = platform.ErrForbidden
	}
	return id, err
}
func (s *Service) DeleteSavedSearch(ctx context.Context, a platform.Actor, id int64) error {
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `DELETE FROM saved_searches_savedsearch WHERE id=$1 AND user_id=$2`, id, a.ID)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return platform.ErrNotFound
		}
		return platform.RecordAudit(ctx, tx, a, "saved_search.deleted", "", map[string]int64{"saved_search_id": id})
	})
}
func (s *Service) registerSavedSearches(mux *http.ServeMux) {
	for _, base := range []string{"/api/v1/saved-searches/saved-searches/", "/api/saved-searches/saved-searches/"} {
		mux.HandleFunc("GET "+base+"{$}", auth(func(w http.ResponseWriter, r *http.Request, a platform.Actor) {
			data, err := s.SavedSearches(r.Context(), a)
			if err != nil {
				platform.Fail(w, err)
				return
			}
			platform.JSON(w, 200, data)
		}))
		mux.HandleFunc("POST "+base+"{$}", auth(func(w http.ResponseWriter, r *http.Request, a platform.Actor) {
			var in SavedSearchInput
			if err := platform.Decode(w, r, &in); err != nil {
				platform.Fail(w, err)
				return
			}
			id, err := s.CreateSavedSearch(r.Context(), a, in)
			if err != nil {
				platform.Fail(w, err)
				return
			}
			data, err := s.SavedSearch(r.Context(), a, id)
			if err != nil {
				platform.Fail(w, err)
				return
			}
			platform.JSON(w, 201, data)
		}))
		for _, method := range []string{"GET", "DELETE"} {
			mux.HandleFunc(method+" "+base+"{id}/{$}", auth(func(w http.ResponseWriter, r *http.Request, a platform.Actor) {
				id, err := catalog.PositiveID(r.PathValue("id"))
				if err != nil {
					platform.Fail(w, err)
					return
				}
				if r.Method == "DELETE" {
					err = s.DeleteSavedSearch(r.Context(), a, id)
					if err == nil {
						w.WriteHeader(204)
						return
					}
				} else {
					data, e := s.SavedSearch(r.Context(), a, id)
					err = e
					if e == nil {
						platform.JSON(w, 200, data)
						return
					}
				}
				platform.Fail(w, err)
			}))
		}
	}
}
