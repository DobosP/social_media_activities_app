package web

import (
	"errors"
	"net/http"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// interestPage adapts the native interest options and recommendation choices to
// the existing category-grouped picker used by both legacy HTML and SPA views.
func (s *Server) interestPage(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	if a.ID < 1 || !a.IsActive {
		return nil, platform.ErrForbidden
	}
	if s.DB == nil || s.API == nil || s.Recommendations == nil || s.Social == nil {
		return nil, errors.New("interest service unavailable")
	}
	chosenSlugs, err := s.Recommendations.InterestSlugs(r.Context(), a)
	if err != nil {
		return nil, err
	}
	chosen := map[string]bool{}
	for _, slug := range chosenSlugs {
		chosen[slug] = true
	}
	starter, err := s.Recommendations.StarterInterests(r.Context(), a, 12)
	if err != nil {
		return nil, err
	}
	starterSlugs := map[string]bool{}
	for _, row := range starter {
		starterSlugs[spaText(row["slug"])] = true
	}
	value, err := s.get(r, "/api/recommendations/interests/options/")
	if err != nil {
		return nil, err
	}
	options, slugs := map[string]map[string]any{}, []string{}
	for _, row := range spaRows(spaMap(value)["options"]) {
		slug := spaText(row["slug"])
		options[slug] = row
		slugs = append(slugs, slug)
	}
	// The public options contract supplies each active choice. Only its category
	// labels and the original database ordering are needed for presentation.
	rows, err := s.DB.Query(r.Context(), `SELECT t.slug,c.slug,c.name FROM taxonomy_activitytype t JOIN taxonomy_activitycategory c ON c.id=t.category_id WHERE t.slug=ANY($1) ORDER BY c.name,t.name`, slugs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := [][]any{}
	byCategory := map[string]int{}
	for rows.Next() {
		var slug, category, name string
		if err := rows.Scan(&slug, &category, &name); err != nil {
			return nil, err
		}
		if starterSlugs[slug] {
			continue
		}
		index, exists := byCategory[category]
		if !exists {
			index = len(groups)
			byCategory[category] = index
			groups = append(groups, []any{map[string]any{"slug": category, "name": name}, []map[string]any{}})
		}
		groups[index][1] = append(groups[index][1].([]map[string]any), options[slug])
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	data := pongo2.Context{"groups": groups, "chosen": chosen, "chosen_count": len(chosen), "starter": starter}
	if err := s.socialNav(r.Context(), a, data); err != nil {
		return nil, err
	}
	return data, nil
}

// The HTML action's outer dispatcher has already enforced authentication and
// CSRF. Preserve getlist semantics for singleton and empty checkbox selections.
func (s *Server) interestAction(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	if s.Recommendations == nil {
		platform.Error(w, 503, "Interest service unavailable.")
		return
	}
	slugs := append([]string{}, r.PostForm["interests"]...)
	if _, err := s.Recommendations.SetInterests(r.Context(), a, slugs); err != nil {
		platform.Fail(w, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}
