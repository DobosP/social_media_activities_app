package web

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Current authorized producer results are copied into these presentation values.
// No domain query, state mutation, capability inference or provider call occurs.
type workflowPage struct {
	Shell                                                                                                             familyShell
	Copy                                                                                                              map[string]string
	catalog                                                                                                           translationCatalog
	Kind, Title, Nonce, PublicID, LanguageNext, ReturnTo, SubmitLabel                                                 string
	Avatar                                                                                                            template.URL
	AccountLinks                                                                                                      []familyLink
	Messages                                                                                                          []accountCenterMessage
	Unread                                                                                                            int64
	User                                                                                                              workflowUser
	Form, PostForm                                                                                                    workflowForm
	Activity                                                                                                          workflowActivity
	Place                                                                                                             workflowPlace
	Proposals                                                                                                         []workflowProposal
	IsMember, IsOwner, IsOrganizer, CanManageOrganizers, IsOpen, IsCompleted, ConnEnabled, CanCreateGroup             bool
	Supervised, SupervisorPresent, ChildSafeVenue, ShowWelcome, MyBringsSupport, MyMetConfirmed                       bool
	ArrivalWindowOpen, CanMarkDeparting, CanSetSupport, CanSayOnMyWay, CanSayRunningLate, MeetingPointNeeded, CanJoin bool
	MyArrival, MyDeparture                                                                                            *time.Time
	MyTransit                                                                                                         string
	MyMembership                                                                                                      *workflowMember
	Members, Pending                                                                                                  []workflowMember
	MyGuardians, OwnerSupervisorCandidates                                                                            []workflowUser
	MeetupBrief                                                                                                       []workflowPair
	ActivityCommunities                                                                                               []workflowCommunity
	RSVPSummary, MetSummary                                                                                           workflowSummary
	ConnRelatedIDs                                                                                                    map[int64]bool
	ShareTargets                                                                                                      []workflowShareTarget
	ShareKind                                                                                                         string
	ShareObjID                                                                                                        int64
	Digest                                                                                                            *workflowDigest
	HasOlder, HasReplyTarget, MediaCapabilitiesSupplied, AttachmentsEnabled, FileEnabled, VideoEnabled                bool
	ThreadQuery, ReplyAuthor, AttachmentAccept                                                                        string
	OlderCursor, VideoMaxSeconds                                                                                      int64
	Announcements, Posts, ThreadResults                                                                               []workflowThreadPost
	Photos                                                                                                            []workflowPhoto
	EphemeralOptions                                                                                                  []familyOption
	HasThreadAudience, AudienceIsGroup                                                                                bool
	AudiencePeers                                                                                                     *int64
	ChatJSON, NudgeJSON, VocabularyJSON, MessagingJSON                                                                template.JS
}
type workflowProposal struct {
	ID, ConfirmationsCount, RequiredConfirmations int64
	Place                                         workflowPlace
}
type workflowCommunity struct{ Name, Slug string }

//go:embed pages/workflow*.gohtml
var workflowPages embed.FS

var errWorkflowSnapshot = errors.New("invalid native workflow snapshot")
var loadWorkflowTemplates = sync.OnceValues(func() (*template.Template, error) {
	parsed := template.New("workflows").Funcs(workflowTemplateFuncs())
	var err error
	parsed, err = parsed.ParseFS(workflowPages, "pages/workflow*.gohtml")
	if err != nil {
		return nil, err
	}
	// Reuse the existing complete post/media/reaction markup after assigning only
	// activity action destinations in the copied activity post model.
	raw, err := groupDetailPages.ReadFile("pages/group-detail-post.gohtml")
	if err != nil {
		return nil, err
	}
	return parsed.Parse(string(raw))
})

func (p workflowPage) T(message string) string {
	if value, ok := p.Copy[message]; ok {
		return value
	}
	return html.UnescapeString(p.catalog.translate(p.Shell.Language, message, 1))
}

