package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
)

func (s *Server) socialGroupDetail(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	ctx := r.Context()
	pk := id(r, "pk")
	raw, err := s.Social.Group(ctx, a, pk)
	if err != nil {
		return nil, err
	}
	g := socialModel(rawObject(raw))
	rows, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',g.id,'owner_id',g.owner_id,'owner_is_staff',u.is_staff,'area',jsonb_build_object('id',ar.id,'name',ar.name,'city',ar.city),'category',jsonb_build_object('id',cat.id,'name',cat.name),'activity_type',CASE WHEN t.id IS NULL THEN NULL ELSE jsonb_build_object('id',t.id,'name',t.name,'slug',t.slug) END,'thread',jsonb_build_object('id',th.id)) FROM social_group g JOIN accounts_user u ON u.id=g.owner_id JOIN communities_area ar ON ar.id=g.area_id JOIN taxonomy_activitycategory cat ON cat.id=g.category_id LEFT JOIN taxonomy_activitytype t ON t.id=g.activity_type_id LEFT JOIN social_thread th ON th.group_id=g.id WHERE g.id=$1`, pk)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, platform.ErrNotFound
	}
	for key, v := range rows[0] {
		g[key] = v
	}
	my, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',id,'role',role,'state',state) FROM social_groupmembership WHERE group_id=$1 AND user_id=$2`, pk, a.ID)
	if err != nil {
		return nil, err
	}
	member := len(my) > 0 && my[0]["state"] == "member"
	owner := spaInt(g["owner_id"]) == int(a.ID)
	tid := spaID(spaMap(g["thread"]))
	read, err := s.Social.CanReadThread(ctx, s.DB, a, tid)
	if err != nil {
		return nil, err
	}
	write, err := s.Social.CanWriteThread(ctx, s.DB, a, tid)
	if err != nil {
		return nil, err
	}
	data := pongo2.Context{"group": g, "user": socialActor(a), "is_member": member && read, "is_owner": owner, "show_thread": read, "can_post": write, "can_announce": owner, "roster": nil, "can_ask": member && !owner && g["cohort"] != "adult" && g["status"] == "active" && spaBool(g["is_staff_curated"]) && spaBool(g["owner_is_staff"]), "question_prompts": []any{[]string{"next_meetup", "When is the next meetup?"}, []string{"where", "Where exactly do we meet?"}, []string{"what_to_bring", "What should I bring?"}, []string{"how_it_works", "I'm new here — how does this group work?"}, []string{"more_info", "Could you post more about what's coming up?"}}}
	if spaBool(data["can_ask"]) {
		if err := platform.Participate(ctx, s.DB, a); err != nil {
			data["can_ask"] = false
		}
	}
	if member && read && a.Cohort == "adult" && g["cohort"] == "adult" {
		roster, e := socialRows(ctx, s.DB, `SELECT `+socialUserJSON+` FROM social_groupmembership m JOIN accounts_user u ON u.id=m.user_id WHERE m.group_id=$2 AND m.state='member' AND u.is_active AND u.cohort=$3 AND `+socialBlockUser+` ORDER BY m.joined_at,m.id LIMIT 1000`, a.ID, pk, a.Cohort)
		if e != nil {
			return nil, e
		}
		if e = s.socialAvatars(ctx, roster); e != nil {
			return nil, e
		}
		data["roster"] = roster
	}
	data["feed"] = []map[string]any{}
	if a.Cohort == g["cohort"] {
		feed, e := s.get(r, fmt.Sprintf("/api/social/groups/%d/activities/", pk))
		if e != nil {
			return nil, e
		}
		data["feed"], err = s.socialActivityModels(ctx, a, results(feed))
		if err != nil {
			return nil, err
		}
	}
	if read {
		if err = s.socialThreadContext(r, a, "group", pk, tid, data); err != nil {
			return nil, err
		}
	} else if owner && a.IsStaff {
		// A staff curator can read their own broadcasts, but never a minor's
		// peer messages by means of the staff bypass on the group metadata.
		announcements, e := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',p.id,'author_id',p.author_id,'author',`+socialUserJSON+`,'body',p.body,'created_at',p.created_at) FROM social_post p JOIN accounts_user u ON u.id=p.author_id WHERE p.thread_id=$1 AND p.author_id=$2 AND p.is_announcement AND NOT p.is_hidden ORDER BY p.created_at DESC,p.id DESC LIMIT 50`, tid, a.ID)
		if e != nil {
			return nil, e
		}
		data["announcements"] = announcements
		data["show_thread"] = true
		data["post_form"], err = s.form(r, a, "PostForm", nil)
	}
	return data, err
}

