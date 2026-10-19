package web

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Presentation only. AccountView supplies already-authorized rows; the existing
// services recheck every POST. No Actor, raw account row, permission union or
// arbitrary action URL crosses this typed boundary.
type familyPage struct {
	Shell             familyShell
	Copy              map[string]string
	WardsPage         bool
	Wards             []familyWard
	Guardians         []familyGuardian
	Invite            bool
	InviteUnavailable string
}
type familyShell struct {
	Language, Theme, Text, Motion, SiteName, CSRF, DisplayName, Canonical string
	English                                                               bool
	Navigation, AccountTabs, Footer                                       []familyLink
	Messages                                                              []string
}
type familyLink struct {
	Label, URL string
	Current    bool
}
type familyOption struct {
	Value, Label string
	Selected     bool
}
type familyPreview struct {
	Total, Eligible int
	Text            string
}
type familyMeetup struct {
	Start, End, Activity, Place, City, MeetingPoint, Rationale string
	Supervised, SupervisionLive                                bool
}
type familyWard struct {
	ID                                                             int64
	Name, Cohort                                                   string
	Meetups                                                        []familyMeetup
	Arrival, Observe, RequiresConsent, ConsentActive, GrantConsent bool
	CanLimits, CanTopics, CombinedBlocks                           bool
	ChosenLimits                                                   []string
	LimitsURL, TopicsURL, RevokeURL, RevokeExplanation             string
	Earliest, Latest                                               []familyOption
	MaxOpen                                                        string
	Supervised                                                     bool
	Weekdays, Categories, Topics                                   []familyOption
	Preview                                                        *familyPreview
}
type familyGuardian struct {
	Name, Relationship string
	Arrival, Observe   bool
	ChosenLimits       []string
}

type familyDecoder struct{ err error }

func (d *familyDecoder) fail() {
	if d.err == nil {
		d.err = fmt.Errorf("invalid native family snapshot")
	}
}
func (d *familyDecoder) row(value any) map[string]any {
	switch row := value.(type) {
	case map[string]any:
		return row
	case pongo2.Context:
		return map[string]any(row)
	default:
		d.fail()
		return nil
	}
}
func (d *familyDecoder) rows(value any) []map[string]any {
	switch rows := value.(type) {
	case []map[string]any:
		return rows
	case []any:
		out := make([]map[string]any, 0, len(rows))
		for _, value := range rows {
			out = append(out, d.row(value))
		}
		return out
	default:
		d.fail()
		return nil
	}
}
func (d *familyDecoder) text(value any) string {
	text, ok := value.(string)
	if !ok || !utf8.ValidString(text) {
		d.fail()
		return ""
	}
	return text
}
func (d *familyDecoder) boolean(value any) bool {
	result, ok := value.(bool)
	if !ok {
		d.fail()
	}
	return result
}
func (d *familyDecoder) integer(value any) int64 {
	switch number := value.(type) {
	case int:
		return int64(number)
	case int64:
		return number
	case json.Number:
		result, err := number.Int64()
		if err == nil {
			return result
		}
	case float64:
		if !math.IsNaN(number) && !math.IsInf(number, 0) && number >= -9007199254740991 && number <= 9007199254740991 && math.Trunc(number) == number {
			return int64(number)
		}
	}
	d.fail()
	return 0
}
func (d *familyDecoder) optional(value any, minimum, maximum int64) *int {
	if value == nil {
		return nil
	}
	n := d.integer(value)
	if n < minimum || n > maximum {
		d.fail()
		return nil
	}
	result := int(n)
	return &result
}
func (d *familyDecoder) strings(value any) []string {
	if values, ok := value.([]string); ok {
		for _, value := range values {
			if !utf8.ValidString(value) {
				d.fail()
			}
		}
		return append([]string(nil), values...)
	}
	if values, ok := value.([]any); ok {
		out := make([]string, 0, len(values))
		for _, value := range values {
			out = append(out, d.text(value))
		}
		return out
	}
	d.fail()
	return nil
}
func (d *familyDecoder) name(row map[string]any) string {
	name := d.text(row["display_name"])
	if name == "" {
		name = d.text(row["username"])
	}
	if name == "" {
		d.fail()
	}
	return name
}
func (d *familyDecoder) date(value any, required bool) string {
	if value == nil && !required {
		return ""
	}
	stamp, ok := spaDateValue(value)
	if !ok || stamp.IsZero() {
		d.fail()
		return ""
	}
	return djangoDate(stamp, "D j M, H:i")
}

