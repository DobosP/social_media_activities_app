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

// Presentation only. notificationPage supplies the current API's authorized,
// ordered display records. No recipient ID, mutation decision or raw payload is
// retained. Mark-all still uses the existing native action/service unchanged.
type notificationInboxPage struct {
	Shell           familyShell
	Copy            map[string]string
	Rows            []notificationInboxRow
	Nonce, PublicID string
	AccountLinks    []familyLink
}
type notificationInboxRow struct {
	Title, Body, Why, When, ISOTime, URL string
	Unread                               bool
}

// One render owns this formatter. Resolve the same source timezone lazily once,
// without changing the generic date helper or caching failure across requests.
type notificationInboxDates struct {
	loadLocation func(string) (*time.Location, error)
	attempted    bool
	zone         *time.Location
}

func (dates *notificationInboxDates) format(stamp time.Time) (string, string) {
	if stamp.IsZero() {
		return "", ""
	}
	if !dates.attempted {
		dates.attempted = true
		load := dates.loadLocation
		if load == nil {
			load = time.LoadLocation
		}
		zone, err := load("Europe/Bucharest")
		dates.zone = nil
		if err == nil {
			dates.zone = zone
		}
	}
	if dates.zone != nil {
		stamp = stamp.In(dates.zone)
	}
	// These are precisely the frozen j M H:i and c token expansions. A failed
	// timezone load keeps each individual stamp's original location/offset.
	return stamp.Format("2 Jan 15:04"), stamp.Format(time.RFC3339)
}

//go:embed pages/notification-inbox.gohtml
var notificationInboxPages embed.FS
var errNotificationInboxSnapshot = errors.New("invalid native notification inbox snapshot")
var loadNotificationInboxTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("notification-inbox").ParseFS(notificationInboxPages, "pages/notification-inbox.gohtml")
})

// Exact policy from the frozen template's safe_href filter. The separately
// preserved SPA helper has a different contract and is not used here.
func notificationInboxSafeHref(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || strings.ContainsAny(raw, "\r\n\\") || !((strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//")) || parsed.Scheme == "http" || parsed.Scheme == "https") {
		return ""
	}
	return raw
}

func (r *Renderer) notificationInboxPage(request *http.Request, data pongo2.Context) (notificationInboxPage, error) {
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated {
		return notificationInboxPage{}, platform.ErrForbidden
	}
	d := familyDecoder{}
	page := notificationInboxPage{Copy: map[string]string{}}
	page.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName, Canonical: "/notifications/"}
	page.Shell.English = page.Shell.Language == "en"
	if page.Shell.Language != "en" && page.Shell.Language != "ro" || page.Shell.SiteName == "" || page.Shell.CSRF == "" {
		d.fail()
	}
	if page.Shell.Theme != "auto" && page.Shell.Theme != "light" && page.Shell.Theme != "dark" && page.Shell.Theme != "contrast" || page.Shell.Text != "normal" && page.Shell.Text != "large" && page.Shell.Text != "larger" || page.Shell.Motion != "auto" && page.Shell.Motion != "reduce" && page.Shell.Motion != "full" {
		d.fail()
	}
	if page.Shell.DisplayName == "" {
		page.Shell.DisplayName = actor.Username
	}
	// notificationPage is not AccountView. Its Renderer user defaults have no
	// id/is_active fields; current authority is the already admitted request Actor.
	page.Nonce = d.text(d.row(data["request"])["csp_nonce"])
	if page.Nonce == "" {
		d.fail()
	}
	page.PublicID = actor.PublicID
	translate := func(message string) string {
		if value, found := page.Copy[message]; found {
			return value
		}
		value := html.UnescapeString(r.catalog.translate(page.Shell.Language, message, 1))
		page.Copy[message] = value
		return value
	}
	for _, message := range []string{"Notifications", "Notification settings", "Mark all read", "new", "view", "No notifications yet.", "Why you got this: %(why)s", "Activities", "Menu", "Primary", "Settings", "Profile", "Account overview", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language"} {
		translate(message)
	}
	for _, link := range [][2]string{{"Home", "home"}, {"Browse", "activity_list"}, {"Organize", "activity_create"}, {"Places", "places_map"}, {"Events", "events_list"}, {"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}, {"Civic partners", "partners"}, {"Notifications", "notifications"}, {"Messages", "messages"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Support", "donate"}, {"Settings", "settings"}} {
		page.Shell.Navigation = append(page.Shell.Navigation, familyLink{Label: translate(link[0]), URL: routeURL(link[1]), Current: link[1] == "notifications"})
	}
	for _, link := range [][2]string{{"Profile", "profile"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Account overview", "you"}, {"Settings", "settings"}, {"Support", "donate"}} {
		page.AccountLinks = append(page.AccountLinks, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	// No connections_enabled decision is invented: this producer does not
	// supply it. Keep the same conditional behavior if current defaults do.
	if enabled, exists := data["connections_enabled"]; exists {
		if d.boolean(enabled) {
			link := familyLink{Label: translate("Connections"), URL: routeURL("connections")}
			page.Shell.Navigation = append(page.Shell.Navigation, link)
			page.AccountLinks = append(page.AccountLinks, link)
		}
	}
	for _, link := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}, {"Settings", "settings"}} {
		page.Shell.Footer = append(page.Shell.Footer, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	rows := d.rows(data["items"])
	if len(rows) > 100 {
		return notificationInboxPage{}, errNotificationInboxSnapshot
	}
	var dates notificationInboxDates
	for _, row := range rows {
		item := notificationInboxRow{Title: d.text(row["title"]), Body: d.text(row["body"])}
		why, href := d.text(row["why"]), d.text(row["url"])
		if why != "" {
			item.Why = strings.ReplaceAll(translate("Why you got this: %(why)s"), "%(why)s", why)
		}
		item.URL = notificationInboxSafeHref(href)
		item.Unread = row["read_at"] == nil
		if !item.Unread && !d.boolean(row["read_at"]) {
			d.fail()
		}
		// The current API emits RFC3339 timestamp text; direct renderer fixtures
		// may supply the equivalent time.Time. Do not reinterpret a date-only or
		// arbitrary display string using the more permissive SPA date parser.
		var stamp time.Time
		switch value := row["created_at"].(type) {
		case time.Time:
			stamp = value
		case string:
			var err error
			stamp, err = time.Parse(time.RFC3339Nano, value)
			if err != nil {
				d.fail()
			}
		default:
			d.fail()
		}
		if stamp.IsZero() {
			d.fail()
		}
		item.When, item.ISOTime = dates.format(stamp)
		page.Rows = append(page.Rows, item)
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
		return notificationInboxPage{}, errNotificationInboxSnapshot
	}
	return page, nil
}

func (r *Renderer) renderNotificationInbox(request *http.Request, data pongo2.Context) ([]byte, error) {
	page, err := r.notificationInboxPage(request, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadNotificationInboxTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(8192 + len(page.Rows)*512)
	if err = parsed.ExecuteTemplate(&output, "notification-inbox", page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
