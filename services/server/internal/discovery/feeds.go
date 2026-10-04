package discovery

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

const eventJoins = ` FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id LEFT JOIN taxonomy_activitytype t ON t.id=e.activity_type_id `
const upcoming = `NOT e.is_tombstone AND NOT e.is_import_held AND e.starts_at>=now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out') AND (e.place_id IS NULL OR (` + catalog.PublicPlaceSQL + `))`
const eventCard = `jsonb_build_object('id',e.id,'title',e.title,'starts_at',e.starts_at,'ends_at',e.ends_at,'url',e.url,'activity_type',t.slug,'place_id',e.place_id,'place_name',p.name,'distance_m',DISTANCE)`

func (s *Service) NearMe(ctx context.Context, a platform.Actor, near catalog.Near, activity string, bookable, wellness, family, events bool, cap int) ([]map[string]any, error) {
	distance := `CASE WHEN $7::double precision IS NULL OR $8::double precision IS NULL THEN NULL ELSE round(ST_Distance(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography)::numeric,1) END`
	where := catalog.PublicPlaceSQL + ` AND ($1::text='' OR EXISTS(SELECT 1 FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND NOT pa.is_disputed AND t.slug=$1)) AND (NOT $2 OR p.website<>'') AND (NOT $3 OR EXISTS(SELECT 1 FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND t.wellness)) AND (NOT $4 OR EXISTS(SELECT 1 FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND t.family_friendly)) AND (NOT $5 OR EXISTS(SELECT 1 FROM events_event e WHERE e.place_id=p.id AND NOT e.is_tombstone AND NOT e.is_import_held AND e.starts_at>=now() AND e.lifecycle_status IN('scheduled','rescheduled','sold_out'))) AND ($7::double precision IS NULL OR $8::double precision IS NULL OR (p.location IS NOT NULL AND ($9::double precision IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($7,$8),4326)::geography,$9)))) AND $6::bigint>=0`
	order := `p.id`
	if near.Lon != nil && near.Lat != nil {
		order = distance + `,p.id`
	}
	data, err := query(ctx, s.DB, `SELECT jsonb_build_object('id',p.id,'name',p.name,'address_city',p.address_city,'lon',ST_X(p.location::geometry),'lat',ST_Y(p.location::geometry),'distance_m',`+distance+`,'is_bookable',p.website<>'','website',p.website,'activities',coalesce((SELECT jsonb_agg(t.slug ORDER BY pa.id) FROM places_placeactivity pa JOIN taxonomy_activitytype t ON t.id=pa.activity_id WHERE pa.place_id=p.id AND NOT pa.is_disputed),'[]'::jsonb),'raw_tags',p.raw_tags) FROM places_place p WHERE `+where+` ORDER BY `+order+` LIMIT $10`, activity, bookable, wellness, family, events, a.ID, near.Lon, near.Lat, near.Radius, cap)
	if err != nil {
		return nil, err
	}
	pref, err := s.Catalog.Access(ctx, a)
	if err != nil {
		return nil, err
	}
	for _, item := range data {
		tags, _ := item["raw_tags"].(map[string]any)
		item["access_match"] = catalog.MatchesAccess(catalog.AccessibilityFacts(tags), pref) == "match"
		delete(item, "raw_tags")
	}
	sort.SliceStable(data, func(i, j int) bool { return data[i]["access_match"].(bool) && !data[j]["access_match"].(bool) })
	for _, item := range data {
		delete(item, "access_match")
	}
	return data, nil
}
func (s *Service) nearMe(w http.ResponseWriter, r *http.Request) {
	a, _ := platform.ActorFrom(r)
	cap := 100
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		cap = 300
	}
	data, err := s.NearMe(r.Context(), a, nearRequest(r), r.URL.Query().Get("activity"), truth(r, "bookable"), truth(r, "wellness"), truth(r, "family_friendly"), truth(r, "has_events"), cap)
	if err != nil {
		finish(w, nil, err)
		return
	}
	value, err := s.page(r, data, 200)
	finish(w, value, err)
}
func (s *Service) Happening(ctx context.Context, near catalog.Near, activity, search string, days *int, limit, offset int) ([]map[string]any, error) {
	if len([]rune(search)) < 2 {
		search = ""
	}
	var until *time.Time
	if days != nil && *days > -365000 && *days < 365000 {
		v := time.Now().AddDate(0, 0, *days)
		until = &v
	}
	distance := `CASE WHEN $4::double precision IS NULL OR $5::double precision IS NULL THEN NULL ELSE round(ST_Distance(p.location,ST_SetSRID(ST_MakePoint($4,$5),4326)::geography)::numeric,1) END`
	where := upcoming + ` AND (SELECT count(*) FROM events_eventreport er WHERE er.event_id=e.id AND er.created_at>=now()-interval '14 days')<3 AND ($1::text='' OR t.slug=$1) AND ($2::text='' OR e.title ILIKE '%'||$2||'%' OR e.description ILIKE '%'||$2||'%' OR p.name ILIKE '%'||$2||'%') AND ($3::timestamptz IS NULL OR e.starts_at<=$3) AND ($4::double precision IS NULL OR $5::double precision IS NULL OR (p.location IS NOT NULL AND ($6::double precision IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($4,$5),4326)::geography,$6))))`
	return query(ctx, s.DB, `SELECT `+strings.ReplaceAll(eventCard, "DISTANCE", distance)+eventJoins+` WHERE `+where+` ORDER BY e.starts_at,e.id LIMIT $7 OFFSET $8`, activity, escapeLike(search), until, near.Lon, near.Lat, near.Radius, limit, offset)
}
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}
func (s *Service) happening(w http.ResponseWriter, r *http.Request) {
	var days *int
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = &n
	}
	limit, offset := s.window(r)
	data, err := s.Happening(r.Context(), nearRequest(r), r.URL.Query().Get("activity"), strings.TrimSpace(r.URL.Query().Get("q")), days, limit+1, offset)
	if err != nil {
		finish(w, nil, err)
		return
	}
	value, err := s.windowBody(r, data, limit, offset)
	finish(w, value, err)
}
func (s *Service) HomeFeed(ctx context.Context, a platform.Actor, near catalog.Near) (map[string]any, error) {
	records, err := s.Recommendations.Recommend(ctx, a, 8, near, true)
	if err != nil {
		return nil, err
	}
	recommended := []map[string]any{}
	seen := map[int64]bool{}
	for _, record := range records {
		seen[record.ID] = true
		item := map[string]any{"id": record.ID, "title": record.Data["title"], "cohort": record.Data["cohort"], "starts_at": record.Data["starts_at"], "status": record.Data["status"], "activity_type": record.TypeSlug, "place_id": record.Data["place"], "distance_m": nil, "place_name": record.PlaceName, "reason": record.Reason}
		recommended = append(recommended, item)
	}
	if err := s.visuals(ctx, a, recommended); err != nil {
		return nil, err
	}
	candidates, err := s.activityCards(ctx, a, catalog.Near{}, "", false, true, nil, nil, 14, 0)
	if err != nil {
		return nil, err
	}
	beginners := []map[string]any{}
	for _, item := range candidates {
		if seen[itemID(item)] {
			continue
		}
		delete(item, "description")
		item["reason"] = ""
		beginners = append(beginners, item)
		if len(beginners) == 6 {
			break
		}
	}
	events, err := query(ctx, s.DB, `SELECT `+strings.ReplaceAll(eventCard, "DISTANCE", "NULL")+` || jsonb_build_object('reason',CASE WHEN i.id IS NULL THEN '' ELSE 'matches your interest in '||t.name END)`+eventJoins+` LEFT JOIN recommendations_userinterest i ON i.activity_type_id=t.id AND i.user_id=$1 WHERE `+upcoming+` ORDER BY (i.id IS NOT NULL) DESC,e.starts_at,e.id LIMIT 6`, a.ID)
	if err != nil {
		return nil, err
	}
	updates, err := query(ctx, s.DB, `SELECT jsonb_build_object('group_id',g.id,'group_title',g.title,'body',left(post.body,280),'created_at',post.created_at) FROM social_post post JOIN social_thread th ON th.id=post.thread_id JOIN social_group g ON g.id=th.group_id WHERE g.cohort=$2 AND g.status='active' AND NOT g.is_hidden AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=g.owner_id) OR (b.blocker_id=g.owner_id AND b.blocked_id=$1)) AND EXISTS(SELECT 1 FROM social_groupmembership m WHERE m.group_id=g.id AND m.user_id=$1 AND m.state='member') AND post.is_announcement AND NOT post.is_hidden ORDER BY post.created_at DESC,post.id DESC LIMIT 5`, a.ID, a.Cohort)
	if err != nil {
		return nil, err
	}
	return map[string]any{"recommended": recommended, "beginners": beginners, "events": events, "group_updates": updates}, nil
}
func (s *Service) feed(w http.ResponseWriter, r *http.Request) {
	a, _ := platform.ActorFrom(r)
	near := nearRequest(r)
	if near.Lon != nil && near.Lat != nil && near.Radius == nil {
		radius := 10000.0
		near.Radius = &radius
	}
	data, err := s.HomeFeed(r.Context(), a, near)
	finish(w, data, err)
}
