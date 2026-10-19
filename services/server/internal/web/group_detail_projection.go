package web

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

type groupDetailDecoder struct{ familyDecoder }

func (d *groupDetailDecoder) optionalText(value any) string {
	if value == nil {
		return ""
	}
	return d.text(value)
}
func (d *groupDetailDecoder) optionalBool(value any) bool {
	if value == nil {
		return false
	}
	return d.boolean(value)
}
func (d *groupDetailDecoder) optionalRows(value any) []map[string]any {
	if value == nil {
		return nil
	}
	return d.rows(value)
}
func (d *groupDetailDecoder) id(value any) int64 {
	n := d.integer(value)
	if n < 1 {
		d.fail()
	}
	return n
}
func (d *groupDetailDecoder) optionalID(value any) int64 {
	if value == nil {
		return 0
	}
	return d.id(value)
}
func (d *groupDetailDecoder) stamp(value any, format string) string {
	if value == nil {
		return ""
	}
	stamp, ok := spaDateValue(value)
	if !ok {
		d.fail()
		return ""
	}
	return djangoDate(stamp, format)
}
func (d *groupDetailDecoder) localMedia(value any) string {
	raw := d.optionalText(value)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/api/media/") || !strings.HasSuffix(u.Path, "/") || path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") || strings.ContainsAny(raw, "\\\r\n\x00") {
		d.fail()
		return ""
	}
	return raw
}
func (d *groupDetailDecoder) person(value any, viewer int64, requireAvatar bool) groupDetailPerson {
	row := d.row(value)
	p := groupDetailPerson{ID: d.id(row["id"]), Name: d.optionalText(row["display_name"]), PublicID: d.optionalText(row["public_id"])}
	if p.Name == "" {
		p.Name = d.optionalText(row["username"])
	}
	if p.Name == "" {
		d.fail()
	}
	p.Other = p.ID != viewer
	if p.Other {
		if p.PublicID == "" {
			d.fail()
		}
		p.ProfileURL = routeURL("person", p.PublicID)
	}
	avatar := d.optionalText(row["avatar_uri"])
	if avatar != "" {
		// accounts.Avatars supplies only avatars.DataURI(RenderGeneration(...)).
		// Preserve that image-only URI; never admit a generic trusted URL value.
		const prefix = "data:image/svg+xml;base64,"
		if !strings.HasPrefix(avatar, prefix) || len(avatar) > 1<<20 {
			d.fail()
		} else if decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(avatar, prefix)); err != nil || !strings.HasPrefix(string(decoded), "<svg ") {
			d.fail()
		} else {
			p.Avatar = template.URL(avatar)
		}
	} else if requireAvatar {
		d.fail()
	}
	return p
}

