package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
)

// Source paginator semantics: invalid text means the first page, while an
// out-of-range integer (including zero/negative) means the last page.
func socialPagination(raw string, count, size int) (map[string]any, int) {
	pages := max(1, (count+size-1)/size)
	raw = strings.TrimSpace(raw)
	number, err := strconv.Atoi(raw)
	if err != nil {
		number = 1
		digits := strings.TrimPrefix(strings.TrimPrefix(raw, "+"), "-")
		if digits != "" && strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' }) < 0 {
			number = pages
		}
	}
	if number < 1 || number > pages {
		number = pages
	}
	return map[string]any{"number": number, "paginator": map[string]any{"count": count, "num_pages": pages}, "has_previous": number > 1, "has_next": number < pages, "has_other_pages": pages > 1, "previous_page_number": max(1, number-1), "next_page_number": min(pages, number+1), "object_list": []map[string]any{}}, (number - 1) * size
}

func socialNear(r *http.Request) catalog.Near {
	near, err := catalog.ParseNear(r.URL.Query(), false)
	if err != nil {
		return catalog.Near{}
	}
	return near
}

func (s *Server) socialBrowse(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	data := pongo2.Context{}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	beginners := r.URL.Query().Get("beginners") == "true"
	near := socialNear(r)
	items := []map[string]any{}
	var count int
	var err error
	if q != "" {
		raw, e := s.Social.SearchActivities(r.Context(), a, q, beginners)
		if e != nil {
			return nil, e
		}
		rows := spaRows(raw)
		count = len(rows)
		page, offset := socialPagination(r.URL.Query().Get("page"), count, 24)
		items, err = s.socialActivityModels(r.Context(), a, rows[min(offset, len(rows)):min(offset+24, len(rows))])
		data["page_obj"] = page
		if count == 0 {
			suggestion, e := s.Social.DidYouMean(r.Context(), a, q)
			if e != nil {
				return nil, e
			}
			if suggestion != nil {
				data["did_you_mean"] = *suggestion
				data["did_you_mean_q"] = url.Values{"q": {*suggestion}}.Encode()
			}
		}
	} else {
		where := social.ActivityVisibilitySQL() + ` AND a.status='open' AND a.starts_at>=now() AND (NOT $3 OR a.beginners_welcome) AND ($4::double precision IS NULL OR $5::double precision IS NULL OR (p.location IS NOT NULL AND ($6::double precision IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($4,$5),4326)::geography,$6))))`
		args := []any{a.ID, a.Cohort, beginners, near.Lon, near.Lat, near.Radius}
		err = s.DB.QueryRow(r.Context(), `SELECT COUNT(*) FROM social_activity a JOIN places_place p ON p.id=a.place_id WHERE `+where, args...).Scan(&count)
		if err != nil {
			return nil, err
		}
		page, offset := socialPagination(r.URL.Query().Get("page"), count, 24)
		distance := "NULL"
		order := "a.starts_at,a.id"
		if near.Lon != nil && near.Lat != nil {
			distance = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($4,$5),4326)::geography)`
			order = distance + `,a.starts_at,a.id`
		}
		rows, e := socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('id',a.id,'distance_m',`+distance+`) FROM social_activity a JOIN places_place p ON p.id=a.place_id WHERE `+where+` ORDER BY `+order+` LIMIT 24 OFFSET $7`, append(args, offset)...)
		if e != nil {
			return nil, e
		}
		items, err = s.socialActivityModels(r.Context(), a, rows)
		data["page_obj"] = page
	}
	if err != nil {
		return nil, err
	}
	data["page_obj"].(map[string]any)["object_list"] = items
	data["activities"] = items
	data["query"] = q
	data["beginners_only"] = beginners
	data["near_active"] = q == "" && near.Lon != nil && near.Lat != nil
	data["view_mode"] = "list"
	if r.URL.Query().Get("view") == "cards" {
		data["view_mode"] = "cards"
	}
	params := r.URL.Query()
	params.Del("page")
	params.Del("view")
	data["base_qs"] = params.Encode()
	return data, nil
}

