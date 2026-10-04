package social

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

const matchingTypes = `WITH matches AS(SELECT mt.id FROM taxonomy_activitytype mt WHERE mt.is_active AND (UPPER(mt.name) LIKE '%'||UPPER($3)||'%' OR UPPER(mt.slug) LIKE '%'||UPPER($3)||'%' OR EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(mt.aliases)='array' THEN mt.aliases ELSE '[]'::jsonb END) alias(value) WHERE jsonb_typeof(alias.value)='string' AND UPPER(alias.value#>>'{}') LIKE '%'||UPPER($3)||'%'))),matched_types AS(SELECT id FROM matches UNION SELECT r.target_id FROM taxonomy_activityrelation r WHERE r.kind IN ('synonym','variant') AND r.source_id IN (SELECT id FROM matches) UNION SELECT r.source_id FROM taxonomy_activityrelation r WHERE r.symmetric AND r.kind IN ('synonym','variant') AND r.target_id IN (SELECT id FROM matches)) `
const activitySearch = `($3='' OR UPPER(a.title) LIKE '%'||UPPER($3)||'%' OR UPPER(a.description) LIKE '%'||UPPER($3)||'%' OR UPPER(p.name) LIKE '%'||UPPER($3)||'%' OR UPPER(t.name) LIKE '%'||UPPER($3)||'%' OR a.activity_type_id IN (SELECT id FROM matched_types) OR EXISTS(SELECT 1 FROM social_activity_secondary_types extra JOIN taxonomy_activitytype st ON st.id=extra.activitytype_id WHERE extra.activity_id=a.id AND (UPPER(st.name) LIKE '%'||UPPER($3)||'%' OR st.id IN (SELECT id FROM matched_types))))`

func (s *Service) SearchActivities(ctx context.Context, a Actor, query string, beginners bool) ([]json.RawMessage, error) {
	query = strings.TrimSpace(query)
	if !assigned(a) || utf8.RuneCountInString(query) < 2 {
		return []json.RawMessage{}, nil
	}
	return objects(ctx, s.DB, matchingTypes+`SELECT `+activityColumns+activityJoin+` JOIN places_place p ON p.id=a.place_id WHERE a.cohort=$2 AND NOT a.is_hidden AND `+blockOwner+` AND a.status='open' AND a.starts_at>=now() AND (NOT $4 OR a.beginners_welcome) AND `+activitySearch+` ORDER BY a.starts_at,a.id LIMIT 100`, a.ID, a.Cohort, escapeLike(query), beginners)
}
func (s *Service) SearchThread(ctx context.Context, a Actor, kind string, id int64, query string) ([]json.RawMessage, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) < 2 {
		return []json.RawMessage{}, nil
	}
	v, err := threadOwner(ctx, s.DB, a, kind, id, false)
	if err != nil {
		return nil, err
	}
	if err := threadGate(ctx, s.DB, a, v, false); err != nil {
		return nil, err
	}
	return objects(ctx, s.DB, `SELECT `+postProjection(ctx)+postJoin+` WHERE po.thread_id=$1 AND NOT po.is_hidden AND UPPER(po.body) LIKE '%'||UPPER($3)||'%' ORDER BY po.created_at DESC,po.id DESC LIMIT 50`, v.ThreadID, a.Cohort, escapeLike(query))
}
func (s *Service) DidYouMean(ctx context.Context, a Actor, query string) (*string, error) {
	if utf8.RuneCountInString(strings.TrimSpace(query)) < 2 {
		return nil, nil
	}
	var name string
	err := s.DB.QueryRow(ctx, `SELECT t.name FROM taxonomy_activitytype t WHERE t.is_active AND similarity(t.name,$3)>0.3 AND EXISTS(SELECT 1 FROM social_activity a WHERE a.activity_type_id=t.id AND a.cohort=$2 AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=now() AND `+blockOwner+`) ORDER BY similarity(t.name,$3) DESC,t.name LIMIT 1`, a.ID, a.Cohort, query).Scan(&name)
	if _, actual := authorizedResult(err); err != nil && actual == nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &name, nil
}
