package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

const communityColumns = `jsonb_build_object('slug',c.slug,'name',c.name,'tier',c.tier,'area',ar.name,'category',cat.name,'activity_type',t.name)`
const communityJoin = ` FROM communities_community c JOIN communities_area ar ON ar.id=c.area_id JOIN taxonomy_activitycategory cat ON cat.id=c.category_id LEFT JOIN taxonomy_activitytype t ON t.id=c.activity_type_id `

func (s *Service) communitiesList(w http.ResponseWriter, r *http.Request, a Actor) {
	if !assigned(a) {
		platform.JSON(w, 200, []any{})
		return
	}
	rows, err := objects(r.Context(), s.DB, `SELECT `+communityColumns+communityJoin+` WHERE c.cohort=$1 AND c.is_published ORDER BY c.name,c.tier LIMIT 1000`, a.Cohort)
	response(w, rows, err, 200)
}
func (s *Service) communityDetail(w http.ResponseWriter, r *http.Request, a Actor) {
	if !assigned(a) {
		platform.Fail(w, platform.ErrNotFound)
		return
	}
	v, err := object(r.Context(), s.DB, `SELECT `+communityColumns+communityJoin+` WHERE c.slug=$1 AND c.cohort=$2 AND c.is_published`, r.PathValue("slug"), a.Cohort)
	response(w, v, err, 200)
}
func (s *Service) communityActivities(w http.ResponseWriter, r *http.Request, a Actor) {
	var city string
	var tid *int64
	var cid int64
	err := s.DB.QueryRow(r.Context(), `SELECT ar.city,c.activity_type_id,c.category_id FROM communities_community c JOIN communities_area ar ON ar.id=c.area_id WHERE c.slug=$1 AND c.cohort=$2 AND c.is_published`, r.PathValue("slug"), a.Cohort).Scan(&city, &tid, &cid)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	rows, err := s.coordinateActivities(r.Context(), a, city, tid, cid)
	response(w, rows, err, 200)
}
func (s *Service) coordinateActivities(ctx context.Context, a Actor, city string, typeID *int64, categoryID int64) ([]json.RawMessage, error) {
	if !assigned(a) {
		return []json.RawMessage{}, nil
	}
	rows, err := objects(ctx, s.DB, `SELECT jsonb_build_object('id',a.id,'title',a.title,'cohort',a.cohort,'starts_at',a.starts_at,'status',a.status,'activity_type',t.slug,'place_id',a.place_id,'distance_m',NULL)`+activityJoin+` JOIN places_place p ON p.id=a.place_id WHERE a.cohort=$2 AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=now() AND `+blockOwner+` AND lower(p.address_city)=lower($3) AND (($4::bigint IS NOT NULL AND a.activity_type_id=$4) OR ($4::bigint IS NULL AND t.category_id=$5)) ORDER BY a.starts_at,a.id LIMIT $6`, a.ID, a.Cohort, city, typeID, categoryID, s.Policy.CommunityActivitiesPageSize)
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 && s.ActivityVisual == nil && s.ActivityVisuals == nil {
		return nil, fmt.Errorf("activity visual adapter unavailable")
	}
	var visuals map[int64]any
	if s.ActivityVisuals != nil && len(rows) > 0 {
		ids := make([]int64, len(rows))
		for i, raw := range rows {
			var row struct {
				ID int64 `json:"id"`
			}
			if err := json.Unmarshal(raw, &row); err != nil {
				return nil, err
			}
			ids[i] = row.ID
		}
		visuals, err = s.ActivityVisuals(ctx, s.DB, a, ids)
		if err != nil {
			return nil, err
		}
	}
	for i, raw := range rows {
		var card map[string]any
		if err := json.Unmarshal(raw, &card); err != nil {
			return nil, err
		}
		id := int64(card["id"].(float64))
		var visual any
		var err error
		if visuals != nil {
			var ok bool
			visual, ok = visuals[id]
			if !ok {
				return nil, fmt.Errorf("activity visual not authorized")
			}
		} else {
			visual, err = s.ActivityVisual(ctx, s.DB, a, id)
		}
		if err != nil {
			return nil, err
		}
		card["visual"] = visual
		body, err := json.Marshal(card)
		if err != nil {
			return nil, err
		}
		rows[i] = body
	}
	return rows, nil
}
func (s *Service) communityGraph(w http.ResponseWriter, r *http.Request, a Actor) {
	v, err := s.CommunityGraph(r.Context(), a)
	response(w, v, err, 200)
}
func (s *Service) CommunityGraph(ctx context.Context, a Actor) (map[string]any, error) {
	out := map[string]any{"nodes": []any{}, "links": []any{}}
	if !assigned(a) {
		return out, nil
	}
	nodes := []any{}
	links := []any{}
	rows, err := s.DB.Query(ctx, `SELECT cat.id,cat.slug,cat.name,cat.parent_id FROM taxonomy_activitycategory cat WHERE cat.id IN (SELECT category_id FROM communities_community WHERE cohort=$1 AND is_published) ORDER BY cat.id`, a.Cohort)
	if err != nil {
		return nil, err
	}
	type cat struct {
		id     int64
		slug   string
		parent *int64
	}
	cats := map[int64]cat{}
	for rows.Next() {
		var c cat
		var name string
		if err := rows.Scan(&c.id, &c.slug, &name, &c.parent); err != nil {
			rows.Close()
			return nil, err
		}
		cats[c.id] = c
		nodes = append(nodes, map[string]any{"id": "cat:" + c.slug, "kind": "category", "label": name})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(cats) == 0 {
		return out, nil
	}
	for _, c := range cats {
		if c.parent != nil {
			if parent, ok := cats[*c.parent]; ok {
				links = append(links, map[string]any{"source": "cat:" + parent.slug, "target": "cat:" + c.slug, "kind": "parent"})
			}
		}
	}
	rows, err = s.DB.Query(ctx, `SELECT t.id,t.slug,t.name,t.category_id FROM taxonomy_activitytype t WHERE t.id IN (SELECT activity_type_id FROM communities_community WHERE cohort=$1 AND is_published AND activity_type_id IS NOT NULL) ORDER BY t.id`, a.Cohort)
	if err != nil {
		return nil, err
	}
	types := map[int64]string{}
	for rows.Next() {
		var id, cid int64
		var slug, name string
		if err := rows.Scan(&id, &slug, &name, &cid); err != nil {
			rows.Close()
			return nil, err
		}
		types[id] = slug
		nodes = append(nodes, map[string]any{"id": "type:" + slug, "kind": "type", "label": name})
		if c, ok := cats[cid]; ok {
			links = append(links, map[string]any{"source": "cat:" + c.slug, "target": "type:" + slug, "kind": "contains"})
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `SELECT c.slug,c.name,c.activity_type_id,c.category_id,(SELECT COUNT(*) FROM social_activity a JOIN places_place p ON p.id=a.place_id JOIN taxonomy_activitytype t ON t.id=a.activity_type_id WHERE a.cohort=$1 AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=now() AND lower(p.address_city)=lower(ar.city) AND ((c.activity_type_id IS NOT NULL AND a.activity_type_id=c.activity_type_id) OR (c.activity_type_id IS NULL AND t.category_id=c.category_id)) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$2 AND b.blocked_id=a.owner_id) OR (b.blocker_id=a.owner_id AND b.blocked_id=$2))) FROM communities_community c JOIN communities_area ar ON ar.id=c.area_id WHERE c.cohort=$1 AND c.is_published ORDER BY c.name,c.tier`, a.Cohort, a.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var slug, name string
		var tid *int64
		var cid, count int64
		if err := rows.Scan(&slug, &name, &tid, &cid, &count); err != nil {
			rows.Close()
			return nil, err
		}
		nid := "comm:" + slug
		nodes = append(nodes, map[string]any{"id": nid, "kind": "community", "label": name, "drill": "/communities/" + slug + "/", "activity_count": count})
		source := ""
		if tid != nil {
			source = "type:" + types[*tid]
		} else {
			source = "cat:" + cats[cid].slug
		}
		links = append(links, map[string]any{"source": source, "target": nid, "kind": "instance"})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = s.DB.Query(ctx, `SELECT src.slug,dst.slug,r.kind FROM taxonomy_activityrelation r JOIN taxonomy_activitytype src ON src.id=r.source_id JOIN taxonomy_activitytype dst ON dst.id=r.target_id WHERE r.source_id IN (SELECT activity_type_id FROM communities_community WHERE cohort=$1 AND is_published) AND r.target_id IN (SELECT activity_type_id FROM communities_community WHERE cohort=$1 AND is_published) ORDER BY r.id`, a.Cohort)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var src, dst, kind string
		if err := rows.Scan(&src, &dst, &kind); err != nil {
			rows.Close()
			return nil, err
		}
		links = append(links, map[string]any{"source": "type:" + src, "target": "type:" + dst, "kind": kind})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	return map[string]any{"nodes": nodes, "links": links}, nil
}