func (s *Server) socialHome(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	if s.Discovery == nil || s.Recommendations == nil {
		return nil, errors.New("home feed unavailable")
	}
	ctx := r.Context()
	near := socialNear(r)
	recommendNear := near
	if near.Lon != nil && near.Lat != nil && recommendNear.Radius == nil {
		radius := 10000.0
		recommendNear.Radius = &radius
	}
	feed, err := s.Discovery.HomeFeed(ctx, a, recommendNear)
	if err != nil {
		return nil, err
	}
	data := pongo2.Context{}
	for _, key := range []string{"recommended", "beginners"} {
		data[key], err = s.socialActivityModels(ctx, a, feed[key])
		if err != nil {
			return nil, err
		}
	}
	data["starter_types"] = []map[string]any{}
	var declared bool
	err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM recommendations_userinterest WHERE user_id=$1)`, a.ID).Scan(&declared)
	if err != nil {
		return nil, err
	}
	if !declared {
		data["starter_types"], err = s.Recommendations.StarterInterests(ctx, a, 6)
		if err != nil {
			return nil, err
		}
	}
	exclude := []int64{}
	for _, row := range spaRows(data["beginners"]) {
		exclude = append(exclude, spaID(row))
	}
	beginners := r.URL.Query().Get("beginners") == "true"
	distance := "NULL"
	order := "a.starts_at,a.id"
	if near.Lon != nil && near.Lat != nil {
		distance = `ST_Distance(p.location,ST_SetSRID(ST_MakePoint($4,$5),4326)::geography)`
		order = distance + `,a.starts_at,a.id`
	}
	rows, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',a.id,'distance_m',`+distance+`) FROM social_activity a JOIN places_place p ON p.id=a.place_id WHERE `+social.ActivityVisibilitySQL()+` AND a.status='open' AND a.starts_at>=now() AND (NOT $3 OR a.beginners_welcome) AND ($4::double precision IS NULL OR $5::double precision IS NULL OR (p.location IS NOT NULL AND ($6::double precision IS NULL OR ST_DWithin(p.location,ST_SetSRID(ST_MakePoint($4,$5),4326)::geography,$6)))) AND NOT a.id=ANY($7) ORDER BY `+order+` LIMIT 20`, a.ID, a.Cohort, beginners, near.Lon, near.Lat, near.Radius, exclude)
	if err != nil {
		return nil, err
	}
	data["upcoming"], err = s.socialActivityModels(ctx, a, rows)
	if err != nil {
		return nil, err
	}
	rows, err = socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',a.id) FROM social_activity a WHERE `+social.ActivityVisibilitySQL()+` AND a.status='open' AND EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1 AND m.state='member' AND m.role<>'guardian') ORDER BY a.starts_at,a.id LIMIT 1000`, a.ID, a.Cohort)
	if err != nil {
		return nil, err
	}
	data["mine"], err = s.socialActivityModels(ctx, a, rows)
	if err != nil {
		return nil, err
	}
	events := spaRows(feed["events"])
	for _, row := range events {
		socialModel(row)
		row["feed_reason"] = row["reason"]
		row["place"] = map[string]any{"name": row["place_name"]}
	}
	data["events"] = events
	updates, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',p.id,'body',p.body,'created_at',p.created_at,'thread',jsonb_build_object('id',th.id,'group',jsonb_build_object('id',g.id,'title',g.title))) FROM social_post p JOIN social_thread th ON th.id=p.thread_id JOIN social_group g ON g.id=th.group_id WHERE g.cohort=$2 AND g.status='active' AND NOT g.is_hidden AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=g.owner_id) OR (b.blocker_id=g.owner_id AND b.blocked_id=$1)) AND EXISTS(SELECT 1 FROM social_groupmembership m WHERE m.group_id=g.id AND m.user_id=$1 AND m.state='member') AND p.is_announcement AND NOT p.is_hidden ORDER BY p.created_at DESC,p.id DESC LIMIT 5`, a.ID, a.Cohort)
	if err != nil {
		return nil, err
	}
	allowed := []map[string]any{}
	for _, row := range updates {
		read, e := s.Social.CanReadThread(ctx, s.DB, a, spaID(spaMap(row["thread"])))
		if e != nil {
			return nil, e
		}
		if read {
			allowed = append(allowed, row)
		}
	}
	data["group_updates"] = allowed
	data["guardian_invites"], err = socialRows(ctx, s.DB, `SELECT jsonb_build_object('token',inv.token,'relationship',inv.relationship,'guardian',jsonb_build_object('display_name',u.display_name,'username',u.username)) FROM accounts_guardianlinkinvite inv JOIN accounts_user u ON u.id=inv.guardian_id WHERE inv.ward_id=$1 AND inv.status='pending' AND inv.expires_at>now() ORDER BY inv.created_at,inv.id LIMIT 100`, a.ID)
	if err != nil {
		return nil, err
	}
	data["near_active"] = near.Lon != nil && near.Lat != nil
	data["beginners_only"] = beginners
	home := strings.TrimRight(s.Config.PublicURL, "/") + "/"
	site := map[string]any{"@context": "https://schema.org", "@graph": []any{map[string]any{"@type": []string{"Organization", "NGO"}, "name": "Activities", "url": home, "description": "A nonprofit, text-first platform that helps people meet in person for real group activities at real places — sport, outdoors, games, reading and more. First city: Cluj-Napoca, Romania."}, map[string]any{"@type": "WebSite", "name": "Activities", "url": home, "potentialAction": map[string]any{"@type": "SearchAction", "target": map[string]any{"@type": "EntryPoint", "urlTemplate": home + "events/?q={query}"}, "query-input": "required name=query"}}}}
	raw, err := json.Marshal(site)
	if err != nil {
		return nil, err
	}
	data["structured_data"] = pongo2.AsSafeValue(string(raw))
	return data, nil
}

