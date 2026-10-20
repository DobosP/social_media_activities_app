package web

import (
	"bytes"
	"embed"
	"errors"
	"html"
	"html/template"
	"net/http"
	"strings"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Presentation only: these snapshots have already passed the self/cohort,
// participation and current membership checks of their existing producers.
// Projection neither loads records nor derives eligibility or venue flags.
type collectionsPage struct {
	Shell                                                              familyShell
	Copy                                                               map[string]string
	Kind, Nonce, PublicID, LanguageNext, Title, CalendarURL, BrowseURL string
	Avatar                                                             template.URL
	AccountLinks                                                       []familyLink
	Messages                                                           []accountCenterMessage
	Unread                                                             int64
	Meetups                                                            []collectionsMeetup
	Guardians                                                          []string
	Venues                                                             []collectionsVenue
	Generated                                                          string
}
type collectionsMeetup struct {
	Title, URL, Type, Start, End, Place, MeetingPoint string
}
type collectionsVenue struct {
	Name, URL string
	Flags     []collectionsFlag
}
type collectionsFlag struct {
	Text    string
	Warning bool
}

//go:embed pages/collections.gohtml
var collectionsPages embed.FS
var errCollectionsSnapshot = errors.New("invalid native collections snapshot")
var loadCollectionsTemplates = sync.OnceValues(func() (*template.Template, error) {
	raw, err := collectionsPages.ReadFile("pages/collections.gohtml")
	if err != nil {
		return nil, err
	}
	return parseCollectionsTemplate(string(raw))
})

func parseCollectionsTemplate(source string) (*template.Template, error) {
	return template.New("collections").Parse(source)
}

func (r *Renderer) collectionsPage(request *http.Request, name string, data pongo2.Context) (collectionsPage, error) {
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated || actor.ID < 1 {
		return collectionsPage{}, platform.ErrForbidden
	}
	p := collectionsPage{Copy: map[string]string{}, PublicID: actor.PublicID}
	switch name {
	case "web/my_meetups.html":
		p.Kind = "meetups"
	case "web/my_venues.html":
		p.Kind = "venues"
	default:
		return collectionsPage{}, errCollectionsSnapshot
	}
	d := groupDetailDecoder{}
	context := d.row(data["request"])
	p.Nonce, p.LanguageNext = d.text(context["csp_nonce"]), d.text(context["path"])
	p.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), Canonical: d.text(data["canonical_url"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName}
	p.Shell.English = p.Shell.Language == "en"
	if (p.Shell.Language != "en" && p.Shell.Language != "ro") || p.Shell.SiteName == "" || p.Shell.CSRF == "" || p.Nonce == "" || p.LanguageNext != request.URL.Path || !accountCenterCanonical(p.Shell.Canonical) {
		d.fail()
	}
	if p.Shell.Theme != "auto" && p.Shell.Theme != "light" && p.Shell.Theme != "dark" && p.Shell.Theme != "contrast" || p.Shell.Text != "normal" && p.Shell.Text != "large" && p.Shell.Text != "larger" || p.Shell.Motion != "auto" && p.Shell.Motion != "reduce" && p.Shell.Motion != "full" {
		d.fail()
	}
	user := d.row(data["user"])
	if !d.boolean(user["is_authenticated"]) || d.text(user["public_id"]) != actor.PublicID || d.text(user["username"]) != actor.Username || d.text(user["display_name"]) != actor.DisplayName {
		d.fail()
	}
	// Accept the sparse Renderer default and the richer SocialView identity.
	// Optional rich identifiers are checked, never required or rendered.
	for _, key := range []string{"id", "pk"} {
		if user[key] != nil && d.id(user[key]) != actor.ID {
			d.fail()
		}
	}
	if user["cohort"] != nil && d.text(user["cohort"]) != actor.Cohort {
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
	tr := func(message string) string {
		if value, exists := p.Copy[message]; exists {
			return value
		}
		value := html.UnescapeString(r.catalog.translate(p.Shell.Language, message, 1))
		p.Copy[message] = value
		return value
	}
	for _, message := range []string{
		"Activities", "Home", "Browse", "Organize", "Profile", "Menu", "Primary", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language",
		"My meetups", "My next meetups", "Saved %(when)s. The essentials to get there &mdash; readable even on a patchy connection.", "Add to my calendar (.ics)", "A one-time download of your upcoming meetups for your phone or desktop calendar.", "You're offline &mdash; this is a saved copy. A meetup cancelled or moved since you last had signal may not show here.", "Where to meet:", "No upcoming meetups yet.", "Find one to join", "Grown-ups you can turn to", "If something doesn't feel right, you can reach out to:", "Place",
		"Check your venues", "Venues worth a quick check", "For the meetups you're going to, these venues have a data-quality flag right now &mdash; worth a quick check before you head out. This page is just for you and is never sent as a notification.", "reported closed", "posted hours unverified", "a correction is pending", "Nothing to flag &mdash; the venues for your upcoming meetups all look fine.",
	} {
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
	case "meetups":
		p.Title = tr("My meetups")
		p.CalendarURL = routeURL("my_calendar")
		p.BrowseURL = routeURL("activity_list")
		p.Generated = strings.ReplaceAll(tr("Saved %(when)s. The essentials to get there &mdash; readable even on a patchy connection."), "%(when)s", d.stamp(data["generated_at"], "D d M, H:i"))
		for _, row := range d.rows(data["meetups"]) {
			item := collectionsMeetup{Title: d.text(row["title"]), URL: routeURL("activity_detail", d.id(row["pk"])), Type: d.text(d.row(row["activity_type"])["name"]), Start: d.stamp(row["starts_at"], "D d M, H:i"), End: d.stamp(row["ends_at"], "H:i"), MeetingPoint: d.optionalText(row["meeting_point"])}
			if row["place"] != nil {
				place := d.row(row["place"])
				item.Place = d.optionalText(place["display_name"])
				if item.Place == "" {
					item.Place = d.optionalText(place["name"])
				}
				if item.Place == "" {
					item.Place = tr("Place")
				}
			}
			p.Meetups = append(p.Meetups, item)
		}
		for _, row := range d.optionalRows(data["my_guardians"]) {
			name := d.optionalText(row["display_name"])
			if name == "" {
				name = d.text(row["username"])
			}
			p.Guardians = append(p.Guardians, name)
		}
	case "venues":
		p.Title = tr("Check your venues")
		for _, row := range d.rows(data["venues"]) {
			place := d.row(row["place"])
			item := collectionsVenue{Name: d.text(place["display_name"]), URL: routeURL("place_detail", d.id(place["pk"]))}
			if item.Name == "" {
				item.Name = tr("Place")
			}
			if row["flags"] != nil {
				for _, flag := range d.strings(row["flags"]) {
					switch flag {
					case "closed":
						item.Flags = append(item.Flags, collectionsFlag{tr("reported closed"), true})
					case "hours_unverified":
						item.Flags = append(item.Flags, collectionsFlag{tr("posted hours unverified"), false})
					case "correction_pending":
						item.Flags = append(item.Flags, collectionsFlag{tr("a correction is pending"), false})
					}
				}
			}
			p.Venues = append(p.Venues, item)
		}
	}
	if d.err != nil {
		return collectionsPage{}, errCollectionsSnapshot
	}
	return p, nil
}

func (r *Renderer) renderCollections(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	p, err := r.collectionsPage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadCollectionsTemplates()
	if err != nil {
		return nil, err
	}
	return executeCollectionsTemplate(parsed, p)
}

func executeCollectionsTemplate(parsed *template.Template, p collectionsPage) ([]byte, error) {
	var out bytes.Buffer
	if err := parsed.ExecuteTemplate(&out, "collections", p); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
