package web

import (
	"bytes"
	"embed"
	"errors"
	"html"
	"html/template"
	"net/http"
	"strconv"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// The existing form builder supplies these values from its existing query and
// binding loop. Widgets, safe HTML and arbitrary form maps are never parsed.
type groupCreateForm struct {
	Staff  bool
	Fields []groupCreateInput
}
type groupCreateInput struct {
	Name, Value string
	Options     []option
}
type groupCreateField struct {
	Name, Label, Value, Help, Error string
	Required, Select, Textarea      bool
	MaxLength                       int
	Options                         []familyOption
}
type groupCreatePage struct {
	// Shared layout values carry no guardian capabilities or private row data.
	Shell        familyShell
	Copy         map[string]string
	Fields       []groupCreateField
	ErrorSummary string
}

//go:embed pages/group-create.gohtml
var groupCreatePages embed.FS

var errGroupCreateSnapshot = errors.New("invalid native group form snapshot")

var loadGroupCreateTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("group-create").ParseFS(groupCreatePages, "pages/group-create.gohtml")
})

func (r *Renderer) groupCreatePage(request *http.Request, data pongo2.Context) (groupCreatePage, error) {
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated {
		return groupCreatePage{}, platform.ErrForbidden
	}
	invalid := errGroupCreateSnapshot
	form, ok := data["form"].(map[string]any)
	if !ok {
		return groupCreatePage{}, invalid
	}
	snapshot, ok := form["native_group"].(groupCreateForm)
	fieldCount := 4
	if actor.IsStaff {
		fieldCount++
	}
	if !ok || snapshot.Staff != actor.IsStaff || len(snapshot.Fields) != fieldCount {
		return groupCreatePage{}, invalid
	}
	page := groupCreatePage{Copy: map[string]string{}}
	text := func(key string) (string, bool) { value, ok := data[key].(string); return value, ok }
	lang, langOK := text("language")
	theme, themeOK := text("display_theme")
	size, sizeOK := text("display_text")
	motion, motionOK := text("display_motion")
	site, siteOK := text("site_name")
	csrf, csrfOK := text("csrf")
	if !langOK || (lang != "en" && lang != "ro") || !themeOK || !sizeOK || !motionOK || !siteOK || !csrfOK || csrf == "" {
		return groupCreatePage{}, invalid
	}
	if theme != "auto" && theme != "light" && theme != "dark" && theme != "contrast" || size != "normal" && size != "large" && size != "larger" || motion != "auto" && motion != "reduce" && motion != "full" {
		return groupCreatePage{}, invalid
	}
	translate := func(message string) string {
		if value, found := page.Copy[message]; found {
			return value
		}
		value := r.groupCreateCopy(lang, message)
		page.Copy[message] = value
		return value
	}
	for _, message := range []string{"Create a group", "Create group", "A standing group for a city + activity type, visible to people your age.", "Communities &amp; groups", "Activities", "Menu", "Primary", "Settings", "Log out", "Skip to main content", "Demo environment: do not enter real personal data.", "Privacy", "Language", "Set language"} {
		translate(message)
	}
	page.Shell = familyShell{Language: lang, English: lang == "en", Theme: theme, Text: size, Motion: motion, SiteName: site, CSRF: csrf, DisplayName: actor.DisplayName, Canonical: "/groups/new/"}
	if page.Shell.DisplayName == "" {
		page.Shell.DisplayName = actor.Username
	}
	for _, link := range [][2]string{{"Home", "home"}, {"Browse", "activity_list"}, {"Organize", "activity_create"}, {"Places", "places_map"}, {"Events", "events_list"}, {"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}, {"Civic partners", "partners"}, {"Notifications", "notifications"}, {"Messages", "messages"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Support", "donate"}, {"Settings", "settings"}} {
		page.Shell.Navigation = append(page.Shell.Navigation, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	if data["connections_enabled"] == true {
		page.Shell.Navigation = append(page.Shell.Navigation, familyLink{Label: translate("Connections"), URL: routeURL("connections")})
	}
	for _, link := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}} {
		page.Shell.Footer = append(page.Shell.Footer, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	// This is the source form's finite error vocabulary, never raw database or
	// provider text. Unknown internal messages become the same generic advice.
	errorText, ok := form["errors"].(string)
	if !ok {
		return groupCreatePage{}, invalid
	}
	errorField, fieldError := groupCreateError(errorText)
	if errorText != "" {
		if errorField != "" {
			page.ErrorSummary = errorField + ": " + translate(fieldError)
		} else {
			page.ErrorSummary = translate(fieldError)
		}
	}
	if messages, exists := data["messages"]; exists && messages != nil {
		values, ok := messages.([]socialMessage)
		if !ok {
			return groupCreatePage{}, invalid
		}
		if errorText == "" {
			for _, message := range values {
				known := false
				for _, original := range postNoticeMessages {
					if message["message"] == original["message"] || message["message"] == r.catalog.translate(lang, original["message"], 1) {
						page.Shell.Messages = append(page.Shell.Messages, translate(original["message"]))
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
	names := []string{"city", "activity_type", "title", "description", "cohort"}
	labels := []string{"City", "Activity type", "Title", "Description", "Cohort"}
	for i, input := range snapshot.Fields {
		if input.Name != names[i] {
			return groupCreatePage{}, invalid
		}
		field := groupCreateField{Name: input.Name, Label: translate(labels[i]), Value: input.Value, Required: i < 3, Select: i == 1 || i == 4, Textarea: i == 3}
		switch input.Name {
		case "city":
			field.MaxLength = 128
		case "title":
			field.MaxLength = 200
		case "description":
			field.Help = "Up to 2,000 characters. Leading and trailing spaces are removed when you save."
			if lang == "ro" {
				field.Help = "Cel mult 2.000 de caractere. Spațiile de la început și de la sfârșit sunt eliminate la salvare."
			}
		}
		if field.Select {
			seen := map[string]bool{}
			if input.Name == "cohort" && len(input.Options) != 3 {
				return groupCreatePage{}, invalid
			}
			for j, choice := range input.Options {
				if seen[choice.Value] || choice.Label == "" {
					return groupCreatePage{}, invalid
				}
				seen[choice.Value] = true
				label := choice.Label
				if input.Name == "activity_type" {
					n, err := strconv.ParseInt(choice.Value, 10, 64)
					if err != nil || n < 1 || strconv.FormatInt(n, 10) != choice.Value {
						return groupCreatePage{}, invalid
					}
				} else {
					if choice.Value != []string{"adult", "teen", "child"}[j] {
						return groupCreatePage{}, invalid
					}
					label = translate(choice.Label)
				}
				field.Options = append(field.Options, familyOption{Value: choice.Value, Label: label, Selected: choice.Value == input.Value})
			}
		} else if len(input.Options) != 0 {
			return groupCreatePage{}, invalid
		}
		if errorField == input.Name {
			field.Error = translate(fieldError)
		}
		page.Fields = append(page.Fields, field)
	}
	return page, nil
}

// Only missing ordinary labels/classified field errors have Go-owned fallback
// copy. Existing catalogue translations win; service errors and English output
// keep the original path. This switch stores no request or private state.
func (r *Renderer) groupCreateCopy(language, message string) string {
	value := html.UnescapeString(r.catalog.translate(language, message, 1))
	if language != "ro" || value != message {
		return value
	}
	switch message {
	case "Title":
		return "Titlu"
	case "Description":
		return "Descriere"
	case "Cohort":
		return "Grupă de vârstă"
	case "This field is required.":
		return "Acest câmp este obligatoriu."
	case "This value is too long.":
		return "Valoarea este prea lungă."
	case "Enter a positive whole number.":
		return "Introduceți un număr întreg pozitiv."
	}
	return value
}

func groupCreateError(value string) (string, string) {
	if value == "" {
		return "", ""
	}
	for _, name := range []string{"city", "activity_type", "title", "description", "cohort"} {
		for _, message := range []string{"This field is required.", "This value is too long.", "Enter a positive whole number."} {
			if value == name+": "+message {
				return name, message
			}
		}
	}
	switch value {
	case "That action isn't available for your account.", "Check the form and try again.", "This action is temporarily unavailable. Please try again.":
		return "", value
	default:
		return "", "Check the form and try again."
	}
}

func (r *Renderer) renderGroupCreate(request *http.Request, data pongo2.Context) ([]byte, error) {
	page, err := r.groupCreatePage(request, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadGroupCreateTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	output.Grow(8192)
	if err = parsed.ExecuteTemplate(&output, "group-create", page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
