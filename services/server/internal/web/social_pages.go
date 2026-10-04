package web

// The legacy social pages keep the release templates, and translate native
// domain results into their small, explicit presentation models. Authorization
// happens before a private thread, person or upload is loaded.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

const socialBlockUser = `NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=u.id) OR (b.blocker_id=u.id AND b.blocked_id=$1))`
const socialUserJSON = `jsonb_build_object('id',u.id,'public_id',u.public_id::text,'username',u.username,'display_name',u.display_name)`

func socialRows(ctx context.Context, db platform.Querier, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		var row map[string]any
		if err = json.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		out = append(out, socialModel(row))
	}
	return out, rows.Err()
}

func socialModel(row map[string]any) map[string]any {
	if row == nil {
		return row
	}
	if row["id"] != nil {
		row["pk"] = row["id"]
	}
	for key, value := range row {
		if number, ok := value.(float64); ok && number == float64(int64(number)) {
			row[key] = int(number)
		}
		if child, ok := value.(map[string]any); ok {
			row[key] = socialModel(child)
		}
		if strings.HasSuffix(key, "_at") || key == "created" {
			if stamp, ok := spaDateValue(value); ok {
				row[key] = stamp
			}
		}
	}
	for key, labels := range map[string]map[string]string{
		"status":        {"open": "Open", "completed": "Completed", "cancelled": "Cancelled", "active": "Active", "paused": "Paused", "ended": "Ended", "archived": "Archived"},
		"cohort":        {"adult": "Adult", "teen": "Teen", "child": "Child"},
		"cadence":       {"weekly": "Weekly", "biweekly": "Every two weeks", "monthly": "Monthly"},
		"cost_band":     {"unspecified": "Not specified", "free": "Free", "low": "Low cost", "paid": "Paid"},
		"difficulty":    {"unspecified": "Not specified", "easy": "Easy", "moderate": "Moderate", "challenging": "Challenging"},
		"role":          {"owner": "Owner", "co_organizer": "Co-organiser", "member": "Member", "guardian": "Guardian"},
		"coarse_window": {"weekday_daytime": "Weekday daytime", "weekday_evening": "Weekday evening", "weekend_daytime": "Weekend daytime", "weekend_evening": "Weekend evening"},
	} {
		if label := labels[spaText(row[key])]; label != "" {
			row["get_"+key+"_display"] = label
		}
	}
	return row
}

func (s *Server) socialAvatars(ctx context.Context, people []map[string]any) error {
	ids := []int64{}
	seen := map[int64]bool{}
	for _, person := range people {
		uid := spaID(person)
		if uid > 0 && !seen[uid] {
			ids = append(ids, uid)
			seen[uid] = true
		}
	}
	cache, err := accounts.Avatars(ctx, s.DB, ids)
	if err != nil {
		return err
	}
	for _, person := range people {
		if spaID(person) > 0 {
			uri, ok := cache[spaID(person)]
			if !ok {
				return platform.ErrNotFound
			}
			person["avatar_uri"] = uri
		}
	}
	return nil
}

func socialActor(a platform.Actor) map[string]any {
	return map[string]any{"id": a.ID, "pk": a.ID, "public_id": a.PublicID, "username": a.Username, "display_name": a.DisplayName, "cohort": a.Cohort, "is_staff": a.IsStaff, "is_authenticated": a.ID > 0, "is_moderator": a.Moderator(), "is_admin": a.Admin()}
}