func familySelected(options []familyOption, values []string) []familyOption {
	selected := map[string]bool{}
	for _, value := range values {
		selected[value] = true
	}
	result := append([]familyOption(nil), options...)
	for index := range result {
		result[index].Selected = selected[result[index].Value]
		delete(selected, result[index].Value)
	}
	// AccountView lists top-level choices; existing services also accept stored
	// subcategories. Preserve those checked values instead of silently clearing
	// a restriction when an ordinary form is saved. No new taxonomy authority.
	for _, value := range values {
		if selected[value] {
			result = append(result, familyOption{Value: value, Label: value, Selected: true})
			delete(selected, value)
		}
	}
	return result
}

func (d *familyDecoder) pairs(value any, translate func(string) string, days bool) []familyOption {
	rows, ok := value.([]any)
	if !ok {
		d.fail()
		return nil
	}
	options := []familyOption{}
	seen := map[string]bool{}
	for index, raw := range rows {
		var key, label string
		switch pair := raw.(type) {
		case []any:
			if !days || len(pair) != 2 {
				d.fail()
				continue
			}
			n := d.integer(pair[0])
			if n != int64(index+1) || n > 7 {
				d.fail()
			}
			key, label = strconv.FormatInt(n, 10), d.text(pair[1])
		case []string:
			if days || len(pair) != 2 {
				d.fail()
				continue
			}
			key, label = d.text(pair[0]), d.text(pair[1])
		default:
			d.fail()
			continue
		}
		if key == "" || seen[key] {
			d.fail()
		}
		seen[key] = true
		options = append(options, familyOption{Value: key, Label: translate(label)})
	}
	if days && len(options) != 7 {
		d.fail()
	}
	return options
}

func (d *familyDecoder) hours(value any, selected *int, translate func(string) string) []familyOption {
	hours, ok := value.([]int)
	if !ok || len(hours) != 24 {
		d.fail()
		return nil
	}
	out := []familyOption{{Value: "", Label: translate("No limit"), Selected: selected == nil}}
	for index, hour := range hours {
		if hour != index {
			d.fail()
		}
		out = append(out, familyOption{Value: strconv.Itoa(hour), Label: fmt.Sprintf("%d:00", hour), Selected: selected != nil && *selected == hour})
	}
	return out
}

func (d *familyDecoder) limits(caps map[string]any, translate func(string) string, plural func(string, int) string, parent bool) ([]string, *int, *int, *int, []string, []string) {
	earliest := d.optional(caps["guardrail_earliest_start_hour"], 0, 23)
	latest := d.optional(caps["guardrail_latest_start_hour"], 0, 23)
	maximum := d.optional(caps["guardrail_max_open_joins"], 1, 50)
	days := []string{}
	switch values := caps["guardrail_allowed_weekday_ints"].(type) {
	case []int:
		for _, day := range values {
			if day < 1 || day > 7 {
				d.fail()
			}
			days = append(days, strconv.Itoa(day))
		}
	default:
		d.fail()
	}
	categories := d.strings(caps["guardrail_allowed_categories"])
	out := []string{}
	perspective := func(ward, guardian string) string {
		if parent {
			return guardian
		}
		return ward
	}
	if d.boolean(caps["guardrail_supervised_only"]) {
		out = append(out, translate(perspective("Only guardian-accompanied meetups.", "&#10003; Limit set: only guardian-accompanied meetups.")))
	}
	if earliest != nil {
		out = append(out, strings.ReplaceAll(translate(perspective("Meetups that start no earlier than %(hour)s:00.", "&#10003; Limit set: meetups starting no earlier than %(hour)s:00.")), "%(hour)s", strconv.Itoa(*earliest)))
	}
	if latest != nil {
		out = append(out, strings.ReplaceAll(translate(perspective("Meetups that start by %(hour)s:00 at the latest.", "&#10003; Limit set: meetups starting by %(hour)s:00 at the latest.")), "%(hour)s", strconv.Itoa(*latest)))
	}
	if maximum != nil {
		out = append(out, strings.ReplaceAll(plural(perspective("You can be in at most %(n)s open meetup at a time.", "&#10003; Limit set: at most %(n)s open meetup at a time."), *maximum), "%(n)s", strconv.Itoa(*maximum)))
	}
	if len(days) > 0 {
		out = append(out, translate(perspective("Only on certain days of the week.", "&#10003; Limit set: only on chosen days of the week.")))
	}
	if len(categories) > 0 {
		out = append(out, translate(perspective("Only certain kinds of activity.", "&#10003; Limit set: only chosen kinds of activity.")))
	}
	return out, earliest, latest, maximum, days, categories
}