const socialCommunityJSON = `jsonb_build_object('id',c.id,'slug',c.slug,'name',c.name,'tier',c.tier,'cohort',c.cohort,'area_id',c.area_id,'category_id',c.category_id,'activity_type_id',c.activity_type_id,'area',jsonb_build_object('id',ar.id,'name',ar.name,'city',ar.city),'category',jsonb_build_object('id',cat.id,'name',cat.name),'activity_type',CASE WHEN t.id IS NULL THEN NULL ELSE jsonb_build_object('id',t.id,'name',t.name,'slug',t.slug) END)`
const socialCommunityJoins = ` FROM communities_community c JOIN communities_area ar ON ar.id=c.area_id JOIN taxonomy_activitycategory cat ON cat.id=c.category_id LEFT JOIN taxonomy_activitytype t ON t.id=c.activity_type_id `
const socialGroupJSON = `jsonb_build_object('id',g.id,'title',g.title,'description',g.description,'tier',g.tier,'area',jsonb_build_object('id',ar.id,'name',ar.name),'category',jsonb_build_object('id',cat.id,'name',cat.name),'activity_type',CASE WHEN t.id IS NULL THEN NULL ELSE jsonb_build_object('id',t.id,'name',t.name,'slug',t.slug) END)`
const socialGroupJoins = ` FROM social_group g JOIN communities_area ar ON ar.id=g.area_id JOIN taxonomy_activitycategory cat ON cat.id=g.category_id LEFT JOIN taxonomy_activitytype t ON t.id=g.activity_type_id `
const socialGroupVisibility = `g.cohort=$2 AND NOT g.is_hidden AND g.status='active' AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=g.owner_id) OR (b.blocker_id=g.owner_id AND b.blocked_id=$1))`