func (s *Server) socialActivityModels(ctx context.Context, a platform.Actor, value any) ([]map[string]any, error) {
	rows, err := s.HydrateActivities(ctx, a, value)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, row := range rows {
		ids = append(ids, spaID(row))
	}
	if len(ids) == 0 {
		return rows, nil
	}
	more, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',a.id,'owner_id',a.owner_id,'activity_type_id',a.activity_type_id,'place_id',a.place_id,'first_time_note',a.first_time_note,'cost_amount',a.cost_amount::text,'cost_note',a.cost_note,'series_id',a.series_id,'owner',`+socialUserJSON+`,'thread',jsonb_build_object('id',th.id)) FROM social_activity a JOIN accounts_user u ON u.id=a.owner_id LEFT JOIN social_thread th ON th.activity_id=a.id WHERE a.id=ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]map[string]any{}
	for _, row := range more {
		byID[spaID(row)] = row
	}
	for _, row := range rows {
		for key, value := range byID[spaID(row)] {
			row[key] = value
		}
		row["activity_type"] = row["activity_type_obj"]
		row["place"] = socialModel(spaMap(row["place_obj"]))
		row["secondary_types"] = map[string]any{"all": spaRows(row["secondary_types"])}
		if row["rec_reason"] == nil && row["reason"] != nil {
			row["rec_reason"] = row["reason"]
		}
		if metres, ok := row["distance_m"].(float64); ok {
			row["distance"] = map[string]any{"km": metres / 1000}
		}
		socialModel(row)
	}
	return rows, nil
}

func (s *Server) socialActivity(ctx context.Context, a platform.Actor, pk int64) (map[string]any, error) {
	raw, err := s.Social.Activity(ctx, a, pk)
	if err != nil {
		return nil, err
	}
	rows, err := s.socialActivityModels(ctx, a, []any{rawObject(raw)})
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 {
		return nil, platform.ErrNotFound
	}
	return rows[0], nil
}