func socialFacets() []map[string]any {
	labels := map[string][2]string{"helped_me": {"🙏", "Helped me"}, "felt_welcome": {"🤝", "Made me feel welcome"}, "made_me_smile": {"🙂", "Made me smile"}, "want_to_come": {"✨", "Makes me want to come"}, "got_me_thinking": {"💡", "Got me thinking"}}
	out := []map[string]any{}
	for _, slug := range social.Facets {
		if pair, ok := labels[slug]; ok {
			out = append(out, map[string]any{"slug": slug, "emoji": pair[0], "label": pair[1]})
		}
	}
	return out
}

func (s *Server) socialPostModels(ctx context.Context, a platform.Actor, tid int64, items []map[string]any) ([]map[string]any, error) {
	if len(items) == 0 {
		return []map[string]any{}, nil
	}
	ids := []int64{}
	for _, p := range items {
		ids = append(ids, spaID(p))
	}
	if len(ids) > 1000 {
		return nil, platform.ErrInvalid
	}
	rows, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',p.id,'author_id',p.author_id,'author',`+socialUserJSON+`,'body',p.body,'created_at',p.created_at,'is_edited',p.updated_at>p.created_at,'is_announcement',p.is_announcement,'reply_to_id',p.reply_to_id,'shared_activity_id',p.shared_activity_id,'shared_place_id',p.shared_place_id,'shared_event_id',p.shared_event_id,'snippet',CASE WHEN parent.id IS NULL THEN NULL ELSE jsonb_build_object('pk',parent.id,'author',COALESCE(NULLIF(pu.display_name,''),pu.username),'text',CASE WHEN parent.is_hidden THEN '(message removed)' ELSE parent.body END) END,'reaction_mine',COALESCE((SELECT jsonb_agg(rx.emoji ORDER BY rx.emoji) FROM social_postreaction rx WHERE rx.post_id=p.id AND rx.user_id=$2),'[]'::jsonb),'dissent_mine',EXISTS(SELECT 1 FROM social_postdissent d WHERE d.post_id=p.id AND d.user_id=$2),'concern_mine',EXISTS(SELECT 1 FROM social_postconcern c WHERE c.post_id=p.id AND c.user_id=$2)) FROM social_post p JOIN accounts_user u ON u.id=p.author_id LEFT JOIN social_post parent ON parent.id=p.reply_to_id LEFT JOIN accounts_user pu ON pu.id=parent.author_id WHERE p.thread_id=$1 AND p.id=ANY($3) AND NOT p.is_hidden ORDER BY p.created_at,p.id`, tid, a.ID, ids)
	if err != nil {
		return nil, err
	}
	people := []map[string]any{}
	roster := map[string]bool{}
	var aid, gid int64
	err = s.DB.QueryRow(ctx, `SELECT COALESCE(activity_id,0),COALESCE(group_id,0) FROM social_thread WHERE id=$1`, tid).Scan(&aid, &gid)
	if err != nil {
		return nil, err
	}
	if aid > 0 {
		names, e := s.DB.Query(ctx, `SELECT u.username FROM social_membership m JOIN accounts_user u ON u.id=m.user_id WHERE m.activity_id=$2 AND m.state='member' AND m.role<>'guardian' AND u.cohort=$3 AND u.is_active AND `+socialBlockUser, a.ID, aid, a.Cohort)
		if e != nil {
			return nil, e
		}
		for names.Next() {
			var v string
			if e = names.Scan(&v); e != nil {
				names.Close()
				return nil, e
			}
			roster[strings.ToLower(v)] = true
		}
		e = names.Err()
		names.Close()
		if e != nil {
			return nil, e
		}
	}
	for _, p := range rows {
		if snippet, ok := p["snippet"].(map[string]any); ok {
			value := []rune(strings.ReplaceAll(strings.TrimSpace(spaText(snippet["text"])), "\n", " "))
			if len(value) > 120 {
				snippet["text"] = strings.TrimRightFunc(string(value[:119]), unicode.IsSpace) + "…"
			} else {
				snippet["text"] = string(value)
			}
		}
		people = append(people, spaMap(p["author"]))
		p["body_html"] = pongo2.AsSafeValue(BodyMarkup(spaText(p["body"]), roster, a.Cohort == "adult"))
		p["replies"] = map[string]any{"all": []map[string]any{}}
	}
	if err = s.socialAvatars(ctx, people); err != nil {
		return nil, err
	}
	kind, ownerID := "activity", aid
	if gid > 0 {
		kind, ownerID = "group", gid
	}
	footers, err := s.Social.SentimentFooters(ctx, a, kind, ownerID, ids)
	if err != nil {
		return nil, err
	}
	for _, p := range rows {
		p["sentiment_lines"] = footers[spaID(p)]
	}
	if s.Media != nil {
		atts := map[int64]any{}
		for begin := 0; begin < len(ids); begin += 200 {
			batch, e := s.Media.ForPosts(ctx, a, ids[begin:min(begin+200, len(ids))])
			if e != nil {
				return nil, e
			}
			for id, items := range batch {
				atts[id] = items
			}
		}
		for _, p := range rows {
			raw, e := json.Marshal(atts[spaID(p)])
			if e != nil {
				return nil, e
			}
			var list []map[string]any
			if e = json.Unmarshal(raw, &list); e != nil {
				return nil, e
			}
			for _, att := range list {
				socialModel(att)
			}
			p["attachment_list"] = list
		}
	} else {
		var has bool
		if err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM media_attachment WHERE post_id=ANY($1))`, ids).Scan(&has); err != nil {
			return nil, err
		}
		if has {
			return nil, fmt.Errorf("thread media unavailable")
		}
	}
	if err = s.socialShares(ctx, a, rows); err != nil {
		return nil, err
	}
	byID := map[int64]map[string]any{}
	for _, p := range rows {
		byID[spaID(p)] = p
	}
	out := []map[string]any{}
	for _, item := range items {
		if p, ok := byID[spaID(item)]; ok {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Server) socialShares(ctx context.Context, a platform.Actor, posts []map[string]any) error {
	activities, places, events := []int64{}, []int64{}, []int64{}
	for _, p := range posts {
		if n := spaInt(p["shared_activity_id"]); n > 0 {
			activities = append(activities, int64(n))
		}
		if n := spaInt(p["shared_place_id"]); n > 0 {
			places = append(places, int64(n))
		}
		if n := spaInt(p["shared_event_id"]); n > 0 {
			events = append(events, int64(n))
		}
	}
	byKind := map[string]map[int64]map[string]any{"activity": {}, "place": {}, "event": {}}
	if len(activities) > 0 {
		items := []map[string]any{}
		for _, n := range activities {
			items = append(items, map[string]any{"id": n})
		}
		rows, err := s.socialActivityModels(ctx, a, items)
		if err != nil {
			return err
		}
		for _, v := range rows {
			if v["status"] != "cancelled" {
				byKind["activity"][spaID(v)] = v
			}
		}
	}
	if len(places) > 0 {
		rows, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',p.id,'name',`+catalog.PlaceDisplayNameSQL()+`,'address_city',p.address_city) FROM places_place p WHERE p.id=ANY($1) AND `+catalog.PolicyFromContext(ctx).PlaceSQL(), places)
		if err != nil {
			return err
		}
		for _, v := range rows {
			byKind["place"][spaID(v)] = v
		}
	}
	if len(events) > 0 {
		rows, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',e.id,'title',e.title,'starts_at',e.starts_at,'place',CASE WHEN p.id IS NULL THEN NULL ELSE jsonb_build_object('id',p.id,'name',`+catalog.PlaceDisplayNameSQL()+`) END) FROM events_event e LEFT JOIN places_place p ON p.id=e.place_id WHERE e.id=ANY($1) AND `+catalog.PolicyFromContext(ctx).EventSQL(), events)
		if err != nil {
			return err
		}
		for _, v := range rows {
			byKind["event"][spaID(v)] = v
		}
	}
	for _, p := range posts {
		for _, kind := range []string{"activity", "place", "event"} {
			if n := spaInt(p["shared_"+kind+"_id"]); n > 0 {
				if obj, ok := byKind[kind][int64(n)]; ok {
					p["share"] = map[string]any{"kind": kind, "obj": obj}
				} else {
					p["share"] = map[string]any{"kind": "gone"}
				}
			}
		}
	}
	return nil
}

