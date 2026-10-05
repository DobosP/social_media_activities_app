package web

import (
	"errors"
	"net/http"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func (s *Server) topicPage(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	if a.ID < 1 || !a.IsActive {
		return nil, platform.ErrForbidden
	}
	if s.DB == nil || s.Recommendations == nil || s.Social == nil {
		return nil, errors.New("topic service unavailable")
	}
	slugs, err := s.Recommendations.TopicSlugs(r.Context(), a)
	if err != nil {
		return nil, err
	}
	chosen := map[string]bool{}
	for _, slug := range slugs {
		chosen[slug] = true
	}
	rows, err := s.DB.Query(r.Context(), `SELECT slug,name,description FROM taxonomy_activitycategory WHERE parent_id IS NULL ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	categories := []map[string]any{}
	for rows.Next() {
		var slug, name, description string
		if err := rows.Scan(&slug, &name, &description); err != nil {
			return nil, err
		}
		categories = append(categories, map[string]any{"slug": slug, "name": name, "description": description})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	data := pongo2.Context{"categories": categories, "chosen": chosen}
	if err := s.socialNav(r.Context(), a, data); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Server) topicAction(w http.ResponseWriter, r *http.Request, a platform.Actor) {
	if s.Recommendations == nil {
		platform.Error(w, 503, "Topic service unavailable.")
		return
	}
	if _, err := s.Recommendations.SetTopics(r.Context(), a, append([]string{}, r.PostForm["topics"]...)); err != nil {
		platform.Fail(w, err)
		return
	}
	http.Redirect(w, r, "/topics/", http.StatusFound)
}
