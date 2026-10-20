package web

import (
	"bytes"
	"embed"
	"errors"
	"html"
	"html/template"
	"net/http"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Presentation only. topicPage and SetTopics retain the active self, taxonomy,
// ordering, CSRF and persistence boundaries. No service or query is added.
type topicsPage struct {
	Shell                         familyShell
	Copy                          map[string]string
	Nonce, PublicID, LanguageNext string
	Avatar                        template.URL
	AccountLinks                  []familyLink
	Choices                       []topicsChoice
	Messages                      []accountCenterMessage
	Unread                        int64
	HasChosen                     bool
}
type topicsChoice struct {
	Slug, Name, Description string
	Checked                 bool
}

//go:embed pages/topics.gohtml
var topicsPages embed.FS
var errTopicsSnapshot = errors.New("invalid native topics snapshot")
var loadTopicsTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("topics").ParseFS(topicsPages, "pages/topics.gohtml")
})

func (r *Renderer) topicsPage(request *http.Request, data pongo2.Context) (topicsPage, error) {
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated || actor.ID < 1 || !actor.IsActive {
		return topicsPage{}, platform.ErrForbidden
	}
	d := groupDetailDecoder{}
	p := topicsPage{Copy: map[string]string{}, PublicID: actor.PublicID}
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
	// This producer uses Renderer defaults, not AccountView's richer user row.
	// Never require or manufacture its id/pk/is_active/guardian keys here.
	user := d.row(data["user"])
	if !d.boolean(user["is_authenticated"]) || d.text(user["public_id"]) != actor.PublicID || d.text(user["username"]) != actor.Username || d.text(user["display_name"]) != actor.DisplayName {
		d.fail()
	}
	if p.Shell.DisplayName == "" {
		p.Shell.DisplayName = actor.Username
	}
	p.Avatar = d.person(map[string]any{"id": actor.ID, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": user["avatar_uri"]}, actor.ID, false).Avatar
	if value, exists := data["unread_notifications"]; exists && value != nil {
		p.Unread = d.integer(value)
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
	for _, message := range []string{"Your topics", "Topics to lean toward", "No topics are available yet.", "Save topics", "see your feed", "Activities", "Home", "Browse", "Organize", "Profile", "Menu", "Primary", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language",
		"Pick the kinds of activity you'd like your suggestions to lean toward. This is your hand on the algorithm &mdash; a setting you choose, never guessed from what you click.",
		"It only nudges the order and adds an honest &ldquo;matches your chosen topics&rdquo; note. It never hides anything: you'll still see everything happening for your age group, whether or not you pick any topics."} {
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
	chosen, ok := data["chosen"].(map[string]bool)
	if !ok {
		d.fail()
	}
	// The frozen template tests membership, not the bool value. It also shows
	// the feed link for any nonempty chosen map, independently of visible rows.
	p.HasChosen = len(chosen) != 0
	for _, row := range d.rows(data["categories"]) {
		slug := d.text(row["slug"])
		_, checked := chosen[slug]
		p.Choices = append(p.Choices, topicsChoice{Slug: slug, Name: d.text(row["name"]), Description: d.optionalText(row["description"]), Checked: checked})
	}
	if value, exists := data["messages"]; exists && value != nil {
		messages, ok := value.([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, message := range messages {
				p.Messages = append(p.Messages, accountCenterMessage{Text: d.text(message["message"]), Tags: d.text(message["tags"])})
			}
		}
	}
	if d.err != nil {
		return topicsPage{}, errTopicsSnapshot
	}
	return p, nil
}

func (r *Renderer) renderTopics(request *http.Request, data pongo2.Context) ([]byte, error) {
	p, err := r.topicsPage(request, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadTopicsTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(8192)
	if err = parsed.ExecuteTemplate(&output, "topics", p); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
