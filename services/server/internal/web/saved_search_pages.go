package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5"
)

func init() {
	for _, name := range []string{"saved_search_create", "saved_search_delete"} {
		actions[name] = actionSpec{}
		actionOnly[name] = true
	}
}

func (s *Server) savedSearchPage(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	if s.Recommendations == nil || s.DB == nil {
		return nil, errors.New("saved search service unavailable")
	}
	if err := platform.Participate(r.Context(), s.DB, a); err != nil {
		if errors.Is(err, platform.ErrForbidden) {
			return pongo2.Context{"redirect": "/"}, nil
		}
		return nil, err
	}
	records, err := s.Recommendations.SavedSearches(r.Context(), a)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, raw := range records {
		var row struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(bytes.NewReader(raw)).Decode(&row); err != nil {
			return nil, err
		}
		ids = append(ids, row.ID)
	}
	items, err := s.accountObjects(r.Context(), `SELECT jsonb_build_object('id',ss.id,'pk',ss.id,'activity_type',CASE WHEN t.id IS NULL THEN NULL ELSE jsonb_build_object('id',t.id,'name',t.name) END,'category',CASE WHEN c.id IS NULL THEN NULL ELSE jsonb_build_object('id',c.id,'name',c.name) END,'area',CASE WHEN ar.id IS NULL THEN NULL ELSE jsonb_build_object('id',ar.id,'name',ar.name) END,'cost_band',ss.cost_band,'coarse_window',ss.coarse_window,'beginners',ss.beginners) FROM saved_searches_savedsearch ss LEFT JOIN taxonomy_activitytype t ON t.id=ss.activity_type_id LEFT JOIN taxonomy_activitycategory c ON c.id=ss.category_id LEFT JOIN communities_area ar ON ar.id=ss.area_id WHERE ss.user_id=$1 AND ss.id=ANY($2) ORDER BY ss.created_at DESC,ss.id DESC`, a.ID, ids)
	if err != nil {
		return nil, err
	}
	for i, item := range items {
		items[i] = socialModel(item)
	}
	types, err := s.accountObjects(r.Context(), `SELECT jsonb_build_object('id',id,'pk',id,'slug',slug,'name',name) FROM taxonomy_activitytype WHERE is_active ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	categories, err := s.accountObjects(r.Context(), `SELECT jsonb_build_object('id',id,'pk',id,'slug',slug,'name',name) FROM taxonomy_activitycategory ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	data := pongo2.Context{"items": items, "activity_types": types, "categories": categories,
		"cost_bands":     [][]string{{"unspecified", "Not specified"}, {"free", "Free"}, {"low", "Low cost"}, {"paid", "Paid"}},
		"coarse_windows": [][]string{{"weekday_daytime", "Weekday daytime"}, {"weekday_evening", "Weekday evening"}, {"weekend_daytime", "Weekend daytime"}, {"weekend_evening", "Weekend evening"}},
	}
	if err := s.socialNav(r.Context(), a, data); err != nil {
		return nil, err
	}
	return data, nil
}

// Source forms accept either a public slug or a database ID. Resolve only
// taxonomy here; native creation owns eligibility, budgets and city adoption.
func (s *Server) savedSearchSelection(r *http.Request, value string, activityType bool) (*int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	parsed, _ := strconv.ParseInt(value, 10, 64)
	sql := `SELECT id FROM taxonomy_activitycategory WHERE id=$1 OR slug=$2 ORDER BY (id=$1) DESC LIMIT 1`
	if activityType {
		sql = `SELECT id FROM taxonomy_activitytype WHERE is_active AND (id=$1 OR slug=$2) ORDER BY (id=$1) DESC LIMIT 1`
	}
	var id int64
	err := s.DB.QueryRow(r.Context(), sql, parsed, value).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

func (s *Server) savedSearchAction(w http.ResponseWriter, r *http.Request, a platform.Actor, name string) {
	if s.Recommendations == nil || s.DB == nil {
		platform.Error(w, 503, "Saved search service unavailable.")
		return
	}
	var err error
	if name == "saved_search_delete" {
		err = s.Recommendations.DeleteSavedSearch(r.Context(), a, id(r, "pk"))
	} else {
		var activityType, category *int64
		activityType, err = s.savedSearchSelection(r, r.PostForm.Get("activity_type"), true)
		if err == nil {
			category, err = s.savedSearchSelection(r, r.PostForm.Get("category"), false)
		}
		if err == nil {
			_, err = s.Recommendations.CreateSavedSearch(r.Context(), a, recommendations.SavedSearchInput{ActivityType: activityType, Category: category, City: r.PostForm.Get("city"), Beginners: r.PostForm.Get("beginners") == "on", CostBand: r.PostForm.Get("cost_band"), CoarseWindow: r.PostForm.Get("coarse_window")})
		}
		if errors.Is(err, platform.ErrInvalid) || errors.Is(err, platform.ErrForbidden) {
			// Expected eligibility/selection refusals return to the source panel;
			// they never create a search or adopt a city.
			err = nil
		}
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	http.Redirect(w, r, socialSafeNext(r, "/saved-searches/"), http.StatusFound)
}
