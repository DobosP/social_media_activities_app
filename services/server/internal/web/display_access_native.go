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

// Presentation only. AccountView/AccountAction keep their existing public
// cookie preference and self-only settings boundaries. No service is called.
type displayAccessPage struct {
	Shell                                       familyShell
	Copy                                        map[string]string
	Display, Authenticated                      bool
	Title, Intro, Nonce, PublicID, LanguageNext string
	Avatar                                      template.URL
	AccountLinks                                []familyLink
	Groups                                      []displayAccessGroup
	Access                                      []displayAccessChoice
	Messages                                    []accountCenterMessage
}
type displayAccessGroup struct {
	Name, Legend string
	Options      []familyOption
}
type displayAccessChoice struct {
	Name, Label, Detail string
	Checked             bool
}

//go:embed pages/display-access.gohtml
var displayAccessPages embed.FS
var errDisplayAccessSnapshot = errors.New("invalid native display/access snapshot")
var loadDisplayAccessTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("display-access").ParseFS(displayAccessPages, "pages/display-access.gohtml")
})

func (r *Renderer) displayAccessPage(request *http.Request, name string, data pongo2.Context) (displayAccessPage, error) {
	if name != "web/display_preferences.html" && name != "web/access_preferences.html" {
		return displayAccessPage{}, errDisplayAccessSnapshot
	}
	actor, authenticated := platform.ActorFrom(request)
	p := displayAccessPage{Display: name == "web/display_preferences.html", Authenticated: authenticated, Copy: map[string]string{}}
	if !p.Display && !authenticated {
		return displayAccessPage{}, platform.ErrForbidden
	}
	d := groupDetailDecoder{}
	context := d.row(data["request"])
	p.Nonce, p.LanguageNext = d.text(context["csp_nonce"]), d.text(context["path"])
	p.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), Canonical: d.text(data["canonical_url"]), CSRF: d.optionalText(data["csrf"])}
	p.Shell.English = p.Shell.Language == "en"
	// The existing public SPA _data display fallback can have an empty CSRF
	// value. Rendering does not issue or authorize a POST; the action still
	// applies the unchanged CSRF/Origin admission to every caller.
	if (p.Shell.Language != "en" && p.Shell.Language != "ro") || p.Shell.SiteName == "" || p.Nonce == "" ||
		p.LanguageNext != request.URL.Path || !accountCenterCanonical(p.Shell.Canonical) {
		d.fail()
	}
	if p.Shell.Theme != "auto" && p.Shell.Theme != "light" && p.Shell.Theme != "dark" && p.Shell.Theme != "contrast" ||
		p.Shell.Text != "normal" && p.Shell.Text != "large" && p.Shell.Text != "larger" ||
		p.Shell.Motion != "auto" && p.Shell.Motion != "reduce" && p.Shell.Motion != "full" {
		d.fail()
	}
	user := d.row(data["user"])
	if d.boolean(user["is_authenticated"]) != authenticated {
		d.fail()
	}
	connections, hasGuardians := d.optionalBool(data["connections_enabled"]), d.optionalBool(data["has_guardians"])
	if authenticated {
		if d.text(user["public_id"]) != actor.PublicID || d.text(user["username"]) != actor.Username || d.text(user["display_name"]) != actor.DisplayName {
			d.fail()
		}
		if !p.Display && (d.integer(user["id"]) != actor.ID || d.integer(user["pk"]) != actor.ID || !d.boolean(user["is_active"])) {
			d.fail()
		}
		p.Shell.DisplayName, p.PublicID = actor.DisplayName, actor.PublicID
		if p.Shell.DisplayName == "" {
			p.Shell.DisplayName = actor.Username
		}
		// Public display uses Renderer's default shell, which has no id/pk or
		// AccountView guardian keys. Reuse only the image URI boundary with
		// authoritative Actor identity; do not manufacture richer user data.
		p.Avatar = d.person(map[string]any{"id": actor.ID, "public_id": actor.PublicID, "username": actor.Username, "display_name": actor.DisplayName, "avatar_uri": user["avatar_uri"]}, actor.ID, false).Avatar
	}
	tr := func(message string) string {
		if text, exists := p.Copy[message]; exists {
			return text
		}
		text := html.UnescapeString(r.catalog.translate(p.Shell.Language, message, 1))
		p.Copy[message] = text
		return text
	}
	for _, message := range []string{"Activities", "Home", "Browse", "Organize", "Profile", "Menu", "Primary", "Account", "Log in", "Sign up", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language", "Save display settings", "Save preferences"} {
		tr(message)
	}
	link := func(label, route string) familyLink { return familyLink{Label: tr(label), URL: routeURL(route)} }
	if authenticated {
		p.Shell.Navigation = append(p.Shell.Navigation, link("Browse", "activity_list"), link("Organize", "activity_create"))
	}
	for _, item := range [][2]string{{"Places", "places_map"}, {"Events", "events_list"}} {
		p.Shell.Navigation = append(p.Shell.Navigation, link(item[0], item[1]))
	}
	if authenticated {
		p.Shell.Navigation = append(p.Shell.Navigation, link("Communities &amp; groups", "communities"), link("Gauge interest", "gauges"))
	}
	p.Shell.Navigation = append(p.Shell.Navigation, link("Civic partners", "partners"))
	if authenticated {
		p.Shell.Navigation = append(p.Shell.Navigation, link("Notifications", "notifications"), link("Messages", "messages"))
		p.AccountLinks = append(p.AccountLinks, link("Profile", "profile"))
		if connections {
			p.AccountLinks = append(p.AccountLinks, link("Connections", "connections"))
		}
		for _, item := range [][2]string{{"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Account overview", "you"}, {"Settings", "settings"}, {"Support", "donate"}} {
			p.AccountLinks = append(p.AccountLinks, link(item[0], item[1]))
		}
		if p.Display {
			for _, item := range [][2]string{{"Overview", "you"}, {"Profile", "profile"}, {"Interests", "interests"}, {"Display", "display_preferences"}, {"Privacy &amp; data", "my_privacy"}, {"Donations", "my_donations"}} {
				v := link(item[0], item[1])
				v.Current = item[1] == "display_preferences"
				p.Shell.AccountTabs = append(p.Shell.AccountTabs, v)
			}
			if hasGuardians {
				p.Shell.AccountTabs = append(p.Shell.AccountTabs, link("Guardians", "my_guardians"))
			}
			p.Shell.AccountTabs = append(p.Shell.AccountTabs, link("Settings", "settings"))
		}
	}
	if p.Display {
		p.Title = tr("Display settings")
		p.Intro = tr("Choose how the site looks. Your choice is saved on this device only (a small functional cookie) and applies whether or not you're signed in. By default we follow your device's own light/dark and reduced-motion settings.")
		for _, group := range []struct {
			name, legend, selected string
			options                [][2]string
		}{
			{"display_theme", "Theme", p.Shell.Theme, [][2]string{{"auto", "Match my device"}, {"light", "Light"}, {"dark", "Dark"}, {"contrast", "High contrast"}}},
			{"display_text", "Text size", p.Shell.Text, [][2]string{{"normal", "Normal"}, {"large", "Large"}, {"larger", "Larger"}}},
			{"display_motion", "Motion", p.Shell.Motion, [][2]string{{"auto", "Match my device"}, {"reduce", "Reduce motion"}, {"full", "Full motion"}}},
		} {
			out := displayAccessGroup{Name: group.name, Legend: tr(group.legend)}
			for _, option := range group.options {
				out.Options = append(out.Options, familyOption{Value: option[0], Label: tr(option[1]), Selected: option[0] == group.selected})
			}
			p.Groups = append(p.Groups, out)
		}
	} else {
		p.Title = tr("Access preferences")
		p.Intro = tr("A setting you choose &mdash; we never track or infer it. We only ever show what a venue's open data records, and places with unknown accessibility are still shown to you.")
		pref := d.row(data["pref"])
		for _, field := range [][2]string{{"needs_step_free", "I need step-free access"}, {"needs_accessible_toilet", "I need an accessible toilet"}, {"needs_hearing_loop", "I need a hearing loop"}, {"prefers_quiet", "I prefer quiet / sensory-friendly places"}} {
			choice := displayAccessChoice{Name: field[0], Label: tr(field[1]), Checked: d.boolean(pref[field[0]])}
			if field[0] == "prefers_quiet" {
				choice.Detail = tr("Saved for the future &mdash; venue data for this isn't available yet, so it doesn't change what you see.")
			}
			p.Access = append(p.Access, choice)
		}
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
	for _, item := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}} {
		p.Shell.Footer = append(p.Shell.Footer, link(item[0], item[1]))
	}
	if authenticated {
		p.Shell.Footer = append(p.Shell.Footer, link("Settings", "settings"))
	}
	if d.err != nil {
		return displayAccessPage{}, errDisplayAccessSnapshot
	}
	return p, nil
}

func (r *Renderer) renderDisplayAccess(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	p, err := r.displayAccessPage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadDisplayAccessTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(8192)
	if err = parsed.ExecuteTemplate(&output, "display-access", p); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