// SocialView is dispatched before generic pages; unhandled pages remain owned
// by their native app adapter.
func (s *Server) SocialView(r *http.Request, a platform.Actor, name string) (pongo2.Context, string, bool, error) {
	if name == "home" && a.ID < 1 {
		return nil, "", false, nil
	}
	handled := false
	for _, n := range []string{"home", "activity_list", "communities", "community_detail", "community_graph", "activity_detail", "activity_create", "activity_edit", "group_detail", "group_create", "series_list", "series_detail", "series_create", "gauges", "gauge_detail", "gauge_create", "gauge_convert", "organize", "my_meetups", "my_venues"} {
		if name == n {
			handled = true
			break
		}
	}
	if !handled {
		return nil, "", false, nil
	}
	if s.Social == nil || s.DB == nil {
		return nil, "", true, errors.New("social page unavailable")
	}
	data := pongo2.Context{"user": socialActor(a)}
	var err error
	template := "web/" + name + ".html"
	switch name {
	case "home":
		data, err = s.socialHome(r, a)
	case "activity_list":
		data, err = s.socialBrowse(r, a)
		template = "web/activities.html"
	case "communities", "community_detail", "community_graph":
		data, err = s.socialCommunities(r, a, name)
		if name == "community_graph" {
			template = "web/communities_graph.html"
		}
	case "activity_detail":
		data, err = s.socialActivityDetail(r, a)
	case "group_detail":
		data, err = s.socialGroupDetail(r, a)
	case "activity_create", "activity_edit", "group_create", "series_create", "gauge_create", "gauge_convert":
		data, template, err = s.socialFormPage(r, a, name)
	case "series_list":
		data["series"], err = s.socialSeriesRows(r, a, 0)
	case "series_detail":
		var rows []map[string]any
		rows, err = s.socialSeriesRows(r, a, id(r, "pk"))
		if err == nil && len(rows) == 0 {
			err = platform.ErrNotFound
		}
		if err == nil {
			series := rows[0]
			data["series"] = series
			data["is_owner"] = spaInt(series["owner_id"]) == int(a.ID)
			data["next_note_form"], err = s.form(r, a, "NextInstanceNoteForm", map[string]any{"next_instance_note": series["next_instance_note"]})
			if err == nil {
				socialBoundFormRepair(r, data["next_note_form"].(map[string]any), map[string]any{"next_instance_note": series["next_instance_note"]}, false)
			}
			if err == nil {
				var instances []map[string]any
				instances, err = socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('id',a.id) FROM social_activity a WHERE a.series_id=$1 ORDER BY a.starts_at DESC,a.id DESC LIMIT 10`, id(r, "pk"))
				if err == nil {
					data["instances"], err = s.socialActivityModels(r.Context(), a, instances)
				}
			}
		}
	case "gauges", "gauge_detail":
		var rows []map[string]any
		pk := int64(0)
		if name == "gauge_detail" {
			pk = id(r, "pk")
		}
		rows, err = s.socialGaugeRows(r, a, pk)
		if name == "gauge_detail" {
			if err == nil && len(rows) == 0 {
				err = platform.ErrNotFound
			}
			if err == nil {
				row := rows[0]
				data["gauge"] = row
				for _, key := range []string{"ready", "remaining", "is_proposer", "viewer_interested"} {
					data[key] = row[key]
				}
			}
		} else {
			items := []map[string]any{}
			for _, row := range rows {
				socialModel(row)
				items = append(items, map[string]any{"gauge": row, "ready": row["ready"], "remaining": row["remaining"], "mine": row["is_proposer"]})
			}
			data["items"] = items
		}
	case "organize":
		var value map[string]any
		value, err = s.Social.OrganizerConsole(r.Context(), a)
		if err == nil {
			rows := spaRows(value["activities"])
			var models []map[string]any
			models, err = s.socialActivityModels(r.Context(), a, rows)
			byID := map[int64]map[string]any{}
			for _, m := range models {
				byID[spaID(m)] = m
			}
			out := []map[string]any{}
			for _, row := range rows {
				socialModel(row)
				if m, ok := byID[spaID(row)]; ok {
					row["activity"] = m
					out = append(out, row)
				}
			}
			data["activities"] = out
			series := spaRows(value["series"])
			for _, row := range series {
				socialModel(row)
			}
			data["series"] = series
			groups := spaRows(value["groups"])
			for _, row := range groups {
				socialModel(row)
			}
			data["groups"] = groups
		}
	case "my_meetups", "my_venues":
		var models []map[string]any
		models, err = s.socialUpcoming(r, a)
		if name == "my_meetups" {
			data["meetups"] = models
			data["generated_at"] = s.Social.Now()
			if err == nil {
				data["my_guardians"], err = s.socialGuardians(r.Context(), a.ID)
			}
		} else if err == nil {
			seen := map[int64]bool{}
			venues := []map[string]any{}
			for _, row := range models {
				place := spaMap(row["place"])
				pk := spaID(place)
				if seen[pk] {
					continue
				}
				seen[pk] = true
				var flags []string
				flags, err = s.socialVenueFlags(r.Context(), pk)
				if err != nil {
					break
				}
				if len(flags) > 0 {
					venues = append(venues, map[string]any{"place": place, "flags": flags})
				}
			}
			data["venues"] = venues
		}
	}
	if data != nil {
		data["user"] = socialActor(a)
		avatar, avatarErr := accounts.Avatar(r.Context(), s.DB, a.ID)
		if avatarErr != nil {
			return nil, "", true, avatarErr
		}
		data["avatar_uri"] = avatar
		data["user"].(map[string]any)["avatar_uri"] = avatar
		if navErr := s.socialNav(r.Context(), a, data); navErr != nil {
			return nil, "", true, navErr
		}
		s.socialTranslateModels(r, map[string]any(data))
	}
	return data, template, true, err
}

func (s *Server) socialTranslateModels(r *http.Request, value any) {
	tr := func(v string) string { return s.Renderer.catalog.translate(language(r), v, 1) }
	switch rows := value.(type) {
	case map[string]any:
		for key, v := range rows {
			if strings.HasPrefix(key, "get_") && strings.HasSuffix(key, "_display") {
				rows[key] = tr(spaText(v))
			}
			if key == "sentiment_lines" {
				if lines, ok := v.([]string); ok {
					for i, line := range lines {
						lines[i] = tr(line)
					}
				}
			}
			if key == "snippet" {
				snippet := spaMap(v)
				if snippet["text"] == "(message removed)" {
					snippet["text"] = tr("(message removed)")
				}
			}
			if key == "reaction_emojis" {
				for _, facet := range spaRows(v) {
					facet["label"] = tr(spaText(facet["label"]))
				}
			}
			s.socialTranslateModels(r, v)
		}
	case []map[string]any:
		for _, row := range rows {
			s.socialTranslateModels(r, row)
		}
	case []any:
		for _, row := range rows {
			s.socialTranslateModels(r, row)
		}
	}
}

func (s *Server) socialGuardians(ctx context.Context, ward int64) ([]map[string]any, error) {
	return socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',u.id,'display_name',u.display_name,'username',u.username) FROM accounts_guardianrelationship rel JOIN accounts_user u ON u.id=rel.guardian_id WHERE rel.ward_id=$1 AND rel.status='active' AND u.is_active ORDER BY rel.id`, ward)
}

func (s *Server) socialUpcoming(r *http.Request, a platform.Actor) ([]map[string]any, error) {
	if err := platform.Participate(r.Context(), s.DB, a); err != nil {
		if errors.Is(err, platform.ErrForbidden) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	rows, err := socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('id',a.id) FROM social_activity a WHERE a.cohort=$2 AND NOT a.is_hidden AND a.status='open' AND a.starts_at>=now() AND EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1 AND m.state='member' AND m.role<>'guardian') AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=a.owner_id) OR (b.blocker_id=a.owner_id AND b.blocked_id=$1)) ORDER BY a.starts_at,a.id LIMIT 1000`, a.ID, a.Cohort)
	if err != nil {
		return nil, err
	}
	return s.socialActivityModels(r.Context(), a, rows)
}

func (s *Server) socialSeriesRows(r *http.Request, a platform.Actor, pk int64) ([]map[string]any, error) {
	return socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('id',sr.id,'title',sr.title,'description',sr.description,'owner_id',sr.owner_id,'cadence',sr.cadence,'status',sr.status,'next_starts_at',sr.next_starts_at,'next_instance_note',sr.next_instance_note,'activity_type',jsonb_build_object('id',t.id,'name',t.name,'slug',t.slug),'place',jsonb_build_object('id',p.id,'name',p.name,'display_name',`+catalog.PlaceDisplayNameSQL()+`)) FROM social_activityseries sr JOIN taxonomy_activitytype t ON t.id=sr.activity_type_id JOIN places_place p ON p.id=sr.place_id WHERE (sr.owner_id=$1 OR $2) AND ($3::bigint=0 OR sr.id=$3) ORDER BY sr.status,sr.next_starts_at,sr.id LIMIT 200`, a.ID, a.IsStaff, pk)
}

