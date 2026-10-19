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

// Presentation only: current AccountView supplies these already-authorized
// ordered display rows. The existing account service alone filters/saves mutes.
type notificationPreferencesPage struct {
	Shell                                  familyShell
	Copy                                   map[string]string
	Rows                                   []notificationPreferenceChoice
	Nonce, PublicID, Intro, ArrivalWarning string
	AccountLinks                           []familyLink
}
type notificationPreferenceChoice struct {
	Kind, Label, Reason string
	Muted, Arrival      bool
}

const notificationPreferencesIntro = "Tick a category to stop receiving those in-app notifications. We never send ads or\n  track you. Important safety and moderation notices are always delivered and can't be turned off."
const notificationPreferencesArrival = "Note: muting this also turns off arrival\n          alerts for a child you supervise."

//go:embed pages/notification-preferences.gohtml
var notificationPreferencesPages embed.FS
var errNotificationPreferencesSnapshot = errors.New("invalid native notification preferences snapshot")
var loadNotificationPreferencesTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("notification-preferences").ParseFS(notificationPreferencesPages, "pages/notification-preferences.gohtml")
})

func (r *Renderer) notificationPreferencesPage(request *http.Request, data pongo2.Context) (notificationPreferencesPage, error) {
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated || actor.ID < 1 || !actor.IsActive {
		return notificationPreferencesPage{}, platform.ErrForbidden
	}
	d := familyDecoder{}
	page := notificationPreferencesPage{Copy: map[string]string{}}
	page.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName, Canonical: "/notifications/preferences/"}
	page.Shell.English = page.Shell.Language == "en"
	if page.Shell.Language != "en" && page.Shell.Language != "ro" || page.Shell.CSRF == "" || page.Shell.SiteName == "" {
		d.fail()
	}
	if page.Shell.Theme != "auto" && page.Shell.Theme != "light" && page.Shell.Theme != "dark" && page.Shell.Theme != "contrast" || page.Shell.Text != "normal" && page.Shell.Text != "large" && page.Shell.Text != "larger" || page.Shell.Motion != "auto" && page.Shell.Motion != "reduce" && page.Shell.Motion != "full" {
		d.fail()
	}
	if page.Shell.DisplayName == "" {
		page.Shell.DisplayName = actor.Username
	}
	user := d.row(data["user"])
	if d.integer(user["id"]) != actor.ID || !d.boolean(user["is_authenticated"]) || !d.boolean(user["is_active"]) {
		d.fail()
	}
	page.Nonce = d.text(d.row(data["request"])["csp_nonce"])
	if page.Nonce == "" {
		d.fail()
	}
	page.PublicID = actor.PublicID
	// Only immutable catalogue strings are shared. Memoized plain copy belongs
	// to this request; no actor/preference/private state crosses requests.
	translate := func(message string) string {
		if value, found := page.Copy[message]; found {
			return value
		}
		value := html.UnescapeString(r.catalog.translate(page.Shell.Language, message, 1))
		page.Copy[message] = value
		return value
	}
	for _, message := range []string{"Notification settings", "Notifications", "Save settings", "Activities", "Menu", "Primary", "Settings", "Profile", "Account overview", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language"} {
		translate(message)
	}
	page.Intro, page.ArrivalWarning = translate(notificationPreferencesIntro), translate(notificationPreferencesArrival)
	for _, link := range [][2]string{{"Home", "home"}, {"Browse", "activity_list"}, {"Organize", "activity_create"}, {"Places", "places_map"}, {"Events", "events_list"}, {"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}, {"Civic partners", "partners"}, {"Notifications", "notifications"}, {"Messages", "messages"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Support", "donate"}, {"Settings", "settings"}} {
		page.Shell.Navigation = append(page.Shell.Navigation, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	for _, link := range [][2]string{{"Profile", "profile"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Account overview", "you"}, {"Settings", "settings"}, {"Support", "donate"}} {
		page.AccountLinks = append(page.AccountLinks, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	if d.boolean(data["connections_enabled"]) {
		link := familyLink{Label: translate("Connections"), URL: routeURL("connections")}
		page.Shell.Navigation = append(page.Shell.Navigation, link)
		page.AccountLinks = append(page.AccountLinks, link)
	}
	for _, link := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}, {"Settings", "settings"}} {
		page.Shell.Footer = append(page.Shell.Footer, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	rows := d.rows(data["rows"])
	if len(rows) != len(accountNotificationOrder) {
		d.fail()
	}
	for i, row := range rows {
		kind, label, reason, muted := d.text(row["value"]), d.text(row["label"]), d.text(row["reason"]), d.boolean(row["muted"])
		if i >= len(accountNotificationOrder) || kind != accountNotificationOrder[i] || label == "" || reason == "" {
			d.fail()
			continue
		}
		// Labels/reasons remain escaped text. A moderator-specific option does
		// not imply the current user has a moderator or guardian capability.
		label = strings.ReplaceAll(translate("Mute: %(label)s"), "%(label)s", label)
		page.Rows = append(page.Rows, notificationPreferenceChoice{Kind: kind, Label: label, Reason: reason, Muted: muted, Arrival: kind == "arrival"})
	}
	if messages, exists := data["messages"]; exists && messages != nil {
		values, ok := messages.([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, item := range values {
				message := item["message"]
				matched := false
				for _, allowed := range []string{"That action isn't available for your account.", "Check the form and try again.", "This action is temporarily unavailable. Please try again.", mediaBusyMessage} {
					if message == allowed || message == r.catalog.translate(page.Shell.Language, allowed, 1) {
						page.Shell.Messages = append(page.Shell.Messages, translate(allowed))
						matched = true
						break
					}
				}
				if !matched {
					page.Shell.Messages = append(page.Shell.Messages, translate("This action is temporarily unavailable. Please try again."))
				}
			}
		}
	}
	if d.err != nil {
		return notificationPreferencesPage{}, errNotificationPreferencesSnapshot
	}
	return page, nil
}

func (r *Renderer) renderNotificationPreferences(request *http.Request, data pongo2.Context) ([]byte, error) {
	page, err := r.notificationPreferencesPage(request, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadNotificationPreferencesTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(12288 + len(page.Rows)*512)
	if err = parsed.ExecuteTemplate(&output, "notification-preferences", page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
