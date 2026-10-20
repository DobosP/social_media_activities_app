package web

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

type workflowActivity struct {
	ID                                                                                                                                         int64
	Title, Description, Cohort, Status, GetStatusDisplay, CostBand, CostAmount, CostNote, GetCostBandDisplay, Difficulty, GetDifficultyDisplay string
	AccessibilityNotes, FirstTimeNote, MeetingPoint, WhatToBring, OrganizerNote                                                                string
	HasCostAmount, BeginnersWelcome, GuardianAccompanied, IsPubliclyListed                                                                     bool
	StartsAt, EndsAt                                                                                                                           *time.Time
	ActivityType                                                                                                                               workflowType
	SecondaryTypes                                                                                                                             []workflowType
	Place                                                                                                                                      workflowPlace
	Owner                                                                                                                                      workflowUser
	ThreadID                                                                                                                                   int64
}
type workflowType struct {
	ID         int64
	Name, Slug string
}
type workflowPlace struct {
	ID                                             int64
	Name, DisplayName, DisplayAddress, AddressCity string
}
type workflowUser struct {
	ID                              int64
	Username, DisplayName, PublicID string
	Avatar                          template.URL
	IsStaff                         bool
	Cohort                          string
}
type workflowMember struct {
	ID                            int64
	Role, State, AttendanceIntent string
	User                          workflowUser
	Approvals, Needed             int64
	MyVote                        *bool
	CanConnect                    bool
}
type workflowSummary struct {
	Going, Total, MinToGo, RemainingNeeded, Confirmed int64
	MetMinimum                                        bool
}
type workflowPair struct{ Label, Sentence string }
type workflowDigest struct {
	HasContent                        bool
	MemberCount                       *int64
	Going, Total                      int64
	Announcements, Logistical, Recent []workflowThreadPost
}
type workflowThreadPost struct {
	groupDetailPost
	CreatedAt *time.Time
	DayBreak  string
}
type workflowPhoto struct{ URL, ThumbURL string }
type workflowShareTarget struct {
	ID       int64
	Title    string
	StartsAt *time.Time
}

func workflowTime(d *groupDetailDecoder, value any) *time.Time {
	if value == nil {
		return nil
	}
	stamp, ok := spaDateValue(value)
	if !ok {
		d.fail()
		return nil
	}
	return &stamp
}
func workflowPerson(d *groupDetailDecoder, value any, viewer int64, avatar bool) workflowUser {
	row := d.row(value)
	p := workflowUser{ID: d.id(row["id"]), Username: d.optionalText(row["username"]), DisplayName: d.optionalText(row["display_name"]), PublicID: d.optionalText(row["public_id"])}
	if avatar {
		p.Avatar = d.person(row, viewer, true).Avatar
	}
	return p
}
func workflowPlaceProjection(d *groupDetailDecoder, value any) workflowPlace {
	row := d.row(value)
	return workflowPlace{ID: d.id(row["id"]), Name: d.optionalText(row["name"]), DisplayName: d.optionalText(row["display_name"]), DisplayAddress: d.optionalText(row["display_address"]), AddressCity: d.optionalText(row["address_city"])}
}
func workflowActivityProjection(d *groupDetailDecoder, value any, viewer int64) workflowActivity {
	row := d.row(value)
	a := workflowActivity{ID: d.id(row["id"]), Title: d.text(row["title"]), Description: d.optionalText(row["description"]), Cohort: d.text(row["cohort"]), Status: d.text(row["status"]), GetStatusDisplay: d.optionalText(row["get_status_display"]), CostBand: d.optionalText(row["cost_band"]), CostNote: d.optionalText(row["cost_note"]), GetCostBandDisplay: d.optionalText(row["get_cost_band_display"]), Difficulty: d.optionalText(row["difficulty"]), GetDifficultyDisplay: d.optionalText(row["get_difficulty_display"]), AccessibilityNotes: d.optionalText(row["accessibility_notes"]), FirstTimeNote: d.optionalText(row["first_time_note"]), MeetingPoint: d.optionalText(row["meeting_point"]), WhatToBring: d.optionalText(row["what_to_bring"]), OrganizerNote: d.optionalText(row["organizer_note"]), BeginnersWelcome: d.optionalBool(row["beginners_welcome"]), GuardianAccompanied: d.optionalBool(row["guardian_accompanied"]), IsPubliclyListed: d.optionalBool(row["is_publicly_listed"]), StartsAt: workflowTime(d, row["starts_at"]), EndsAt: workflowTime(d, row["ends_at"])}
	if row["cost_amount"] != nil {
		a.HasCostAmount = true
		// Match the installed Pongo value's decimal display (including zero).
		switch v := row["cost_amount"].(type) {
		case string:
			a.CostAmount = d.text(v)
		case int, int64, float64, json.Number:
			a.CostAmount = pongo2.AsValue(v).String()
		default:
			d.fail()
		}
	}
	t := d.row(row["activity_type"])
	a.ActivityType = workflowType{ID: d.id(t["id"]), Name: d.text(t["name"]), Slug: d.optionalText(t["slug"])}
	if row["secondary_types"] != nil {
		for _, t := range d.optionalRows(d.row(row["secondary_types"])["all"]) {
			a.SecondaryTypes = append(a.SecondaryTypes, workflowType{ID: d.id(t["id"]), Name: d.text(t["name"]), Slug: d.optionalText(t["slug"])})
		}
	}
	a.Place = workflowPlaceProjection(d, row["place"])
	a.Owner = workflowPerson(d, row["owner"], viewer, false)
	if row["thread"] != nil {
		a.ThreadID = d.optionalID(d.row(row["thread"])["id"])
	}
	return a
}