func (s *Server) socialThreadContext(r *http.Request, a platform.Actor, kind string, pk, tid int64, data pongo2.Context) error {
	ctx := r.Context()
	before, _ := strconv.ParseInt(r.URL.Query().Get("before"), 10, 64)
	roots, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',p.id,'created_at',p.created_at) FROM social_post p WHERE p.thread_id=$1 AND NOT p.is_hidden AND NOT p.is_announcement AND p.reply_to_id IS NULL AND ($2::bigint<=0 OR NOT EXISTS(SELECT 1 FROM social_post anchor WHERE anchor.id=$2 AND anchor.thread_id=$1) OR (p.created_at,p.id)<(SELECT anchor.created_at,anchor.id FROM social_post anchor WHERE anchor.id=$2 AND anchor.thread_id=$1)) ORDER BY p.created_at DESC,p.id DESC LIMIT 101`, tid, before)
	if err != nil {
		return err
	}
	data["has_older"] = len(roots) > 100
	if len(roots) > 100 {
		roots = roots[:100]
		data["older_cursor"] = spaID(roots[len(roots)-1])
	}
	for i, j := 0, len(roots)-1; i < j; i, j = i+1, j-1 {
		roots[i], roots[j] = roots[j], roots[i]
	}
	ids := []int64{}
	for _, p := range roots {
		ids = append(ids, spaID(p))
	}
	replies := []map[string]any{}
	if len(ids) > 0 {
		replies, err = socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',id,'reply_to_id',reply_to_id) FROM social_post WHERE thread_id=$1 AND reply_to_id=ANY($2) AND NOT is_hidden ORDER BY created_at,id LIMIT 901`, tid, ids)
		if err != nil {
			return err
		}
		if len(replies) > 900 {
			return platform.ErrInvalid
		}
	}
	announcements, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',id) FROM social_post WHERE thread_id=$1 AND is_announcement AND NOT is_hidden ORDER BY created_at DESC,id DESC LIMIT 50`, tid)
	if err != nil {
		return err
	}
	all := append(append(append([]map[string]any{}, roots...), replies...), announcements...)
	models, err := s.socialPostModels(ctx, a, tid, all)
	if err != nil {
		return err
	}
	byID := map[int64]map[string]any{}
	for _, p := range models {
		byID[spaID(p)] = p
	}
	posts := []map[string]any{}
	for _, p := range roots {
		if m, ok := byID[spaID(p)]; ok {
			children := []map[string]any{}
			for _, rp := range replies {
				if spaInt(rp["reply_to_id"]) == int(spaID(p)) {
					if child, ok := byID[spaID(rp)]; ok {
						children = append(children, child)
					}
				}
			}
			m["replies"] = map[string]any{"all": children}
			posts = append(posts, m)
		}
	}
	pinned := []map[string]any{}
	for _, p := range announcements {
		if m, ok := byID[spaID(p)]; ok {
			pinned = append(pinned, m)
		}
	}
	data["posts"] = posts
	data["announcements"] = pinned
	write, err := s.Social.CanWriteThread(ctx, s.DB, a, tid)
	if err != nil {
		return err
	}
	var target map[string]any
	if rt, e := strconv.ParseInt(r.URL.Query().Get("reply_to"), 10, 64); e == nil && rt > 0 && write {
		slots, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',id) FROM social_post WHERE id=$1 AND thread_id=$2 AND NOT is_hidden AND NOT is_announcement`, rt, tid)
		if err != nil {
			return err
		}
		if len(slots) > 0 {
			items, err := s.socialPostModels(ctx, a, tid, slots)
			if err != nil {
				return err
			}
			if len(items) > 0 {
				target = items[0]
			}
		}
	}
	data["reply_target"] = target
	initial := map[string]any{}
	if target != nil {
		initial["reply_to"] = spaID(target)
	}
	data["post_form"], err = s.form(r, a, "PostForm", initial)
	if err != nil {
		return err
	}
	socialBoundFormRepair(r, data["post_form"].(map[string]any), initial, false)
	data["reaction_emojis"] = socialFacets()
	data["show_dissent_concern"] = write && a.Cohort != "child"
	data["post_owner_pk"] = pk
	for _, suffix := range []string{"react", "dissent", "concern", "edit", "delete"} {
		data["post_"+suffix+"_url_name"] = kind + "_post_" + suffix
	}
	data["ephemeral_options"] = []any{[]string{"86400", "1 day"}, []string{"604800", "1 week"}}
	if a.Cohort == "adult" {
		data["ephemeral_options"] = []any{[]string{"3600", "1 hour"}, []string{"86400", "1 day"}, []string{"604800", "1 week"}}
	}
	data["video_enabled"] = a.Cohort == "adult" && s.Media != nil && s.Media.VideoEnabled()
	data["video_max_seconds"] = 90
	if s.Media != nil && s.Media.VideoMaxSeconds() > 0 {
		data["video_max_seconds"] = int(s.Media.VideoMaxSeconds())
	}
	i18n := map[string]string{}
	for _, v := range [][2]string{{"reply", "Reply"}, {"react", "react"}, {"edited", "(edited)"}, {"replyingTo", "Replying to"}, {"messageSent", "Message sent."}, {"newAnnouncement", "New announcement posted."}, {"newMessages", "New messages"}, {"livePaused", "Live updates paused — reload to catch up."}, {"typingOne", "%(name)s is typing…"}, {"typingTwo", "%(a)s and %(b)s are typing…"}, {"typingMany", "Several people are typing…"}, {"justNow", "just now"}, {"sharedImage", "shared image"}, {"videoProcessing", "Video is being prepared — it will appear here shortly."}, {"videoFailed", "This video couldn't be processed."}, {"attachmentExpired", "This temporary picture has disappeared."}, {"attachmentDisappears", "disappears in %(when)s"}, {"attachmentBlocked", "Blocked by safety screening."}, {"pdfDownloads", "(PDF — downloads)"}} {
		i18n[v[0]] = s.Renderer.catalog.translate(language(r), v[1], 1)
	}
	data["thread_chat_config"] = map[string]any{"threadId": tid, "meId": a.ID, "reactUrlTemplate": routeURL(kind+"_post_react", pk, int64(987654321)), "reactionFacets": socialFacets(), "i18n": i18n}
	where := "meetup"
	if kind == "group" {
		where = "group"
	}
	data["presend_nudge"] = map[string]any{"rules": socialPresendRules, "message": s.Renderer.catalog.translate(language(r), "This looks like it might share contact details or a plan to meet one-to-one. To keep everyone safe — especially younger members — try to keep coordination inside the "+where+". Post it anyway?", 1)}
	if kind == "activity" {
		scanned, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',id) FROM social_post WHERE thread_id=$1 AND NOT is_hidden AND NOT is_announcement ORDER BY created_at DESC,id DESC LIMIT 60`, tid)
		if err != nil {
			return err
		}
		recentModels, err := s.socialPostModels(ctx, a, tid, scanned)
		if err != nil {
			return err
		}
		recent := recentModels[:min(3, len(recentModels))]
		logistical := []map[string]any{}
		for _, p := range recentModels[min(3, len(recentModels)):] {
			if socialLogistical.MatchString(spaText(p["body"])) && len(logistical) < 3 {
				logistical = append(logistical, p)
			}
		}
		digest := map[string]any{"announcements": pinned[:min(2, len(pinned))], "recent": recent, "logistical": logistical, "member_count": nil, "going": nil, "total": nil, "has_content": len(pinned)+len(recentModels) > 0}
		if a.Cohort == "adult" {
			att := spaMap(data["rsvp_summary"])
			digest["going"] = att["going"]
			digest["total"] = att["total"]
			digest["member_count"] = len(spaRows(data["members"]))
		}
		data["digest"] = digest
	}
	return nil
}

