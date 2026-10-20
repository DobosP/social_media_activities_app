package web

import (
	"bytes"
	"embed"
	"errors"
	"html"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// These four form snapshots are additive producer metadata. The existing form
// builder owns order, options and binding; no widget or safe HTML is parsed.
type seriesGaugeFormSnapshot struct {
	Name, Cohort string
	Staff        bool
	Fields       []seriesGaugeFormInput
}
type seriesGaugeFormInput struct {
	Name, Value         string
	HasOptions, Checked bool
	Options             []option
}
type seriesGaugeField struct {
	Name, Label, Value, Help, Type             string
	Required, Select, Textarea, Checked, Combo bool
	MaxLength, MinLength                       int
	Options                                    []familyOption
}
type seriesGaugeForm struct {
	Fields []seriesGaugeField
	Error  string
}
type seriesGaugeSeries struct {
	ID                                                                                             int64
	Title, Description, Activity, Place, DisplayPlace, Cadence, Status, StatusLabel, Next, At, URL string
	Active                                                                                         bool
}
type seriesGaugeInstance struct{ Title, URL, When, Status string }
type seriesGaugeGauge struct {
	ID                                                int64
	Activity, Place, City, Window, URL, Readiness, At string
	Ready                                             bool
	Remaining                                         int64
}
type seriesGaugeAction struct{ URL, Label, Confirm string }
type seriesGaugePage struct {
	Shell                                                                                                                          familyShell
	Copy                                                                                                                           map[string]string
	Messages                                                                                                                       []accountCenterMessage
	AccountLinks                                                                                                                   []familyLink
	Avatar                                                                                                                         template.URL
	Title, Intro, Nonce, PublicID, LanguageNext, BackURL, BackLabel, CreateURL, CreateLabel, Submit, After, OwnerNote, NextNoteURL string
	Unread                                                                                                                         int64
	SeriesList, SeriesDetail, GaugeList, GaugeDetail, Owner, Proposer, Interested, ShowForm                                        bool
	Series                                                                                                                         []seriesGaugeSeries
	Instances                                                                                                                      []seriesGaugeInstance
	Gauges                                                                                                                         []seriesGaugeGauge
	CurrentSeries                                                                                                                  seriesGaugeSeries
	CurrentGauge                                                                                                                   seriesGaugeGauge
	Actions                                                                                                                        []seriesGaugeAction
	Form, NextNote                                                                                                                 seriesGaugeForm
	ShowNextNote                                                                                                                   bool
}

var nativeSeriesGaugeTemplates = map[string]bool{
	"web/series_list.html": true, "web/series_detail.html": true, "web/series_form.html": true,
	"web/gauges.html": true, "web/gauge_detail.html": true, "web/gauge_form.html": true, "web/gauge_convert.html": true,
}

func seriesGaugeFormName(name string) bool {
	return name == "SeriesForm" || name == "GaugeForm" || name == "GaugeConvertForm" || name == "NextInstanceNoteForm"
}

//go:embed pages/series-gauges.gohtml
var seriesGaugePages embed.FS
var errSeriesGaugeSnapshot = errors.New("invalid native series/gauge snapshot")
var loadSeriesGaugeTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("series-gauges").ParseFS(seriesGaugePages, "pages/series-gauges.gohtml")
})