func (s *Server) socialGaugeRows(r *http.Request, a platform.Actor, pk int64) ([]map[string]any, error) {
	if !a.IsActive || a.Cohort == "" || a.Cohort == "unassigned" {
		return []map[string]any{}, nil
	}
	return socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('id',g.id,'cohort',g.cohort,'coarse_window',g.coarse_window,'expires_at',g.expires_at,'is_proposer',g.proposer_id=$1,'viewer_interested',EXISTS(SELECT 1 FROM social_activityinterest_interested_users mine WHERE mine.activityinterest_id=g.id AND mine.user_id=$1),'ready',(SELECT COUNT(*) FROM social_activityinterest_interested_users x WHERE x.activityinterest_id=g.id)>=3,'remaining',GREATEST(0,3-(SELECT COUNT(*) FROM social_activityinterest_interested_users x WHERE x.activityinterest_id=g.id)),'activity_type',jsonb_build_object('id',t.id,'name',t.name,'slug',t.slug),'place',jsonb_build_object('id',p.id,'name',p.name,'display_name',`+catalog.PlaceDisplayNameSQL()+`,'address_city',p.address_city)) FROM social_activityinterest g JOIN taxonomy_activitytype t ON t.id=g.activity_type_id JOIN places_place p ON p.id=g.place_id WHERE g.cohort=$2 AND g.converted_activity_id IS NULL AND g.expires_at>now() AND ($3::bigint=0 OR g.id=$3) AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=g.proposer_id) OR (b.blocker_id=g.proposer_id AND b.blocked_id=$1)) ORDER BY g.expires_at,g.id LIMIT 200`, a.ID, a.Cohort, pk)
}

func (s *Server) socialVenueFlags(ctx context.Context, pk int64) ([]string, error) {
	var closed, pending bool
	var reports int
	var raw []byte
	var correction string
	err := s.DB.QueryRow(ctx, `SELECT (SELECT COUNT(*) FROM places_placeclosurereport report WHERE report.place_id=p.id AND report.created_at>=now()-interval '14 days')>=3,(SELECT COUNT(*) FROM places_opennowreport report WHERE report.place_id=p.id AND report.created_at>=now()-interval '14 days'),EXISTS(SELECT 1 FROM places_placecorrection c WHERE c.place_id=p.id AND c.status='pending'),p.opening_hours,coalesce((SELECT proposed_value FROM places_placecorrection c WHERE c.place_id=p.id AND c.field='hours' AND c.status='published' ORDER BY coalesce(c.published_at,c.created_at) DESC,c.id DESC LIMIT 1),'') FROM places_place p WHERE p.id=$1`, pk).Scan(&closed, &reports, &pending, &raw, &correction)
	if err != nil {
		return nil, err
	}
	flags := []string{}
	var schedule catalog.Schedule
	if correction != "" {
		schedule = catalog.ParseOpeningHours(correction)
	} else {
		_ = json.Unmarshal(raw, &schedule)
	}
	zone, _ := time.LoadLocation("Europe/Bucharest")
	unverified := reports >= 3 && catalog.OpenAt(schedule, s.Social.Now().In(zone)) != nil
	if closed {
		flags = append(flags, "closed")
	} else if unverified {
		flags = append(flags, "hours_unverified")
	}
	if pending {
		flags = append(flags, "correction_pending")
	}
	return flags, nil
}

var socialLogistical = regexp.MustCompile(`(?i)\b(meet|meeting|change|changed|move|moved|moving|bring|bringing|cancel|cancelled|canceled|cancelling|reschedul\w*|postpon\w*|location|venue)\b`)

func (s *Server) socialActivityDetail(r *http.Request, a platform.Actor) (pongo2.Context, error) {
	ctx := r.Context()
	activity, err := s.socialActivity(ctx, a, id(r, "pk"))
	if err != nil {
		return nil, err
	}
	pk := spaID(activity)
	tid := spaID(spaMap(activity["thread"]))
	data := pongo2.Context{"activity": activity, "user": socialActor(a)}
	my, err := socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',m.id,'role',m.role,'state',m.state,'attendance_intent',m.attendance_intent,'arrived_at',m.arrived_at,'transit_status',m.transit_status,'departing_at',m.departing_at,'met_confirmed_at',m.met_confirmed_at,'brings_support_person',m.brings_support_person,'welcomed_at',m.welcomed_at) FROM social_membership m WHERE m.activity_id=$1 AND m.user_id=$2`, pk, a.ID)
	if err != nil {
		return nil, err
	}
	var membership map[string]any
	if len(my) > 0 {
		membership = my[0]
	}
	data["my_membership"] = membership
	isMember := membership != nil && membership["state"] == "member"
	peer := isMember && membership["role"] != "guardian"
	owner := spaInt(activity["owner_id"]) == int(a.ID)
	organizer := owner || peer && membership["role"] == "co_organizer"
	read, err := s.Social.CanReadThread(ctx, s.DB, a, tid)
	if err != nil {
		return nil, err
	}
	write, err := s.Social.CanWriteThread(ctx, s.DB, a, tid)
	if err != nil {
		return nil, err
	}
	// A stale/guardian membership never makes a thread visible. Template member
	// state describes the permitted page capability, rather than raw DB presence.
	data["is_member"] = isMember && read
	data["is_owner"] = owner
	data["is_organizer"] = organizer
	data["can_manage_organizers"] = owner && a.Cohort == "adult"
	data["is_open"] = activity["status"] == "open"
	data["is_completed"] = activity["status"] == "completed"
	members := []map[string]any{}
	pending := []map[string]any{}
	if read {
		members, err = socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',m.id,'role',m.role,'state',m.state,'user',`+socialUserJSON+`) FROM social_membership m JOIN accounts_user u ON u.id=m.user_id WHERE m.activity_id=$2 AND m.state='member' AND `+socialBlockUser+` ORDER BY m.created_at,m.id LIMIT 1000`, a.ID, pk)
		if err != nil {
			return nil, err
		}
		if write {
			pending, err = socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',m.id,'user',`+socialUserJSON+`,'approvals',(SELECT COUNT(*) FROM social_joinvote vote WHERE vote.membership_id=m.id AND vote.approve),'needed',GREATEST(1,ceil(a.join_threshold*(SELECT COUNT(*) FROM social_membership vm WHERE vm.activity_id=a.id AND vm.state='member' AND vm.role<>'guardian'))),'my_vote',(SELECT vote.approve FROM social_joinvote vote WHERE vote.membership_id=m.id AND vote.voter_id=$1)) FROM social_membership m JOIN social_activity a ON a.id=m.activity_id JOIN accounts_user u ON u.id=m.user_id WHERE m.activity_id=$2 AND m.state='requested' AND u.cohort=$3 AND u.is_active AND `+socialBlockUser+` ORDER BY m.created_at,m.id LIMIT 1000`, a.ID, pk, a.Cohort)
			if err != nil {
				return nil, err
			}
		}
		people := []map[string]any{}
		for _, m := range append(members, pending...) {
			people = append(people, spaMap(m["user"]))
		}
		if err = s.socialAvatars(ctx, people); err != nil {
			return nil, err
		}
	}
	data["members"] = members
	data["pending"] = pending
	data["conn_enabled"] = peer && read && s.Social.ConnectionCohorts[a.Cohort]
	related := []int64{}
	if spaBool(data["conn_enabled"]) {
		rows, e := s.DB.Query(ctx, `SELECT CASE WHEN requester_id=$1 THEN addressee_id ELSE requester_id END FROM connections_connection WHERE status IN ('pending','accepted') AND (requester_id=$1 OR addressee_id=$1)`, a.ID)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var n int64
			if e = rows.Scan(&n); e != nil {
				rows.Close()
				return nil, e
			}
			related = append(related, n)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	data["conn_related_ids"] = related
	data["can_create_group"] = peer && (a.IsStaff || a.Cohort == "adult" && s.Social.AllowUserGroups)
	data["my_guardians"], err = s.socialGuardians(ctx, a.ID)
	if err != nil {
		return nil, err
	}
	data["supervised"] = activity["supervised"]
	supervisor, err := s.Social.SupervisorPresent(ctx, pk)
	if err != nil {
		return nil, err
	}
	data["supervisor_present"] = supervisor
	if owner && spaBool(activity["supervised"]) && !supervisor {
		data["owner_supervisor_candidates"], err = socialRows(ctx, s.DB, `SELECT jsonb_build_object('id',u.id,'username',u.username,'display_name',u.display_name) FROM accounts_guardianrelationship rel JOIN accounts_user u ON u.id=rel.guardian_id WHERE rel.ward_id=$1 AND rel.status='active' AND u.is_active AND u.is_identity_verified AND u.cohort='adult' AND NOT EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=$2 AND m.user_id=u.id AND m.role='guardian' AND m.state<>'removed') ORDER BY rel.id`, a.ID, pk)
		if err != nil {
			return nil, err
		}
	}
	data["child_safe_venue"] = false
	if a.Cohort == "child" {
		var safe bool
		safe, err = s.Social.ChildVenueSafe(ctx, int64(spaInt(activity["place_id"])))
		if err != nil {
			return nil, err
		}
		data["child_safe_venue"] = safe
	}
	data["meetup_brief"] = s.socialBrief(r, activity, isMember && read)
	data["show_welcome"] = false
	if t, ok := spaDateValue(membership["welcomed_at"]); ok {
		data["show_welcome"] = read && s.Social.Now().Sub(t) <= 7*24*time.Hour
	}
	data["my_arrival"] = membership["arrived_at"]
	data["my_transit"] = membership["transit_status"]
	data["my_departure"] = membership["departing_at"]
	data["my_brings_support"] = membership["brings_support_person"]
	data["my_met_confirmed"] = membership["met_confirmed_at"] != nil
	start, _ := spaDateValue(activity["starts_at"])
	end, ok := spaDateValue(activity["ends_at"])
	if !ok {
		end = start.Add(3 * time.Hour)
	}
	now := s.Social.Now()
	open := activity["status"] == "open"
	data["arrival_window_open"] = peer && read && open && !now.Before(start.Add(-2*time.Hour)) && !now.After(start.Add(3*time.Hour))
	data["can_mark_departing"] = peer && read && a.Cohort == "child" && membership["departing_at"] == nil && open && !now.Before(start) && !now.After(end.Add(3*time.Hour))
	data["can_set_support"] = peer && read && a.Cohort == "adult"
	transit := spaText(membership["transit_status"])
	data["can_say_on_my_way"] = peer && read && transit == "none"
	data["can_say_running_late"] = peer && read && (transit == "none" || transit == "on_my_way")
	attendance := map[string]any{}
	if read {
		raw, e := s.Social.Attendance(ctx, a, pk)
		if e != nil {
			return nil, e
		}
		attendance = socialModel(rawObject(raw))
	}
	data["rsvp_summary"] = attendance
	data["meeting_point_needed"] = organizer && spaBool(attendance["met_minimum"]) && strings.TrimSpace(spaText(activity["meeting_point"])) == ""
	var total, confirmed int
	if read {
		err = s.DB.QueryRow(ctx, `SELECT COUNT(*) FILTER(WHERE role<>'guardian'),COUNT(*) FILTER(WHERE role<>'guardian' AND met_confirmed_at IS NOT NULL) FROM social_membership WHERE activity_id=$1 AND state='member'`, pk).Scan(&total, &confirmed)
		if err != nil {
			return nil, err
		}
	}
	data["met_summary"] = map[string]any{"total": total, "confirmed": confirmed}
	data["can_join"], err = s.Social.CanJoin(ctx, a, pk)
	if err != nil {
		return nil, err
	}
	if read {
		if err = s.socialThreadContext(r, a, "activity", pk, tid, data); err != nil {
			return nil, err
		}
		photos, e := s.get(r, fmt.Sprintf("/api/media/threads/%d/photos/", tid))
		if e != nil {
			return nil, e
		}
		data["photos"] = results(photos)
		data["share_targets"], err = s.socialShareTargets(r, a, pk)
		if err != nil {
			return nil, err
		}
		data["thread_audience"] = map[string]any{"is_group": false, "peer_count": nil}
		if a.Cohort == "adult" {
			data["thread_audience"].(map[string]any)["peer_count"] = max(0, total-1)
		}
		query := strings.TrimSpace(r.URL.Query().Get("tq"))
		data["thread_query"] = query
		if query != "" {
			raw, e := s.Social.SearchThread(ctx, a, "activity", pk, query)
			if e != nil {
				return nil, e
			}
			data["thread_results"], err = s.socialPostModels(ctx, a, tid, spaRows(raw))
			if err != nil {
				return nil, err
			}
		}
	}
	data["share_kind"] = "activity"
	data["share_obj_id"] = pk
	return data, nil
}