// Frozen release copies of the shared browser nudge grammar. These patterns
// are advisory only; every accepted write still uses Social.MessagePolicy.
var socialPresendRules = []map[string]string{
	{"key": "phone", "pattern": `(?:\+|00)[0-9 .\-]{7,}[0-9]|\b0[0-9]{2}[0-9 .\-]{5,}[0-9]\b`, "flags": ""},
	{"key": "email", "pattern": `[^\s@]{1,64}@[^\s@]{1,255}\.[^\s@]{2,}`, "flags": ""},
	{"key": "address", "pattern": `\b(?:(?:strada|str|calea|bulevardul|bulevard|bdul|aleea|soseaua|piata)\b\.?\s+\S+|[0-9]{1,4}\s+(?:[A-Za-z]+\s+){0,2}(?:street|avenue|boulevard|blvd)(?=[.,]|\s*$|\s+[0-9]))`, "flags": "i"},
	{"key": "meet_alone", "pattern": `\b(?:(?:come|meet)\s+(?:me\s+|up\s+)?alone|just\s+(?:the\s+)?two\s+of\s+us|to\s+my\s+(?:place|house|apartment|flat|home)|at\s+my\s+(?:place|house|apartment|flat)|vino\s+singur|ne\s+vedem\s+singur|doar\s+noi\s+doi|(?:vino|hai)\s+la\s+mine|acas[ăa]\s+la\s+mine)`, "flags": "i"},
}