func (r *Renderer) groupDetailPage(request *http.Request, data pongo2.Context) (groupDetailPage, error) {
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated {
		return groupDetailPage{}, platform.ErrForbidden
	}
	d := &groupDetailDecoder{}
	page := groupDetailPage{Copy: map[string]string{}}
	translate := func(message string) string {
		if value, exists := page.Copy[message]; exists {
			return value
		}
		value := r.groupCreateCopy(d.optionalText(data["language"]), message)
		page.Copy[message] = value
		return value
	}
	for _, message := range groupDetailMessages {
		translate(message)
	}
	page.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName}
	page.Shell.English = page.Shell.Language == "en"
	if page.Shell.Language != "en" && page.Shell.Language != "ro" || page.Shell.CSRF == "" {
		d.fail()
	}
	if page.Shell.Theme != "auto" && page.Shell.Theme != "light" && page.Shell.Theme != "dark" && page.Shell.Theme != "contrast" {
		d.fail()
	}
	if page.Shell.Text != "normal" && page.Shell.Text != "large" && page.Shell.Text != "larger" {
		d.fail()
	}
	if page.Shell.Motion != "auto" && page.Shell.Motion != "reduce" && page.Shell.Motion != "full" {
		d.fail()
	}
	if page.Shell.DisplayName == "" {
		page.Shell.DisplayName = actor.Username
	}
	page.PublicID = actor.PublicID
	page.Nonce = d.text(d.row(data["request"])["csp_nonce"])
	if page.Nonce == "" {
		d.fail()
	}
	user := d.row(data["user"])
	if d.id(user["id"]) != actor.ID || d.text(user["cohort"]) != actor.Cohort || d.boolean(user["is_staff"]) != actor.IsStaff {
		d.fail()
	}
	for _, link := range [][2]string{{"Home", "home"}, {"Browse", "activity_list"}, {"Organize", "activity_create"}, {"Places", "places_map"}, {"Events", "events_list"}, {"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}, {"Civic partners", "partners"}, {"Notifications", "notifications"}, {"Messages", "messages"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Support", "donate"}, {"Settings", "settings"}} {
		page.Shell.Navigation = append(page.Shell.Navigation, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	if d.optionalBool(data["connections_enabled"]) {
		page.Shell.Navigation = append(page.Shell.Navigation, familyLink{Label: translate("Connections"), URL: routeURL("connections")})
	}
	for _, link := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}} {
		page.Shell.Footer = append(page.Shell.Footer, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	g := d.row(data["group"])
	page.ID, page.Title, page.Description = d.id(g["id"]), d.text(g["title"]), d.optionalText(g["description"])
	page.Canonical = routeURL("group_detail", page.ID)
	page.Shell.Canonical = page.Canonical
	page.JoinURL, page.LeaveURL, page.ArchiveURL = routeURL("group_join", page.ID), routeURL("group_leave", page.ID), routeURL("group_archive", page.ID)
	page.ListingURL, page.AnnounceURL, page.PostURL, page.AskURL = routeURL("group_listing_toggle", page.ID), routeURL("group_announce", page.ID), routeURL("group_post", page.ID), routeURL("group_ask", page.ID)
	page.Cohort, page.Status = d.text(g["cohort"]), d.text(g["status"])
	cohorts := map[string]string{"adult": "Adult", "teen": "Teen", "child": "Child"}
	statuses := map[string]string{"active": "Active", "archived": "Archived"}
	if cohorts[page.Cohort] == "" || statuses[page.Status] == "" {
		d.fail()
	}
	page.CohortLabel, page.StatusLabel = translate(cohorts[page.Cohort]), translate(statuses[page.Status])
	page.Area = d.text(d.row(g["area"])["name"])
	if g["activity_type"] != nil {
		page.Kind = d.text(d.row(g["activity_type"])["name"])
	} else {
		page.Kind = strings.ReplaceAll(translate("All %(category)s"), "%(category)s", d.text(d.row(g["category"])["name"]))
	}
	page.Owner, page.Member = d.boolean(data["is_owner"]), d.boolean(data["is_member"])
	page.ShowThread, page.CanPost, page.CanAnnounce, page.CanAsk = d.boolean(data["show_thread"]), d.boolean(data["can_post"]), d.boolean(data["can_announce"]), d.boolean(data["can_ask"])
	page.DissentControls = d.optionalBool(data["show_dissent_concern"])
	if page.DissentControls && (!page.CanPost || actor.Cohort == "child") {
		d.fail()
	}
	page.Listed = d.boolean(g["is_publicly_listed"])
	if page.Owner != (d.id(g["owner_id"]) == actor.ID) || page.CanAnnounce != page.Owner || page.CanPost && !page.ShowThread {
		d.fail()
	}
	if page.Cohort != actor.Cohort && !actor.IsStaff {
		d.fail()
	}
	if data["roster"] != nil {
		page.RosterVisible = true
		if actor.Cohort != "adult" || page.Cohort != "adult" || !page.Member || !page.ShowThread {
			d.fail()
		}
		rows := d.rows(data["roster"])
		if len(rows) > 1000 {
			d.fail()
		}
		for _, row := range rows {
			page.Roster = append(page.Roster, d.person(row, actor.ID, true))
		}
		page.RosterHeading = strings.ReplaceAll(translate("Members (%(count_n)s)"), "%(count_n)s", strconv.Itoa(len(page.Roster)))
	}
	if messages := data["messages"]; messages != nil {
		values, ok := messages.([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, message := range values {
				text := message["message"]
				known := false
				for _, allowed := range groupDetailPublicMessages {
					if text == allowed || text == r.catalog.translate(page.Shell.Language, allowed, 1) {
						page.Shell.Messages = append(page.Shell.Messages, translate(allowed))
						known = true
						break
					}
				}
				if !known {
					page.Shell.Messages = append(page.Shell.Messages, translate("Check the form and try again."))
				}
			}
		}
	}
	if page.CanAsk {
		if page.Owner || page.Cohort == "adult" || page.Status != "active" || !d.boolean(g["is_staff_curated"]) || !d.boolean(g["owner_is_staff"]) {
			d.fail()
		}
		prompts, ok := data["question_prompts"].([]any)
		if !ok || len(prompts) != 5 {
			d.fail()
		} else {
			for i, prompt := range prompts {
				pair, ok := prompt.([]string)
				if !ok || len(pair) != 2 || pair[0] != []string{"next_meetup", "where", "what_to_bring", "how_it_works", "more_info"}[i] {
					d.fail()
					continue
				}
				page.Questions = append(page.Questions, familyOption{Value: pair[0], Label: translate(pair[1])})
			}
		}
	}
	tid := d.optionalID(d.row(g["thread"])["id"])
	if config, exists := data["thread_chat_config"]; exists {
		page.ChatJSON = groupDetailChatJSON(d, config, page.ID, tid, actor.ID)
		page.LiveThread = true
		if !page.ShowThread {
			d.fail()
		}
	}
	if nudge, exists := data["presend_nudge"]; exists && nudge != nil {
		page.NudgeJSON = groupDetailNudgeJSON(d, nudge)
	}
	if facets := data["reaction_emojis"]; facets != nil {
		rows, expected := d.rows(facets), socialFacets()
		if len(rows) != len(expected) {
			d.fail()
		} else {
			for i, row := range rows {
				facet := groupDetailFacet{Slug: d.text(row["slug"]), Emoji: d.text(row["emoji"]), Label: d.text(row["label"])}
				if facet.Slug != expected[i]["slug"] || facet.Emoji != expected[i]["emoji"] || facet.Label != r.catalog.translate(page.Shell.Language, expected[i]["label"].(string), 1) {
					d.fail()
				}
				facet.Aria = strings.ReplaceAll(translate("react: %(label)s"), "%(label)s", facet.Label)
				page.ReactionFacets = append(page.ReactionFacets, facet)
			}
		}
	} else if page.LiveThread {
		d.fail()
	}
	if page.ShowThread {
		for _, row := range d.optionalRows(data["announcements"]) {
			page.Announcements = append(page.Announcements, r.groupDetailPost(d, row, actor, page, false, true, translate))
		}
		for _, row := range d.optionalRows(data["posts"]) {
			page.Posts = append(page.Posts, r.groupDetailPost(d, row, actor, page, true, false, translate))
		}
	} else if len(d.optionalRows(data["posts"])) != 0 || len(d.optionalRows(data["announcements"])) != 0 {
		d.fail()
	}
	if page.CanPost {
		form := d.row(data["post_form"])
		page.ComposeBody = d.text(d.row(form["body"])["value"])
		if request.Method == "POST" {
			page.ComposeReplyTo = request.PostForm.Get("reply_to")
		}
		if target := data["reply_target"]; target != nil {
			row := d.row(target)
			author := d.row(row["author"])
			page.ReplyAuthor = d.optionalText(author["display_name"])
			if page.ReplyAuthor == "" {
				page.ReplyAuthor = d.text(author["username"])
			}
			if request.Method != "POST" {
				page.ComposeReplyTo = strconv.FormatInt(d.id(row["id"]), 10)
			}
		}
	}
	for _, row := range d.rows(data["feed"]) {
		page.Feed = append(page.Feed, r.groupDetailActivity(d, row, d.optionalBool(data["show_accent"]), translate))
	}
	if actor.Cohort != page.Cohort {
		if len(page.Feed) != 0 || len(page.Posts) != 0 || page.RosterVisible || page.CanPost || page.Member || page.LiveThread || page.ShowThread && (!page.Owner || !actor.IsStaff) {
			d.fail()
		}
		for _, announcement := range page.Announcements {
			if announcement.AuthorID != actor.ID {
				d.fail()
			}
		}
	}
	if page.ShowThread && !page.LiveThread {
		// The exact existing fallback is owner && staff after a failed thread
		// read. It can also occur within the same cohort after assurance or a
		// membership is withdrawn; cohort equality alone is not live authority.
		if !page.Owner || !actor.IsStaff || page.Member || page.CanPost || page.RosterVisible || len(page.Posts) != 0 {
			d.fail()
		}
		for _, announcement := range page.Announcements {
			if announcement.AuthorID != actor.ID {
				d.fail()
			}
		}
	}
	if len(page.Posts) > 1000 || len(page.Announcements) > 50 {
		d.fail()
	}
	seen := map[int64]bool{}
	count := 0
	for _, posts := range [][]groupDetailPost{page.Announcements, page.Posts} {
		for _, post := range posts {
			if seen[post.ID] {
				d.fail()
			}
			seen[post.ID] = true
			count++
			for _, reply := range post.Replies {
				if seen[reply.ID] {
					d.fail()
				}
				seen[reply.ID] = true
				count++
			}
		}
	}
	if count > 2010 {
		d.fail()
	}
	if d.err != nil {
		return groupDetailPage{}, errGroupDetailSnapshot
	}
	return page, nil
}

func groupDetailChatJSON(d *groupDetailDecoder, value any, groupID, threadID, viewer int64) template.JS {
	row := d.row(value)
	if len(row) != 5 || threadID < 1 || d.id(row["threadId"]) != threadID || d.id(row["meId"]) != viewer || d.text(row["reactUrlTemplate"]) != routeURL("group_post_react", groupID, int64(987654321)) {
		d.fail()
	}
	left, err := json.Marshal(row["reactionFacets"])
	right, otherErr := json.Marshal(socialFacets())
	if err != nil || otherErr != nil || string(left) != string(right) {
		d.fail()
	}
	i18n, ok := row["i18n"].(map[string]string)
	if !ok {
		d.fail()
	}
	keys := []string{"reply", "react", "edited", "replyingTo", "messageSent", "newAnnouncement", "newMessages", "livePaused", "typingOne", "typingTwo", "typingMany", "justNow", "sharedImage", "videoProcessing", "videoFailed", "attachmentExpired", "attachmentDisappears", "attachmentBlocked", "pdfDownloads"}
	if len(i18n) != len(keys) {
		d.fail()
	}
	for _, key := range keys {
		value, exists := i18n[key]
		if !exists {
			d.fail()
		}
		d.text(value)
	}
	raw, err := json.Marshal(row)
	if err != nil {
		d.fail()
		return ""
	}
	// JSON encoding escapes <, >, &, U+2028/U+2029. Only this validated current
	// producer object enters the application/json script data context.
	return template.JS(raw)
}

func groupDetailNudgeJSON(d *groupDetailDecoder, value any) template.JS {
	row := d.row(value)
	if len(row) != 2 {
		d.fail()
	}
	d.text(row["message"])
	rules, err := json.Marshal(row["rules"])
	expected, expectedErr := json.Marshal(socialPresendRules)
	if err != nil || expectedErr != nil || string(rules) != string(expected) {
		d.fail()
	}
	raw, err := json.Marshal(row)
	if err != nil {
		d.fail()
		return ""
	}
	return template.JS(raw)
}

func groupDetailExpiry(d *groupDetailDecoder, value any) string {
	if value == nil {
		return ""
	}
	stamp, ok := spaDateValue(value)
	if !ok {
		d.fail()
		return ""
	}
	minutes := max(0, int(time.Until(stamp).Minutes()))
	if minutes >= 1440 {
		return fmt.Sprintf("%d days", minutes/1440)
	}
	if minutes >= 60 {
		return fmt.Sprintf("%d hours", minutes/60)
	}
	return fmt.Sprintf("%d minutes", minutes)
}