func (s *Server) socialShareTargets(r *http.Request, a platform.Actor, exclude int64) ([]map[string]any, error) {
	rows, err := socialRows(r.Context(), s.DB, `SELECT jsonb_build_object('id',a.id) FROM social_activity a WHERE a.cohort=$2 AND NOT a.is_hidden AND a.status='open' AND a.id<>$3 AND EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=a.id AND m.user_id=$1 AND m.state='member' AND m.role<>'guardian') AND NOT EXISTS(SELECT 1 FROM safety_block b WHERE (b.blocker_id=$1 AND b.blocked_id=a.owner_id) OR (b.blocker_id=a.owner_id AND b.blocked_id=$1)) ORDER BY a.starts_at,a.id LIMIT 50`, a.ID, a.Cohort, exclude)
	if err != nil {
		return nil, err
	}
	return s.socialActivityModels(r.Context(), a, rows)
}

func (s *Server) socialBrief(r *http.Request, row map[string]any, member bool) []any {
	tr := func(v string) string { return s.Renderer.catalog.translate(language(r), v, 1) }
	fill := func(v, key, value string) string { return strings.ReplaceAll(tr(v), "%("+key+")s", value) }
	brief := []any{[]string{tr("What"), spaText(row["title"])}, []string{tr("Activity"), fill("It is %(type)s.", "type", spaText(spaMap(row["activity_type"])["name"]))}}
	place := spaText(spaMap(row["place"])["name"])
	if place != "" {
		brief = append(brief, []string{tr("Where"), fill("It is at %(place)s.", "place", place)})
	}
	brief = append(brief, []string{tr("When"), fill("It starts on %(when)s.", "when", s.spaDate(r, row["starts_at"], "l d F ")+tr("at")+" "+s.spaDate(r, row["starts_at"], "H:i"))})
	if row["cost_band"] != "unspecified" {
		brief = append(brief, []string{tr("Cost"), tr(spaText(row["get_cost_band_display"]))})
	}
	if row["difficulty"] != "unspecified" {
		brief = append(brief, []string{tr("Difficulty"), fill("It is %(level)s.", "level", tr(spaText(row["get_difficulty_display"])))})
	}
	if value := strings.TrimSpace(spaText(row["accessibility_notes"])); value != "" {
		brief = append(brief, []string{tr("Access"), value})
	}
	if member {
		for _, f := range [][2]string{{"Where to meet", "meeting_point"}, {"What to bring", "what_to_bring"}, {"A note from the organiser", "organizer_note"}, {"First time here", "first_time_note"}} {
			if value := strings.TrimSpace(spaText(row[f[1]])); value != "" {
				brief = append(brief, []string{tr(f[0]), value})
			}
		}
	}
	return brief
}