func (s *Server) socialCommunities(r *http.Request, a platform.Actor, name string) (pongo2.Context, error) {
	ctx := r.Context()
	data := pongo2.Context{"can_create": a.IsStaff || a.Cohort == "adult" && s.Social.AllowUserGroups}
	cohort := a.Cohort
	if !a.IsActive || a.Cohort == "unassigned" {
		cohort = ""
	}
	var count int
	err := s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM communities_community WHERE cohort=$1 AND is_published`, cohort).Scan(&count)
	if err != nil {
		return nil, err
	}
	page, offset := socialPagination(r.URL.Query().Get("page"), count, 30)
	query := `SELECT ` + socialCommunityJSON + socialCommunityJoins + ` WHERE c.cohort=$1 AND c.is_published ORDER BY c.name,c.tier,c.id LIMIT 30 OFFSET $2`
	rows, err := socialRows(ctx, s.DB, query, cohort, offset)
	if err != nil {
		return nil, err
	}
	page["object_list"] = rows
	data["page"] = page
	if name == "community_graph" {
		data["communities"], err = socialRows(ctx, s.DB, `SELECT `+socialCommunityJSON+socialCommunityJoins+` WHERE c.cohort=$1 AND c.is_published ORDER BY c.name,c.tier,c.id LIMIT 1000`, cohort)
		return data, err
	}
	if name == "community_detail" {
		rows, err = socialRows(ctx, s.DB, `SELECT `+socialCommunityJSON+socialCommunityJoins+` WHERE c.slug=$1 AND c.cohort=$2 AND c.is_published`, r.PathValue("slug"), cohort)
		if err != nil {
			return nil, err
		}
		if len(rows) != 1 {
			return nil, platform.ErrNotFound
		}
		community := rows[0]
		data["community"] = community
		cards, err := s.get(r, "/api/communities/communities/"+url.PathEscape(r.PathValue("slug"))+"/activities/")
		if err != nil {
			return nil, err
		}
		data["activities"], err = s.socialActivityModels(ctx, a, results(cards))
		if err != nil {
			return nil, err
		}
		linked, err := socialRows(ctx, s.DB, `SELECT `+socialGroupJSON+socialGroupJoins+` WHERE `+socialGroupVisibility+` AND g.area_id=$3 AND g.tier=$4 AND (($4='type' AND g.activity_type_id=$5) OR ($4='category' AND g.category_id=$6)) ORDER BY g.title,g.id LIMIT 1`, a.ID, cohort, community["area_id"], community["tier"], community["activity_type_id"], community["category_id"])
		if err != nil {
			return nil, err
		}
		if len(linked) > 0 {
			data["linked_group"] = linked[0]
		}
		return data, nil
	}
	if err = s.DB.QueryRow(ctx, `SELECT COUNT(*) FROM social_group g WHERE `+socialGroupVisibility, a.ID, cohort).Scan(&count); err != nil {
		return nil, err
	}
	gpage, offset := socialPagination(r.URL.Query().Get("gpage"), count, 30)
	rows, err = socialRows(ctx, s.DB, `SELECT `+socialGroupJSON+socialGroupJoins+` WHERE `+socialGroupVisibility+` ORDER BY g.title,g.id LIMIT 30 OFFSET $3`, a.ID, cohort, offset)
	if err != nil {
		return nil, err
	}
	gpage["object_list"] = rows
	data["groups_page"] = gpage
	return data, nil
}

func (s *Server) socialNav(ctx context.Context, a platform.Actor, data pongo2.Context) error {
	var unread int
	var guardians bool
	err := s.DB.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM notifications_notification WHERE recipient_id=$1 AND read_at IS NULL),EXISTS(SELECT 1 FROM accounts_guardianrelationship WHERE ward_id=$1 AND status='active')`, a.ID).Scan(&unread, &guardians)
	if err != nil {
		return err
	}
	data["unread_notifications"] = unread
	data["has_guardians"] = guardians
	data["connections_enabled"] = s.Social.ConnectionCohorts[a.Cohort]
	return nil
}