func (r *Renderer) workflowActivityPage(request *http.Request, data pongo2.Context, p *workflowPage, d *groupDetailDecoder, actor platform.Actor) {
	a := d.row(data["activity"])
	if p.Kind == "safe-exit" {
		// Deliberately copy only the minimal authorized fallback. Never hydrate the
		// place, organizer, roster, logistics, thread or media from this page.
		p.Activity = workflowActivity{ID: d.id(a["id"]), Title: d.text(a["title"])}
		p.IsMember = d.boolean(data["is_member"])
		p.IsOwner = d.boolean(data["is_owner"])
		if !d.boolean(data["safe_exit_only"]) || !p.IsMember || p.IsOwner {
			d.fail()
		}
		p.Title = p.Activity.Title
		for _, g := range d.optionalRows(data["my_guardians"]) {
			p.MyGuardians = append(p.MyGuardians, workflowPerson(d, g, actor.ID, false))
		}
		return
	}
	p.Activity = workflowActivityProjection(d, a, actor.ID)
	if p.Kind == "activity-edit" {
		p.Title = p.T("Edit activity")
		p.Form = r.workflowForm(data["form"], "ActivityEditForm", actor, d, p.T)
		return
	}
	p.Title = p.Activity.Title
	for key, target := range map[string]*bool{"is_member": &p.IsMember, "is_owner": &p.IsOwner, "is_organizer": &p.IsOrganizer, "can_manage_organizers": &p.CanManageOrganizers, "is_open": &p.IsOpen, "is_completed": &p.IsCompleted, "conn_enabled": &p.ConnEnabled, "can_create_group": &p.CanCreateGroup, "supervised": &p.Supervised, "supervisor_present": &p.SupervisorPresent, "child_safe_venue": &p.ChildSafeVenue, "show_welcome": &p.ShowWelcome, "my_brings_support": &p.MyBringsSupport, "my_met_confirmed": &p.MyMetConfirmed, "arrival_window_open": &p.ArrivalWindowOpen, "can_mark_departing": &p.CanMarkDeparting, "can_set_support": &p.CanSetSupport, "can_say_on_my_way": &p.CanSayOnMyWay, "can_say_running_late": &p.CanSayRunningLate, "meeting_point_needed": &p.MeetingPointNeeded, "can_join": &p.CanJoin} {
		*target = d.optionalBool(data[key])
	}
	p.MyArrival = workflowTime(d, data["my_arrival"])
	p.MyDeparture = workflowTime(d, data["my_departure"])
	p.MyTransit = d.optionalText(data["my_transit"])
	if value := data["my_membership"]; value != nil {
		if my := d.row(value); my != nil {
			p.MyMembership = &workflowMember{ID: d.id(my["id"]), Role: d.text(my["role"]), State: d.text(my["state"]), AttendanceIntent: d.text(my["attendance_intent"])}
		}
	}
	related := map[int64]bool{}
	switch list := data["conn_related_ids"].(type) {
	case nil:
	case []int64:
		for _, id := range list {
			related[id] = true
		}
	default:
		d.fail()
	}
	p.ConnRelatedIDs = related
	members := func(value any, pending bool) []workflowMember {
		var result []workflowMember
		for _, row := range d.optionalRows(value) {
			m := workflowMember{ID: d.id(row["id"]), User: workflowPerson(d, row["user"], actor.ID, true), Role: d.optionalText(row["role"]), State: d.optionalText(row["state"])}
			if pending {
				m.Approvals = d.integer(row["approvals"])
				m.Needed = d.integer(row["needed"])
				if row["my_vote"] != nil {
					v := d.boolean(row["my_vote"])
					m.MyVote = &v
				}
			}
			m.CanConnect = p.ConnEnabled && m.Role != "guardian" && m.User.ID != actor.ID && !related[m.User.ID]
			result = append(result, m)
		}
		return result
	}
	p.Members = members(data["members"], false)
	p.Pending = members(data["pending"], true)
	for _, g := range d.optionalRows(data["my_guardians"]) {
		p.MyGuardians = append(p.MyGuardians, workflowPerson(d, g, actor.ID, false))
	}
	for _, g := range d.optionalRows(data["owner_supervisor_candidates"]) {
		p.OwnerSupervisorCandidates = append(p.OwnerSupervisorCandidates, workflowPerson(d, g, actor.ID, false))
	}
	if data["meetup_brief"] != nil {
		pairs, ok := data["meetup_brief"].([]any)
		if !ok {
			d.fail()
		} else {
			for _, v := range pairs {
				pair, ok := v.([]string)
				if !ok || len(pair) != 2 {
					d.fail()
					continue
				}
				p.MeetupBrief = append(p.MeetupBrief, workflowPair{d.text(pair[0]), d.text(pair[1])})
			}
		}
	}
	for _, v := range d.optionalRows(data["activity_communities"]) {
		p.ActivityCommunities = append(p.ActivityCommunities, workflowCommunity{Name: d.text(v["name"]), Slug: d.text(v["slug"])})
	}
	summary := func(value any) workflowSummary {
		row := d.row(value)
		out := workflowSummary{}
		for key, target := range map[string]*int64{"going": &out.Going, "total": &out.Total, "min_to_go": &out.MinToGo, "remaining_needed": &out.RemainingNeeded, "confirmed": &out.Confirmed} {
			if row[key] != nil {
				*target = d.integer(row[key])
			}
		}
		out.MetMinimum = d.optionalBool(row["met_minimum"])
		return out
	}
	p.RSVPSummary = summary(data["rsvp_summary"])
	p.MetSummary = summary(data["met_summary"])
	for _, row := range d.optionalRows(data["share_targets"]) {
		p.ShareTargets = append(p.ShareTargets, workflowShareTarget{ID: d.id(row["pk"]), Title: d.text(row["title"]), StartsAt: workflowTime(d, row["starts_at"])})
	}
	p.ShareKind = d.optionalText(data["share_kind"])
	p.ShareObjID = d.optionalID(data["share_obj_id"])
	if len(p.ShareTargets) > 0 && (p.ShareKind != "activity" || p.ShareObjID != p.Activity.ID) {
		d.fail()
	}
	if !p.IsMember && !actor.IsStaff {
		return
	}
	p.ThreadQuery = d.optionalText(data["thread_query"])
	p.HasOlder = d.optionalBool(data["has_older"])
	if p.HasOlder {
		p.OlderCursor = d.id(data["older_cursor"])
	}
	threadPage := groupDetailPage{ID: p.Activity.ID, Shell: p.Shell, Copy: p.Copy, Member: p.IsMember, DissentControls: d.optionalBool(data["show_dissent_concern"])}
	for _, row := range d.optionalRows(data["reaction_emojis"]) {
		f := groupDetailFacet{Slug: d.text(row["slug"]), Emoji: d.text(row["emoji"]), Label: d.text(row["label"])}
		f.Aria = strings.ReplaceAll(p.T("react: %(label)s"), "%(label)s", f.Label)
		threadPage.ReactionFacets = append(threadPage.ReactionFacets, f)
	}
	posts := func(value any, replies, announcement bool, limit int) []workflowThreadPost {
		var result []workflowThreadPost
		rows := d.optionalRows(value)
		if len(rows) > limit {
			d.fail()
		}
		day := ""
		for _, row := range rows {
			post := workflowThreadPost{groupDetailPost: r.workflowPost(d, row, actor, threadPage, replies, announcement, p.T), CreatedAt: workflowTime(d, row["created_at"])}
			next := d.stamp(row["created_at"], "Y-m-d")
			if next != day {
				post.DayBreak = d.stamp(row["created_at"], "D j M")
				day = next
			}
			result = append(result, post)
		}
		return result
	}
	p.Announcements = posts(data["announcements"], false, true, 50)
	p.Posts = posts(data["posts"], true, false, 1000)
	p.ThreadResults = posts(data["thread_results"], false, true, 1000)
	if data["digest"] != nil {
		row := d.row(data["digest"])
		g := &workflowDigest{HasContent: d.boolean(row["has_content"]), Announcements: posts(row["announcements"], false, true, 2), Logistical: posts(row["logistical"], false, true, 3), Recent: posts(row["recent"], false, true, 3)}
		if row["member_count"] != nil {
			v := d.integer(row["member_count"])
			g.MemberCount = &v
			g.Going = d.integer(row["going"])
			g.Total = d.integer(row["total"])
		}
		p.Digest = g
	}
	for _, row := range d.optionalRows(data["photos"]) {
		image := workflowPhoto{URL: d.localMedia(row["url"]), ThumbURL: d.localMedia(row["thumb_url"])}
		if image.ThumbURL == "" {
			image.ThumbURL = image.URL
		}
		p.Photos = append(p.Photos, image)
	}
	if !p.IsMember {
		return
	}
	p.PostForm = r.workflowForm(data["post_form"], "PostForm", actor, d, p.T)
	if value := data["reply_target"]; value != nil {
		row := d.row(value)
		if row != nil {
			author := d.row(row["author"])
			p.ReplyAuthor = d.optionalText(author["display_name"])
			if p.ReplyAuthor == "" {
				p.ReplyAuthor = d.text(author["username"])
			}
			p.HasReplyTarget = true
		}
	}
	// The reference producer omits this marker. Its existing template branch
	// supplies the historical media controls only when that marker is absent.
	p.MediaCapabilitiesSupplied = d.optionalBool(data["media_capabilities_supplied"])
	p.AttachmentsEnabled = d.boolean(data["attachments_enabled"])
	p.FileEnabled = d.boolean(data["file_enabled"])
	p.VideoEnabled = d.boolean(data["video_enabled"])
	p.AttachmentAccept = d.text(data["attachment_accept"])
	p.VideoMaxSeconds = d.integer(data["video_max_seconds"])
	if values, ok := data["ephemeral_options"].([]any); ok {
		for _, value := range values {
			pair, ok := value.([]string)
			if !ok || len(pair) != 2 {
				d.fail()
				continue
			}
			p.EphemeralOptions = append(p.EphemeralOptions, familyOption{Value: d.text(pair[0]), Label: d.text(pair[1])})
		}
	} else {
		d.fail()
	}
	audience := d.row(data["thread_audience"])
	p.HasThreadAudience = audience != nil
	p.AudienceIsGroup = d.optionalBool(audience["is_group"])
	if audience["peer_count"] != nil {
		v := d.integer(audience["peer_count"])
		p.AudiencePeers = &v
	}
	// Adapt only the activity react URL for the established strict five-key schema.
	config := d.row(data["thread_chat_config"])
	copyConfig := map[string]any{}
	for key, value := range config {
		copyConfig[key] = value
	}
	if d.text(config["reactUrlTemplate"]) != routeURL("activity_post_react", p.Activity.ID, int64(987654321)) {
		d.fail()
	}
	copyConfig["reactUrlTemplate"] = routeURL("group_post_react", p.Activity.ID, int64(987654321))
	_ = groupDetailChatJSON(d, copyConfig, p.Activity.ID, p.Activity.ThreadID, actor.ID)
	raw, err := json.Marshal(config)
	if err != nil {
		d.fail()
	} else {
		p.ChatJSON = template.JS(raw)
	}
	p.NudgeJSON = groupDetailNudgeJSON(d, data["presend_nudge"])
}