func socialICSEscape(v string) string {
	return strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\r\n", `\n`, "\n", `\n`, "\r", `\n`).Replace(v)
}
func socialICSFold(line string) string {
	out := []string{}
	limit := 75
	for len(line) > limit {
		n := limit
		for n > 0 && !utf8.RuneStart(line[n]) {
			n--
		}
		out = append(out, line[:n])
		line = line[n:]
		limit = 74
	}
	out = append(out, line)
	return strings.Join(out, "\r\n ")
}
func socialCalendar(rows []map[string]any, host string, now time.Time) string {
	// Host is release configuration, never an untrusted Host header.
	host = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, host)
	lines := []string{"BEGIN:VCALENDAR", "VERSION:2.0", "PRODID:-//" + host + "//meetups//EN", "CALSCALE:GREGORIAN", "METHOD:PUBLISH"}
	utc := func(t time.Time) string { return t.UTC().Format("20060102T150405Z") }
	for _, row := range rows {
		start, ok := spaDateValue(row["starts_at"])
		if !ok {
			continue
		}
		lines = append(lines, "BEGIN:VEVENT", fmt.Sprintf("UID:meetup-%d@%s", spaID(row), host), "DTSTAMP:"+utc(now), "DTSTART:"+utc(start))
		if end, ok := spaDateValue(row["ends_at"]); ok {
			lines = append(lines, "DTEND:"+utc(end))
		}
		lines = append(lines, "SUMMARY:"+socialICSEscape(spaText(row["title"])), "LOCATION:"+socialICSEscape(spaText(spaMap(row["place"])["display_name"])), "END:VEVENT")
	}
	lines = append(lines, "END:VCALENDAR")
	for i, line := range lines {
		lines[i] = socialICSFold(line)
	}
	return strings.Join(lines, "\r\n") + "\r\n"
}

// SocialDownload is a response hook for the self-only calendar download.
func (s *Server) SocialDownload(w http.ResponseWriter, r *http.Request, a platform.Actor, name string) bool {
	if name != "my_calendar" {
		return false
	}
	rows, err := s.socialUpcoming(r, a)
	if err != nil {
		platform.Fail(w, err)
		return true
	}
	host := "social.local"
	if u, err := http.NewRequest("GET", s.Config.PublicURL, nil); err == nil && u.URL.Host != "" {
		host = u.URL.Host
	}
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="my-meetups-`+a.PublicID+`.ics"`)
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write([]byte(socialCalendar(rows, host, s.Social.Now())))
	return true
}
