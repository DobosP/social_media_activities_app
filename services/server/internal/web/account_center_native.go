package web

import (
	"bytes"
	"embed"
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

// AccountView is the producer. This projection never calls Self, reads SQL,
// changes token/session state, or interprets a guardian flag as authority.
type accountCenterPage struct {
	Shell                                familyShell
	Copy                                 map[string]string
	Settings, HasToken                   bool
	Nonce, PublicID, LanguageNext, Title string
	SignedBefore, SignedAfter, TokenText string
	Avatar                               template.URL
	AccountLinks                         []familyLink
	Groups                               []accountCenterGroup
	Messages                             []accountCenterMessage
}
type accountCenterGroup struct {
	Heading string
	Links   []familyLink
}
type accountCenterMessage struct{ Text, Tags string }

//go:embed pages/account-center.gohtml
var accountCenterPages embed.FS
var errAccountCenterSnapshot = errors.New("invalid native account center snapshot")
var loadAccountCenterTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("account-center").ParseFS(accountCenterPages, "pages/account-center.gohtml")
})

func accountCenterCanonical(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && raw != "" && parsed.User == nil && !strings.ContainsAny(raw, "\r\n\\") &&
		(strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") || parsed.Scheme == "https" || parsed.Scheme == "http")
}

func (r *Renderer) accountCenterPage(request *http.Request, name string, data pongo2.Context) (accountCenterPage, error) {
	if name != "web/you.html" && name != "web/settings.html" {
		return accountCenterPage{}, errAccountCenterSnapshot
	}
	actor, admitted := platform.ActorFrom(request)
	if !admitted {
		return accountCenterPage{}, platform.ErrForbidden
	}
	d := familyDecoder{}
	p := accountCenterPage{Settings: name == "web/settings.html", Copy: map[string]string{}, PublicID: actor.PublicID}
	user := d.row(data["user"])
	if d.integer(user["id"]) != actor.ID || d.integer(user["pk"]) != actor.ID || d.text(user["public_id"]) != actor.PublicID ||
		!d.boolean(user["is_active"]) || !d.boolean(user["is_authenticated"]) ||
		d.text(user["username"]) != actor.Username || d.text(user["display_name"]) != actor.DisplayName {
		d.fail()
	}
	guardian, hasGuardians, connections := d.boolean(data["is_guardian"]), d.boolean(data["has_guardians"]), d.boolean(data["connections_enabled"])
	if d.boolean(user["is_guardian"]) != guardian {
		d.fail()
	}
	context := d.row(data["request"])
	p.Nonce, p.LanguageNext = d.text(context["csp_nonce"]), d.text(context["path"])
	p.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName, Canonical: d.text(data["canonical_url"])}
	p.Shell.English = p.Shell.Language == "en"
	if p.Shell.DisplayName == "" {
		p.Shell.DisplayName = actor.Username
	}
	if (p.Shell.Language != "en" && p.Shell.Language != "ro") || p.Shell.SiteName == "" || p.Shell.CSRF == "" || p.Nonce == "" ||
		p.LanguageNext != request.URL.Path || !accountCenterCanonical(p.Shell.Canonical) {
		d.fail()
	}
	if p.Shell.Theme != "auto" && p.Shell.Theme != "light" && p.Shell.Theme != "dark" && p.Shell.Theme != "contrast" {
		d.fail()
	}
	if p.Shell.Text != "normal" && p.Shell.Text != "large" && p.Shell.Text != "larger" {
		d.fail()
	}
	if p.Shell.Motion != "auto" && p.Shell.Motion != "reduce" && p.Shell.Motion != "full" {
		d.fail()
	}
	// Reuse the existing generated-avatar image URI boundary. On the direct
	// revoke-error path the source map has no avatar; do not query or invent one.
	personDecoder := groupDetailDecoder{}
	p.Avatar = personDecoder.person(user, actor.ID, false).Avatar
	if personDecoder.err != nil {
		d.fail()
	}
	tr := func(message string) string {
		if value, found := p.Copy[message]; found {
			return value
		}
		value := html.UnescapeString(r.catalog.translate(p.Shell.Language, message, 1))
		p.Copy[message] = value
		return value
	}
	for _, text := range []string{"Activities", "Menu", "Primary", "Account", "Account overview", "Settings", "Profile", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language", "Save", "Choose the language this site is shown in.", "API access", "No device currently holds API access to your account.", "Revoke API access", "Your account", "Your data belongs to you. Take a copy any time, or delete the account and the personal data that goes with it.", "Download my data", "Delete my account"} {
		tr(text)
	}
	p.Title = tr("Account & settings")
	if p.Settings {
		p.Title = tr("Settings")
	}
	link := func(label, route string) familyLink { return familyLink{Label: tr(label), URL: routeURL(route)} }
	for _, item := range [][2]string{{"Home", "home"}, {"Browse", "activity_list"}, {"Organize", "activity_create"}, {"Places", "places_map"}, {"Events", "events_list"}, {"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}, {"Civic partners", "partners"}, {"Notifications", "notifications"}, {"Messages", "messages"}} {
		p.Shell.Navigation = append(p.Shell.Navigation, link(item[0], item[1]))
	}
	for _, item := range [][2]string{{"Profile", "profile"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Account overview", "you"}, {"Settings", "settings"}, {"Support", "donate"}} {
		p.AccountLinks = append(p.AccountLinks, link(item[0], item[1]))
	}
	if connections {
		p.AccountLinks = append(p.AccountLinks[:1], append([]familyLink{link("Connections", "connections")}, p.AccountLinks[1:]...)...)
	}
	for _, item := range [][2]string{{"Overview", "you"}, {"Profile", "profile"}, {"Interests", "interests"}, {"Display", "display_preferences"}, {"Privacy &amp; data", "my_privacy"}, {"Donations", "my_donations"}} {
		v := link(item[0], item[1])
		v.Current = !p.Settings && item[1] == "you"
		p.Shell.AccountTabs = append(p.Shell.AccountTabs, v)
	}
	if hasGuardians {
		p.Shell.AccountTabs = append(p.Shell.AccountTabs, link("Guardians", "my_guardians"))
	}
	v := link("Settings", "settings")
	v.Current = p.Settings
	p.Shell.AccountTabs = append(p.Shell.AccountTabs, v)
	addGroup := func(heading string, entries [][2]string) {
		group := accountCenterGroup{Heading: tr(heading)}
		for _, entry := range entries {
			group.Links = append(group.Links, link(entry[0], entry[1]))
		}
		p.Groups = append(p.Groups, group)
	}
	profile := [][2]string{{"Your profile", "profile"}, {"Interests", "interests"}, {"Your topics (tune suggestions)", "topic_preferences"}, {"Access needs", "access_preferences"}, {"Display (theme, text size, motion)", "display_preferences"}, {"Age verification", "verify_age"}, {"Notifications", "notifications"}, {"Messages", "messages"}}
	if connections {
		profile = append(profile, [2]string{"Connections", "connections"})
	}
	profile = append(profile, [2]string{"Saved searches", "saved_searches"}, [2]string{"Notification settings", "notification_preferences"})
	if p.Settings {
		profile = [][2]string{{"Display (theme, text size, motion)", "display_preferences"}, {"Notifications", "notification_preferences"}, {"Access needs", "access_preferences"}, {"Interests", "interests"}, {"Your topics (tune suggestions)", "topic_preferences"}, {"Saved searches", "saved_searches"}}
	}
	heading := "Profile &amp; preferences"
	if p.Settings {
		heading = "Preferences"
	}
	addGroup(heading, profile)
	privacy := [][2]string{{"Privacy &amp; your data", "my_privacy"}, {"Your safety record", "safety_record"}, {"Your activity log", "activity_log"}}
	if p.Settings {
		privacy = [][2]string{{"Privacy &amp; your data", "my_privacy"}, {"Age verification", "verify_age"}, {"Your safety record", "safety_record"}, {"Your activity log", "activity_log"}}
	}
	if guardian {
		privacy = append(privacy, [2]string{"Wards you look after", "wards"})
	}
	if hasGuardians {
		privacy = append(privacy, [2]string{"Your guardians", "my_guardians"})
	}
	addGroup("Privacy &amp; safety", privacy)
	giving := [][2]string{{"My donations", "my_donations"}, {"Support this platform", "donate"}, {"Campaigns", "campaigns"}, {"Where the money goes", "transparency"}}
	if p.Settings {
		giving = [][2]string{{"My donations", "my_donations"}, {"Where the money goes", "transparency"}}
	}
	addGroup("Giving", giving)
	if !p.Settings {
		before, after, found := strings.Cut(tr("Signed in as <strong>%(name)s</strong>."), "<strong>%(name)s</strong>")
		if !found || strings.Contains(after, "%(name)s") {
			d.fail()
		}
		p.SignedBefore, p.SignedAfter = before, after
	} else {
		if d.text(data["current_language"]) != p.Shell.Language {
			d.fail()
		}
		options, ok := data["languages"].([]any)
		if !ok || len(options) != 2 {
			d.fail()
		} else {
			for index, expected := range [][2]string{{"en", "English"}, {"ro", "Română"}} {
				row, ok := options[index].([]string)
				if !ok || len(row) != 2 || row[0] != expected[0] || row[1] != expected[1] {
					d.fail()
				}
			}
		}
		if value, exists := data["api_token_created"]; exists && value != nil {
			created, ok := value.(time.Time)
			if !ok {
				d.fail()
			} else {
				p.HasToken = true // Record presence only; do not assert current validity.
				p.TokenText = strings.ReplaceAll(tr("A device (e.g. the mobile app) holds API access to your account, issued %(when)s. Revoke it if you lost the device or no longer use it."), "%(when)s", djangoDate(created, "j M Y"))
			}
		}
	}
	if value, exists := data["messages"]; exists && value != nil {
		messages, ok := value.([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, m := range messages {
				p.Messages = append(p.Messages, accountCenterMessage{Text: m["message"], Tags: m["tags"]})
			}
		}
	}
	for _, item := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}, {"Settings", "settings"}} {
		p.Shell.Footer = append(p.Shell.Footer, link(item[0], item[1]))
	}
	if d.err != nil {
		return accountCenterPage{}, errAccountCenterSnapshot
	}
	return p, nil
}

func (r *Renderer) renderAccountCenter(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	page, err := r.accountCenterPage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadAccountCenterTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(12288)
	if err = parsed.ExecuteTemplate(&output, "account-center", page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