func seriesGaugeFieldNames(name, cohort string, staff bool) []string {
	names := []string{}
	for key := range definitions[name] {
		if key == "supervised" && cohort != "child" || key == "cohort" && !staff {
			continue
		}
		names = append(names, key)
	}
	sort.Slice(names, func(i, j int) bool { return definitions[name][names[i]].Order < definitions[name][names[j]].Order })
	return names
}
func (r *Renderer) seriesGaugeForm(value any, name string, actor platform.Actor, d *groupDetailDecoder, tr func(string) string) seriesGaugeForm {
	out := seriesGaugeForm{}
	form := d.row(value)
	snapshot, ok := form["native_series_gauges"].(seriesGaugeFormSnapshot)
	names := seriesGaugeFieldNames(name, actor.Cohort, actor.IsStaff)
	if !ok || !seriesGaugeFormName(name) || snapshot.Name != name || snapshot.Cohort != actor.Cohort || snapshot.Staff != actor.IsStaff || len(snapshot.Fields) != len(names) {
		d.fail()
		return out
	}
	out.Error = d.text(form["errors"])
	for i, input := range snapshot.Fields {
		if input.Name != names[i] {
			d.fail()
			continue
		}
		def := definitions[name][input.Name]
		label := def.Label
		if label == "" {
			label = strings.ReplaceAll(input.Name, "_", " ")
		}
		field := seriesGaugeField{Name: input.Name, Label: tr(label), Value: d.text(input.Value), Help: tr(def.HelpText), Required: def.Required, Textarea: def.Textarea || def.Kind == "_logistics_field", Select: input.HasOptions, Checked: input.Checked, MaxLength: def.MaxLength, MinLength: def.MinLength, Type: "text"}
		if field.Select {
			field.Combo = input.Name == "activity_type"
			field.Options = append(field.Options, familyOption{Value: "", Label: "---------", Selected: input.Value == ""})
			seen := map[string]bool{}
			for _, choice := range input.Options {
				v, label := d.text(choice.Value), d.text(choice.Label)
				if v == "" || seen[v] {
					d.fail()
				}
				seen[v] = true
				field.Options = append(field.Options, familyOption{Value: v, Label: label, Selected: v == input.Value})
			}
		} else if len(input.Options) != 0 {
			d.fail()
		}
		if !field.Select && !field.Textarea {
			switch def.Kind {
			case "IntegerField", "_cost_amount_field":
				field.Type = "number"
			case "BooleanField":
				field.Type = "checkbox"
			case "_dt_field":
				field.Type = "datetime-local"
			}
		}
		if def.Kind == "BooleanField" && input.Value != "on" || def.Kind != "BooleanField" && input.Checked {
			d.fail()
		}
		out.Fields = append(out.Fields, field)
	}
	return out
}