// ComposeBody keeps one explicit accessible name while preserving the copied
// field value, type, constraints, help and errors. It never reads request data.
func (p workflowPage) ComposeBody() workflowField {
	field := p.PostForm.Field("body")
	field.Label = p.T("Write a message")
	return field
}

func (p workflowPage) B(index int, count any, values ...any) template.HTML {
	if index < 0 || index >= len(workflowBlockMessages) {
		return ""
	}
	for i, value := range values {
		switch v := value.(type) {
		case *int64:
			if v == nil {
				values[i] = ""
			} else {
				values[i] = *v
			}
		}
	}
	n := 1
	switch value := count.(type) {
	case int:
		n = value
	case int64:
		n = int(value)
	case *int64:
		if value != nil {
			n = int(*value)
		}
	}
	source := workflowBlockMessages[index]
	if n != 1 {
		if plural, exists := workflowBlockPluralMessages[index]; exists {
			source = plural
		}
	}
	encoded := base64.RawStdEncoding.EncodeToString([]byte(source))
	// The msgid is an immutable template literal; existing translatedBlock escapes
	// each dynamic substitution before retaining that literal's permitted markup.
	return template.HTML(translatedBlock(p.catalog, p.Shell.Language, encoded, n, values...).String())
}
func workflowTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"wfurl": routeURL, "wfprev": func(n int) int { return n - 1 }, "wflower": strings.ToLower, "wfquery": url.QueryEscape,
		"wfdefault": func(value, fallback any) any {
			if value == nil {
				return fallback
			}
			if s, ok := value.(string); ok && s == "" {
				return fallback
			}
			return value
		},
		"wfdate": func(value *time.Time, format string) string {
			if value == nil {
				return ""
			}
			return djangoDate(*value, format)
		},
		"wflines": func(value string) template.HTML {
			return template.HTML(strings.ReplaceAll(html.EscapeString(value), "\n", "<br>"))
		},
		"wfparagraphs": func(value string) template.HTML {
			escaped := html.EscapeString(strings.ReplaceAll(value, "\r\n", "\n"))
			return template.HTML("<p>" + strings.ReplaceAll(strings.ReplaceAll(escaped, "\n\n", "</p><p>"), "\n", "<br>") + "</p>")
		},
		"wftruncate": func(value string, n int) string {
			r := []rune(value)
			if len(r) <= n {
				return value
			}
			if n < 1 {
				return ""
			}
			return string(r[:n-1]) + "…"
		},
		"wfyesno": func(value *bool, choices string) string {
			parts := strings.Split(choices, ",")
			if len(parts) < 2 {
				return ""
			}
			if value != nil && *value {
				return parts[0]
			}
			return parts[1]
		},
		"wfnotin": func(id int64, ids map[int64]bool) bool { return !ids[id] },
	}
}