func (r *Renderer) familyPage(request *http.Request, name string, data pongo2.Context) (familyPage, error) {
	d := &familyDecoder{}
	actor, authenticated := platform.ActorFrom(request)
	if !authenticated {
		return familyPage{}, platform.ErrForbidden
	}
	page := familyPage{Copy: map[string]string{}, WardsPage: name == "web/wards.html"}
	lang := d.text(data["language"])
	if lang != "en" && lang != "ro" {
		d.fail()
	}
	translate := func(message string) string {
		value := familyPlainCopy(r.catalog, lang, message, 1)
		page.Copy[message] = value
		return value
	}
	for message := range familyCopySources {
		translate(message)
	}
	plural := func(message string, count int) string { return familyPlainCopy(r.catalog, lang, message, count) }
	shell := familyShell{Language: lang, English: lang == "en", Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), CSRF: d.text(data["csrf"]), DisplayName: actor.DisplayName, Canonical: "/guardianship/"}
	if page.WardsPage {
		shell.Canonical = "/wards/"
	}
	if shell.DisplayName == "" {
		shell.DisplayName = actor.Username
	}
	if shell.CSRF == "" {
		d.fail()
	}
	switch shell.Theme {
	case "auto", "light", "dark", "contrast":
	default:
		d.fail()
	}
	switch shell.Text {
	case "normal", "large", "larger":
	default:
		d.fail()
	}
	switch shell.Motion {
	case "auto", "reduce", "full":
	default:
		d.fail()
	}
	for _, link := range [][2]string{{"Home", "home"}, {"Browse", "activity_list"}, {"Organize", "activity_create"}, {"Places", "places_map"}, {"Events", "events_list"}, {"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}, {"Civic partners", "partners"}, {"Notifications", "notifications"}, {"Messages", "messages"}, {"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Support", "donate"}, {"Settings", "settings"}} {
		shell.Navigation = append(shell.Navigation, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	if data["connections_enabled"] == true {
		shell.Navigation = append(shell.Navigation, familyLink{Label: translate("Connections"), URL: routeURL("connections")})
	}
	for _, link := range [][2]string{{"Overview", "you"}, {"Profile", "profile"}, {"Interests", "interests"}, {"Display", "display_preferences"}, {"Privacy & data", "my_privacy"}, {"Donations", "my_donations"}, {"Guardians", "my_guardians"}, {"Settings", "settings"}} {
		if page.WardsPage && link[1] == "my_guardians" && data["has_guardians"] != true {
			continue
		}
		shell.AccountTabs = append(shell.AccountTabs, familyLink{Label: translate(link[0]), URL: routeURL(link[1]), Current: !page.WardsPage && link[1] == "my_guardians"})
	}
	for _, link := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}} {
		shell.Footer = append(shell.Footer, familyLink{Label: translate(link[0]), URL: routeURL(link[1])})
	}
	if messages, exists := data["messages"]; exists && messages != nil {
		// Existing action adapters use fixed public categories. Never echo raw
		// SQL/provider text or a user-supplied message into the native shell.
		switch values := messages.(type) {
		case []socialMessage:
			if len(values) > 0 {
				shell.Messages = []string{translate("Please check the form and try again.")}
			}
		default:
			d.fail()
		}
	}
	page.Shell = shell
	if page.WardsPage {
		categories := d.pairs(data["guardrail_categories"], translate, false)
		weekdays := d.pairs(data["guardrail_weekdays"], translate, true)
		onboarding := d.boolean(data["minor_onboarding"])
		page.Invite = onboarding && actor.Cohort == "adult" && actor.AgeBand == "adult" && actor.IdentityVerified
		if !page.Invite {
			if !onboarding {
				page.InviteUnavailable = "Guardianship requests are disabled in this environment."
				if lang == "ro" {
					page.InviteUnavailable = "Cererile de tutelă sunt dezactivate în acest mediu."
				}
			} else {
				page.InviteUnavailable = "Only a verified adult can send a guardianship request."
				if lang == "ro" {
					page.InviteUnavailable = "Doar un adult verificat poate trimite o cerere de tutelă."
				}
			}
		}
		seen := map[int64]bool{}
		for _, row := range d.rows(data["wards"]) {
			ward := familyWard{ID: d.integer(row["id"]), Name: d.name(row)}
			if ward.ID <= 0 || seen[ward.ID] {
				d.fail()
			}
			seen[ward.ID] = true
			cohort := d.text(row["cohort"])
			labels := map[string]string{"child": "Child (under 16)", "teen": "Teen (16-17)", "adult": "Adult (18+)"}
			if _, ok := labels[cohort]; !ok {
				d.fail()
			}
			ward.Cohort = translate(labels[cohort])
			caps := d.row(row["caps"])
			ward.Arrival, ward.Observe = d.boolean(caps["can_get_arrival_pings"]), d.boolean(caps["can_observe_messaging"])
			ward.RequiresConsent, ward.ConsentActive, ward.GrantConsent = d.boolean(caps["requires_consent"]), d.boolean(caps["consent_active"]), d.boolean(caps["can_grant_consent"])
			ward.CanLimits, ward.CanTopics = d.boolean(caps["can_set_guardrails"]), d.boolean(row["can_set_topics"])
			if cohort != "child" && (ward.Arrival || ward.Observe || ward.CanLimits || ward.CanTopics) {
				d.fail()
			}
			ward.CombinedBlocks, ward.Supervised = d.boolean(caps["guardrail_combined_blocks_all"]), d.boolean(caps["guardrail_supervised_only"])
			limits, earliest, latest, maximum, days, selections := d.limits(caps, translate, plural, true)
			ward.ChosenLimits = limits
			ward.Earliest, ward.Latest = d.hours(data["guardrail_hours"], earliest, translate), d.hours(data["guardrail_hours"], latest, translate)
			if maximum != nil {
				ward.MaxOpen = strconv.Itoa(*maximum)
			}
			ward.Weekdays = familySelected(weekdays, days)
			ward.Categories = familySelected(categories, selections)
			ward.Topics = familySelected(categories, d.strings(row["topic_slugs"]))
			ward.LimitsURL, ward.TopicsURL, ward.RevokeURL = routeURL("guardian_guardrail_set", ward.ID), routeURL("ward_topics_set", ward.ID), routeURL("guardian_revoke", ward.ID)
			ward.RevokeExplanation = strings.ReplaceAll(translate("End guardianship of %(name)s? This also revokes the consent you granted."), "%(name)s", ward.Name)
			if raw, exists := row["guardrail_preview"]; exists && raw != nil {
				if cohort != "child" {
					d.fail()
				}
				preview, ok := raw.(map[string]int)
				_, totalOK := preview["total"]
				_, eligibleOK := preview["eligible"]
				if !ok || len(preview) != 2 || !totalOK || !eligibleOK {
					d.fail()
				} else {
					value := familyPreview{Total: preview["total"], Eligible: preview["eligible"]}
					value.Text = strings.NewReplacer("%(eligible)s", strconv.Itoa(value.Eligible), "%(total)s", strconv.Itoa(value.Total)).Replace(translate("Right now, they could join %(eligible)s of %(total)s upcoming meetups with room in their cohort."))
					if value.Total < 0 || value.Eligible < 0 || value.Eligible > value.Total {
						d.fail()
					}
					ward.Preview = &value
				}
			}
			for _, meetup := range d.rows(row["meetups"]) {
				place, activity := d.row(meetup["place"]), d.row(meetup["activity_type"])
				value := familyMeetup{Start: d.date(meetup["starts_at"], true), Activity: d.text(activity["name"]), Place: d.text(place["name"]), City: d.text(place["address_city"]), Supervised: d.boolean(meetup["supervised"]), SupervisionLive: d.boolean(meetup["supervision_live"])}
				if value.Place == "" {
					value.Place = translate("a place")
				}
				if cohort == "child" {
					value.End, value.MeetingPoint = d.date(meetup["ends_at"], false), d.text(meetup["meeting_point"])
					if rationale, exists := meetup["child_venue_rationale"]; exists {
						switch d.text(rationale) {
						case "":
						case "staff_verified":
							value.Rationale = translate("Child-approved venue (staff-verified)")
						case "rule_match":
							value.Rationale = translate("Child-approved venue (matches the public-venue rule)")
						default:
							d.fail()
						}
					}
				}
				ward.Meetups = append(ward.Meetups, value)
			}
			page.Wards = append(page.Wards, ward)
		}
	} else if name == "web/guardianship.html" {
		for _, row := range d.rows(data["guardians"]) {
			caps := d.row(row["caps"])
			relationship := d.text(caps["relationship"])
			label := relationship
			if known, exists := map[string]string{"parent": "Parent", "legal_guardian": "Legal guardian", "guardian": "Guardian"}[relationship]; exists {
				label = translate(known)
			}
			// The existing API admits bounded custom relationship labels. They
			// remain escaped text, not a new legal/authorization classification.
			if label == "" || utf8.RuneCountInString(relationship) > 32 {
				d.fail()
			}
			limits, _, _, _, _, _ := d.limits(caps, translate, plural, false)
			page.Guardians = append(page.Guardians, familyGuardian{Name: d.name(row), Relationship: label, Arrival: d.boolean(caps["can_get_arrival_pings"]), Observe: d.boolean(caps["can_observe_messaging"]), ChosenLimits: limits})
		}
	} else {
		d.fail()
	}
	if d.err != nil {
		return familyPage{}, d.err
	}
	return page, nil
}