func (r *Renderer) seriesGaugePage(request *http.Request, name string, data pongo2.Context) (seriesGaugePage, error) {
	if !nativeSeriesGaugeTemplates[name] {
		return seriesGaugePage{}, errSeriesGaugeSnapshot
	}
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated {
		return seriesGaugePage{}, platform.ErrForbidden
	}
	d := groupDetailDecoder{}
	p := seriesGaugePage{Copy: map[string]string{}, PublicID: actor.PublicID}
	ctx := d.row(data["request"])
	p.Nonce, p.LanguageNext = d.text(ctx["csp_nonce"]), d.text(ctx["path"])
	p.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), Canonical: d.text(data["canonical_url"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName}
	p.Shell.English = p.Shell.Language == "en"
	if p.Shell.Language != "en" && p.Shell.Language != "ro" || p.Shell.SiteName == "" || p.Shell.CSRF == "" || p.Nonce == "" || p.LanguageNext != request.URL.Path || !accountCenterCanonical(p.Shell.Canonical) {
		d.fail()
	}
	if p.Shell.Theme != "auto" && p.Shell.Theme != "light" && p.Shell.Theme != "dark" && p.Shell.Theme != "contrast" || p.Shell.Text != "normal" && p.Shell.Text != "large" && p.Shell.Text != "larger" || p.Shell.Motion != "auto" && p.Shell.Motion != "reduce" && p.Shell.Motion != "full" {
		d.fail()
	}
	user := d.row(data["user"])
	if d.id(user["id"]) != actor.ID || d.text(user["cohort"]) != actor.Cohort || d.boolean(user["is_staff"]) != actor.IsStaff {
		d.fail()
	}
	if p.Shell.DisplayName == "" {
		p.Shell.DisplayName = actor.Username
	}
	if data["unread_notifications"] != nil {
		p.Unread = d.integer(data["unread_notifications"])
		if p.Unread < 0 {
			d.fail()
		}
	}
	p.Avatar = d.person(map[string]any{"id": actor.ID, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": user["avatar_uri"]}, actor.ID, false).Avatar
	tr := func(message string) string {
		if value, ok := p.Copy[message]; ok {
			return value
		}
		value := html.UnescapeString(r.catalog.translate(p.Shell.Language, message, 1))
		p.Copy[message] = value
		return value
	}
	for _, message := range []string{"Activities", "Home", "Browse", "Organize", "Profile", "Menu", "Primary", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language", "Scheduled meetups", "Series controls", "Pause", "Resume", "End series", "Save heads-up", "You started this gauge.", "Ready to start", "ready to start", "Create one?", "Enough people are interested to start this as a real meetup.", "Temporary: this gauge expires on its own if it isn't converted.", "No meetups scheduled yet &mdash; the next one appears automatically before it starts.", "No open gauges right now &mdash; start one above.", "You haven't set up any recurring series yet.", "Pausing stops new meetups from being scheduled; ending is permanent. Meetups already scheduled always stand.", "End this series? No more meetups will be scheduled. This can't be undone.", "Added to the next scheduled meetup's organiser note, then cleared automatically. Leave blank to clear."} {
		tr(message)
	}
	link := func(label, route string) familyLink { return familyLink{Label: tr(label), URL: routeURL(route)} }
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
	if raw := data["messages"]; raw != nil {
		messages, ok := raw.([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, message := range messages {
				p.Messages = append(p.Messages, accountCenterMessage{Text: d.text(message["message"]), Tags: d.optionalText(message["tags"])})
			}
		}
	}
	series := func(row map[string]any) seriesGaugeSeries {
		s := seriesGaugeSeries{ID: d.id(row["pk"]), Title: d.text(row["title"]), Description: d.optionalText(row["description"]), Cadence: d.text(row["get_cadence_display"]), Status: d.text(row["status"]), StatusLabel: d.text(row["get_status_display"])}
		s.Activity = d.text(d.row(row["activity_type"])["name"])
		place := d.row(row["place"])
		s.Place = d.text(place["name"])
		s.DisplayPlace = d.text(place["display_name"])
		if s.Status != "active" && s.Status != "paused" && s.Status != "ended" {
			d.fail()
		}
		s.Active = s.Status == "active"
		s.URL = routeURL("series_detail", s.ID)
		if s.Active {
			s.Next = strings.ReplaceAll(tr("next around %(when)s"), "%(when)s", d.stamp(row["next_starts_at"], "j M, H:i"))
		}
		s.At = strings.ReplaceAll(tr("At %(place)s."), "%(place)s", s.DisplayPlace)
		return s
	}
	gauge := func(row map[string]any, ready any, remaining any) seriesGaugeGauge {
		g := seriesGaugeGauge{ID: d.id(row["pk"]), Activity: d.text(d.row(row["activity_type"])["name"]), Window: d.text(row["get_coarse_window_display"]), Ready: d.boolean(ready), Remaining: d.integer(remaining)}
		place := d.row(row["place"])
		g.Place = d.text(place["name"])
		g.City = d.optionalText(place["address_city"])
		g.URL = routeURL("gauge_detail", g.ID)
		g.At = strings.ReplaceAll(tr("at %(place)s"), "%(place)s", g.Place)
		if g.Remaining < 0 || int64(int(g.Remaining)) != g.Remaining {
			d.fail()
		}
		message := "needs %(counter)s more"
		if name == "web/gauge_detail.html" {
			message = "Needs %(counter)s more to be ready"
		}
		g.Readiness = strings.ReplaceAll(html.UnescapeString(r.catalog.translate(p.Shell.Language, message, int(g.Remaining))), "%(counter)s", strconv.FormatInt(g.Remaining, 10))
		return g
	}
	switch name {
	case "web/series_list.html":
		p.SeriesList = true
		p.Title = tr("Your recurring series")
		p.Intro = tr("Repeating meetups you've set up. Each one auto-schedules its next instance before it starts; every instance needs a fresh join.")
		p.CreateURL = routeURL("series_create")
		p.CreateLabel = tr("New recurring series")
		for _, row := range d.rows(data["series"]) {
			p.Series = append(p.Series, series(row))
		}
	case "web/series_detail.html":
		p.SeriesDetail = true
		p.CurrentSeries = series(d.row(data["series"]))
		p.Title = p.CurrentSeries.Title
		p.BackURL = routeURL("series_list")
		p.BackLabel = tr("Your recurring series")
		p.Owner = d.boolean(data["is_owner"])
		if p.CurrentSeries.Active {
			p.CurrentSeries.Next = strings.ReplaceAll(tr("Next instance scheduled around %(when)s."), "%(when)s", d.stamp(d.row(data["series"])["next_starts_at"], "l j F Y, H:i"))
		}
		for _, row := range d.rows(data["instances"]) {
			p.Instances = append(p.Instances, seriesGaugeInstance{Title: d.text(row["title"]), URL: routeURL("activity_detail", d.id(row["pk"])), When: d.stamp(row["starts_at"], "D j M, H:i"), Status: d.text(row["get_status_display"])})
		}
		if p.Owner {
			if p.CurrentSeries.Status == "active" {
				p.Actions = append(p.Actions, seriesGaugeAction{URL: routeURL("series_pause", p.CurrentSeries.ID), Label: tr("Pause")})
			} else if p.CurrentSeries.Status == "paused" {
				p.Actions = append(p.Actions, seriesGaugeAction{URL: routeURL("series_resume", p.CurrentSeries.ID), Label: tr("Resume")})
			}
			if p.CurrentSeries.Status != "ended" {
				p.Actions = append(p.Actions, seriesGaugeAction{URL: routeURL("series_end", p.CurrentSeries.ID), Label: tr("End series"), Confirm: tr("End this series? No more meetups will be scheduled. This can't be undone.")})
				p.ShowNextNote = true
				p.NextNoteURL = routeURL("series_set_next_note", p.CurrentSeries.ID)
				p.NextNote = r.seriesGaugeForm(data["next_note_form"], "NextInstanceNoteForm", actor, &d, tr)
			}
		}
	case "web/gauges.html":
		p.GaugeList = true
		p.Title = tr("Gauge interest")
		p.Intro = tr("A gauge is a quick, temporary &ldquo;I'd come&rdquo; poll for a place and rough time. It's\n  NOT a standing group &mdash; if enough people your age signal interest, the person who started it\n  can turn it into a real meetup. If not, it just quietly expires.")
		p.CreateURL = routeURL("gauge_create")
		p.CreateLabel = tr("+ Start a gauge")
		for _, item := range d.rows(data["items"]) {
			p.Gauges = append(p.Gauges, gauge(d.row(item["gauge"]), item["ready"], item["remaining"]))
		}
	case "web/gauge_detail.html":
		p.GaugeDetail = true
		p.CurrentGauge = gauge(d.row(data["gauge"]), data["ready"], data["remaining"])
		p.Title = p.CurrentGauge.Activity + " " + p.CurrentGauge.At
		p.BackURL = routeURL("gauges")
		p.BackLabel = tr("Gauge interest")
		p.Proposer = d.boolean(data["is_proposer"])
		p.Interested = d.boolean(data["viewer_interested"])
		if p.Proposer {
			p.CreateURL = routeURL("gauge_convert", p.CurrentGauge.ID)
			p.CreateLabel = tr("Turn into a real meetup")
		} else if p.Interested {
			p.Actions = append(p.Actions, seriesGaugeAction{URL: routeURL("gauge_uninterested", p.CurrentGauge.ID), Label: tr("I'm no longer interested")})
		} else {
			p.Actions = append(p.Actions, seriesGaugeAction{URL: routeURL("gauge_interested", p.CurrentGauge.ID), Label: tr("I'd come")})
		}
	case "web/series_form.html":
		p.ShowForm = true
		p.Title = tr("Create a recurring series")
		p.Intro = strings.TrimSuffix(strings.TrimPrefix(tr(seriesGaugeSeriesIntro), `<p class="muted">`), "</p>")
		p.BackURL = routeURL("activity_create")
		p.BackLabel = tr("Just organising once? Create a single activity instead.")
		p.Submit = tr("Create series")
		p.Form = r.seriesGaugeForm(data["form"], "SeriesForm", actor, &d, tr)
	case "web/gauge_form.html":
		p.ShowForm = true
		p.Title = tr("Start a gauge")
		p.Intro = tr("Float a place and a rough time to see who'd come, before committing to a real meetup. It's temporary until you convert it.")
		p.BackURL = routeURL("gauges")
		p.BackLabel = tr("Gauge interest")
		p.Submit = tr("Start gauge")
		p.Form = r.seriesGaugeForm(data["form"], "GaugeForm", actor, &d, tr)
	case "web/gauge_convert.html":
		p.ShowForm = true
		g := d.row(data["gauge"])
		pk := d.id(g["pk"])
		p.Title = tr("Turn into a real meetup")
		p.BackURL = routeURL("gauge_detail", pk)
		p.BackLabel = tr("Back to gauge")
		p.Submit = tr("Create the meetup")
		p.Intro = strings.NewReplacer("%(type)s", d.text(d.row(g["activity_type"])["name"]), "%(place)s", d.text(d.row(g["place"])["name"])).Replace(tr("This creates a real %(type)s meetup at %(place)s and invites everyone who signalled interest. Pick a concrete time below."))
		p.Form = r.seriesGaugeForm(data["form"], "GaugeConvertForm", actor, &d, tr)
	}
	if d.err != nil {
		return seriesGaugePage{}, errSeriesGaugeSnapshot
	}
	return p, nil
}
func (r *Renderer) renderSeriesGauges(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	page, err := r.seriesGaugePage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadSeriesGaugeTemplates()
	if err != nil {
		return nil, err
	}
	return executeSeriesGaugeTemplate(parsed, page)
}
func executeSeriesGaugeTemplate(parsed *template.Template, page seriesGaugePage) ([]byte, error) {
	var output bytes.Buffer
	if err := parsed.ExecuteTemplate(&output, "series-gauges", page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

// Exact original whole-block msgid. Remove only its fixed source paragraph
// wrapper; the translated body remains ordinary escaped text in the native p.
const seriesGaugeSeriesIntro = "<p class=\"muted\">Define a repeating meetup once &mdash; the next instance is scheduled\n  automatically before it starts, visible to people your age. Each instance needs a fresh join,\n  so it never becomes a standing roster.</p>"