func (r *Renderer) workflowPage(request *http.Request, name string, data pongo2.Context) (workflowPage, error) {
	actor, signed := platform.ActorFrom(request)
	if !signed || actor.ID < 1 {
		return workflowPage{}, platform.ErrForbidden
	}
	p := workflowPage{Copy: map[string]string{}, catalog: r.catalog, PublicID: actor.PublicID, User: workflowUser{ID: actor.ID, Username: actor.Username, DisplayName: actor.DisplayName, PublicID: actor.PublicID, IsStaff: actor.IsStaff, Cohort: actor.Cohort}}
	switch name {
	case "web/activity_form.html":
		p.Kind = "activity-create"
	case "web/activity_edit.html":
		p.Kind = "activity-edit"
	case "web/activity_detail.html":
		p.Kind = "activity-detail"
	case "web/activity_safe_exit.html":
		p.Kind = "safe-exit"
	case "web/place_propose.html":
		p.Kind = "place-propose"
	case "web/place_claim.html":
		p.Kind = "place-claim"
	case "web/places_pending.html":
		p.Kind = "places-pending"
	case "web/messages.html":
		p.Kind = "messages"
	default:
		return workflowPage{}, errWorkflowSnapshot
	}
	d := &groupDetailDecoder{}
	context := d.row(data["request"])
	p.Nonce, p.LanguageNext = d.text(context["csp_nonce"]), d.text(context["path"])
	p.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), Canonical: d.text(data["canonical_url"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName}
	p.Shell.English = p.Shell.Language == "en"
	if p.Shell.Language != "en" && p.Shell.Language != "ro" || p.Shell.SiteName == "" || p.Shell.CSRF == "" || p.Nonce == "" || p.LanguageNext != request.URL.Path || !accountCenterCanonical(p.Shell.Canonical) {
		d.fail()
	}
	if p.Shell.Theme != "auto" && p.Shell.Theme != "light" && p.Shell.Theme != "dark" && p.Shell.Theme != "contrast" || p.Shell.Text != "normal" && p.Shell.Text != "large" && p.Shell.Text != "larger" || p.Shell.Motion != "auto" && p.Shell.Motion != "reduce" && p.Shell.Motion != "full" {
		d.fail()
	}
	user := d.row(data["user"])
	if !d.boolean(user["is_authenticated"]) || d.text(user["public_id"]) != actor.PublicID || d.text(user["username"]) != actor.Username || d.text(user["display_name"]) != actor.DisplayName {
		d.fail()
	}
	for _, key := range []string{"id", "pk"} {
		if user[key] != nil && d.id(user[key]) != actor.ID {
			d.fail()
		}
	}
	if user["cohort"] != nil && d.text(user["cohort"]) != actor.Cohort {
		d.fail()
	}
	if user["is_staff"] != nil && d.boolean(user["is_staff"]) != actor.IsStaff {
		d.fail()
	}
	if p.Shell.DisplayName == "" {
		p.Shell.DisplayName = actor.Username
	}
	p.Avatar = d.person(map[string]any{"id": actor.ID, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": user["avatar_uri"]}, actor.ID, false).Avatar
	if data["unread_notifications"] != nil {
		p.Unread = d.integer(data["unread_notifications"])
		if p.Unread < 0 {
			d.fail()
		}
	}
	for _, message := range workflowMessages {
		p.Copy[message] = p.T(message)
	}
	for _, message := range groupDetailMessages {
		p.Copy[message] = p.T(message)
	}
	link := func(label, route string) familyLink { return familyLink{Label: p.T(label), URL: routeURL(route)} }
	for _, item := range [][2]string{{"Browse", "activity_list"}, {"Organize", "activity_create"}, {"Places", "places_map"}, {"Events", "events_list"}, {"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}, {"Civic partners", "partners"}, {"Notifications", "notifications"}, {"Messages", "messages"}} {
		p.Shell.Navigation = append(p.Shell.Navigation, link(item[0], item[1]))
	}
	p.AccountLinks = append(p.AccountLinks, link("Profile", "profile"))
	if d.optionalBool(data["connections_enabled"]) {
		p.AccountLinks = append(p.AccountLinks, link("Connections", "connections"))
	}
	for _, item := range [][2]string{{"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Account overview", "you"}, {"Settings", "settings"}, {"Support", "donate"}} {
		p.AccountLinks = append(p.AccountLinks, link(item[0], item[1]))
	}
	for _, item := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}, {"Settings", "settings"}} {
		p.Shell.Footer = append(p.Shell.Footer, link(item[0], item[1]))
	}
	if data["messages"] != nil {
		messages, ok := data["messages"].([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, message := range messages {
				p.Messages = append(p.Messages, accountCenterMessage{Text: d.text(message["message"]), Tags: d.text(message["tags"])})
			}
		}
	}
	switch p.Kind {
	case "activity-create":
		p.Title = p.T("Organise an activity")
		p.SubmitLabel = p.T("Create activity")
		p.Form = r.workflowForm(data["form"], "ActivityForm", actor, d, p.T)
		p.VocabularyJSON = workflowVocabulary(d, data["type_vocabulary"])
	case "activity-edit", "activity-detail", "safe-exit":
		r.workflowActivityPage(request, data, &p, d, actor)
		if p.Kind == "activity-edit" {
			p.SubmitLabel = p.T("Save changes")
			p.VocabularyJSON = workflowVocabulary(d, data["type_vocabulary"])
		}
	case "place-propose":
		p.Title = p.T("Add a place")
		p.Form = r.workflowForm(data["form"], "PlaceProposeForm", actor, d, p.T)
		p.ReturnTo = d.optionalText(data["return_to"])
		if p.ReturnTo != "" && p.ReturnTo != "organize" {
			d.fail()
		}
	case "place-claim":
		p.Title = p.T("Claim this venue")
		p.Place = workflowPlaceProjection(d, data["place"])
		p.Form = r.workflowForm(data["form"], "PlaceClaimForm", actor, d, p.T)
	case "places-pending":
		p.Title = p.T("Pending places")
		for _, row := range d.rows(data["proposals"]) {
			proposal := workflowProposal{ID: d.id(row["id"]), Place: workflowPlaceProjection(d, row["place"]), ConfirmationsCount: d.integer(row["confirmations_count"]), RequiredConfirmations: d.integer(row["required_confirmations"])}
			p.Proposals = append(p.Proposals, proposal)
		}
	case "messages":
		p.Title = p.T("Messages")
		p.CanCreateGroup = d.boolean(data["can_create_group"])
		p.MessagingJSON = workflowMessagingJSON(d, data["messaging_config"], actor)
	}
	if d.err != nil {
		return workflowPage{}, errWorkflowSnapshot
	}
	return p, nil
}

func workflowVocabulary(d *groupDetailDecoder, value any) template.JS {
	rows := d.rows(value)
	copied := []map[string]any{}
	for _, row := range rows {
		v := map[string]any{"id": d.id(row["id"]), "name": d.text(row["name"]), "slug": d.text(row["slug"]), "category": d.text(row["category"]), "aliases": append([]string(nil), d.strings(row["aliases"])...)}
		copied = append(copied, v)
	}
	raw, err := json.Marshal(copied)
	if err != nil {
		d.fail()
		return ""
	}
	return template.JS(raw)
}

func workflowMessagingJSON(d *groupDetailDecoder, value any, actor platform.Actor) template.JS {
	row := d.row(value)
	if len(row) != 3 {
		d.fail()
	}
	person := func(value any) map[string]any {
		u := d.row(value)
		if len(u) != 4 {
			d.fail()
		}
		return map[string]any{"public_id": d.text(u["public_id"]), "username": d.text(u["username"]), "display_name": d.text(u["display_name"]), "avatar": d.text(u["avatar"])}
	}
	me := person(row["me"])
	display := actor.DisplayName
	if display == "" {
		display = actor.Username
	}
	if me["public_id"] != actor.PublicID || me["username"] != actor.Username || me["display_name"] != display {
		d.fail()
	}
	connections := []map[string]any{}
	for _, connection := range d.rows(row["connections"]) {
		connections = append(connections, person(connection))
	}
	reactions := append([]string(nil), d.strings(row["reaction_emojis"])...)
	expected := []string{"👍", "❤️", "🎉", "👏", "🙏"}
	if len(reactions) != len(expected) {
		d.fail()
	} else {
		for i := range expected {
			if reactions[i] != expected[i] {
				d.fail()
			}
		}
	}
	copied := map[string]any{"me": me, "connections": connections, "reaction_emojis": reactions}
	raw, err := json.Marshal(copied)
	if err != nil {
		d.fail()
		return ""
	}
	return template.JS(raw)
}
func (r *Renderer) renderWorkflows(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	p, err := r.workflowPage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadWorkflowTemplates()
	if err != nil {
		return nil, err
	}
	return executeWorkflowTemplate(parsed, p)
}
func executeWorkflowTemplate(parsed *template.Template, p workflowPage) ([]byte, error) {
	var out bytes.Buffer
	if err := parsed.ExecuteTemplate(&out, "workflows", p); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
