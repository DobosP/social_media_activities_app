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
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// These seven presentation models consume admitted producer results only.
// They never query, rank, determine publication, or infer accessibility.
type publicNativePage struct {
	Shell                                                                                                                                   familyShell
	Copy                                                                                                                                    map[string]string
	Kind, Title, Nonce, PublicID, LanguageNext, GoogleVerification, BingVerification, OGLocale, Description, OGDescription, OGTitle, OGType string
	Signed, Staff                                                                                                                           bool
	Avatar                                                                                                                                  template.URL
	Navigation, AccountLinks                                                                                                                []familyLink
	Messages                                                                                                                                []accountCenterMessage
	Unread                                                                                                                                  int64
	Structured, Breadcrumb, MapVocabulary                                                                                                   template.JS
	Communities                                                                                                                             []familyLink
	Activities                                                                                                                              []publicNativeDiscoveryActivity
	Groups                                                                                                                                  []publicNativeDiscoveryGroup
	Categories                                                                                                                              []publicNativeCategory
	Snapshot                                                                                                                                bool
	Event                                                                                                                                   publicNativeEvent
	Place                                                                                                                                   *publicPlaceDetail
	FooterIntro                                                                                                                             template.HTML
}
type publicNativeDiscoveryActivity struct{ Title, Type, Place, Start string }
type publicNativeDiscoveryGroup struct{ Title, Type, Area, Description string }
type publicNativeCategory struct{ Slug, Name string }
type publicNativeVocabulary struct {
	Slug            string   `json:"slug"`
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Category        string   `json:"category"`
	CategoryName    string   `json:"categoryName"`
	TopCategory     string   `json:"topCategory"`
	TopCategoryName string   `json:"topCategoryName"`
}
type publicNativeCredit struct{ Attribution, License, URL string }
type publicNativeShareTarget struct {
	ID    int64
	Label string
}
type publicNativeShare struct {
	Kind     string
	ObjectID int64
	Targets  []publicNativeShareTarget
}
type publicNativeShareView struct {
	Share      publicNativeShare
	CSRF, Next string
	Copy       map[string]string
}
type publicNativeEvent struct {
	Title, Type, Start, End, Website, ReportURL, ResetURL, OrganizeURL, GaugeURL string
	POC, Discoverable, Unverified, CanReport, CanConvene, CanOrganize            bool
	At, Status, Related                                                          template.HTML
	Description                                                                  [][]string
	Credit                                                                       publicNativeCredit
	Share                                                                        publicNativeShareView
}

//go:embed pages/public-discovery.gohtml pages/public-place-detail.gohtml
var publicNativePages embed.FS
var errPublicNativeSnapshot = errors.New("invalid native public discovery snapshot")
var loadPublicNativeTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("public-discovery").ParseFS(publicNativePages, "pages/public-discovery.gohtml", "pages/public-place-detail.gohtml")
})

func publicNativeTemplate(name string) bool {
	switch name {
	case "web/landing.html", "web/communities_graph.html", "web/discover.html", "web/places.html", "web/place_detail.html", "web/event_detail.html", "web/open_data.html":
		return true
	}
	return false
}
func (r *Renderer) publicNativeCopy(language, message string) string {
	return html.UnescapeString(r.catalog.translate(language, message, 1))
}

// Only repository-owned literal block messages may contain HTML. Every value
// is escaped by the unchanged translatedBlock helper before interpolation.
func (r *Renderer) publicNativeBlock(language, message string, values ...any) template.HTML {
	allowed := false
	for _, literal := range publicDiscoveryBlockMessages {
		if message == literal {
			allowed = true
			break
		}
	}
	if !allowed {
		for _, literal := range publicPlaceBlockMessages {
			if message == literal {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return ""
	}
	return template.HTML(translatedBlock(r.catalog, language, base64.RawStdEncoding.EncodeToString([]byte(message)), 1, values...).String())
}
func publicNativeParagraphs(value string) [][]string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	if value == "" {
		return nil
	}
	out := [][]string{}
	for _, part := range strings.Split(value, "\n\n") {
		out = append(out, strings.Split(part, "\n"))
	}
	return out
}

// Match the release template filters' string behavior with fixed limits.
func publicNativeTruncateChars(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		if limit >= 3 {
			return string(runes[:limit-3]) + "..."
		}
		return string(runes[:limit])
	}
	return value
}

var publicNativeTags = regexp.MustCompile("<[^>]*?>")

func publicNativeDescription(value string, limit int) string {
	words := strings.Fields(strings.TrimSpace(publicNativeTags.ReplaceAllString(value, "")))
	if len(words) > limit {
		words = append(words[:limit], "...")
	}
	return strings.Join(words, " ")
}

// JSON comes only from the selected producer's publicLD result. Re-marshal a
// RawMessage to validate and HTML-escape script delimiters without rounding
// numeric tokens, changing keys, or trusting pre-marked HTML/JS fragments.
func publicNativeJSON(d *groupDetailDecoder, value any) template.JS {
	if value == nil {
		return ""
	}
	provided, ok := value.(*pongo2.Value)
	if !ok || provided == nil {
		d.fail()
		return ""
	}
	raw := provided.String()
	if raw == "" {
		return ""
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		d.fail()
		return ""
	}
	encoded, err := json.Marshal(json.RawMessage(raw))
	if err != nil {
		d.fail()
		return ""
	}
	return template.JS(encoded)
}
func publicNativeCreditProjection(d *groupDetailDecoder, value any, fallback string) publicNativeCredit {
	if value == nil {
		return publicNativeCredit{}
	}
	row := d.row(value)
	out := publicNativeCredit{Attribution: d.optionalText(row["attribution"]), License: d.optionalText(row["license_name"]), URL: d.optionalText(row["provenance_url"])}
	if out.Attribution == "" {
		out.Attribution = fallback
	}
	return out
}
func publicNativeShareProjection(d *groupDetailDecoder, data pongo2.Context, kind string) publicNativeShare {
	out := publicNativeShare{Kind: kind}
	for _, row := range d.optionalRows(data["share_targets"]) {
		out.Targets = append(out.Targets, publicNativeShareTarget{ID: d.id(row["pk"]), Label: d.text(row["title"]) + " (" + d.stamp(row["starts_at"], "j M") + ")"})
	}
	if len(out.Targets) > 0 {
		if d.text(data["share_kind"]) != kind {
			d.fail()
		}
		out.ObjectID = d.id(data["share_obj_id"])
	}
	return out
}

func (r *Renderer) publicNativePage(request *http.Request, name string, data pongo2.Context) (publicNativePage, error) {
	if !publicNativeTemplate(name) {
		return publicNativePage{}, errPublicNativeSnapshot
	}
	actor, signed := platform.ActorFrom(request)
	if name == "web/communities_graph.html" && (!signed || actor.ID < 1) {
		return publicNativePage{}, platform.ErrForbidden
	}
	d := groupDetailDecoder{}
	p := publicNativePage{Kind: strings.TrimSuffix(strings.TrimPrefix(name, "web/"), ".html"), Copy: map[string]string{}, Signed: signed, Staff: actor.IsStaff, PublicID: actor.PublicID, OGType: "website"}
	context := d.row(data["request"])
	p.Nonce, p.LanguageNext = d.text(context["csp_nonce"]), d.text(context["path"])
	p.Shell = familyShell{Language: d.text(data["language"]), Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), Canonical: d.text(data["canonical_url"]), CSRF: d.optionalText(data["csrf"]), DisplayName: actor.DisplayName}
	p.Shell.English = p.Shell.Language == "en"
	if (p.Shell.Language != "en" && p.Shell.Language != "ro") || p.Nonce == "" || p.Shell.SiteName == "" || p.LanguageNext != request.URL.Path || !accountCenterCanonical(p.Shell.Canonical) {
		d.fail()
	}
	if p.Shell.Theme != "auto" && p.Shell.Theme != "light" && p.Shell.Theme != "dark" && p.Shell.Theme != "contrast" || p.Shell.Text != "normal" && p.Shell.Text != "large" && p.Shell.Text != "larger" || p.Shell.Motion != "auto" && p.Shell.Motion != "reduce" && p.Shell.Motion != "full" {
		d.fail()
	}
	user := d.row(data["user"])
	if d.boolean(user["is_authenticated"]) != signed {
		d.fail()
	}
	if signed {
		if actor.ID < 1 || (name == "web/communities_graph.html" && p.Shell.CSRF == "") || d.text(user["public_id"]) != actor.PublicID || d.text(user["username"]) != actor.Username || d.text(user["display_name"]) != actor.DisplayName {
			d.fail()
		}
		for _, key := range []string{"id", "pk"} {
			if user[key] != nil && d.integer(user[key]) != actor.ID {
				d.fail()
			}
		}
		if user["cohort"] != nil && d.text(user["cohort"]) != actor.Cohort {
			d.fail()
		}
		if p.Shell.DisplayName == "" {
			p.Shell.DisplayName = actor.Username
		}
		p.Avatar = d.person(map[string]any{"id": actor.ID, "public_id": actor.PublicID, "display_name": actor.DisplayName, "username": actor.Username, "avatar_uri": user["avatar_uri"]}, actor.ID, false).Avatar
	}
	if user["is_staff"] != nil && d.boolean(user["is_staff"]) != actor.IsStaff {
		d.fail()
	}
	if data["unread_notifications"] != nil {
		p.Unread = d.integer(data["unread_notifications"])
		if p.Unread < 0 {
			d.fail()
		}
	}
	p.GoogleVerification = d.optionalText(data["google_site_verification"])
	p.BingVerification = d.optionalText(data["bing_site_verification"])
	p.OGLocale = d.text(data["og_locale"])
	tr := func(message string) string {
		if value, ok := p.Copy[message]; ok {
			return value
		}
		value := r.publicNativeCopy(p.Shell.Language, message)
		p.Copy[message] = value
		return value
	}
	for _, message := range publicDiscoveryMessages {
		tr(message)
	}
	link := func(label, route string) familyLink { return familyLink{Label: tr(label), URL: routeURL(route)} }
	if signed {
		p.Shell.Navigation = append(p.Shell.Navigation, link("Browse", "activity_list"), link("Organize", "activity_create"))
	}
	p.Navigation = append(p.Navigation, link("Places", "places_map"), link("Events", "events_list"))
	if signed {
		p.Navigation = append(p.Navigation, link("Communities &amp; groups", "communities"), link("Gauge interest", "gauges"))
	}
	p.Navigation = append(p.Navigation, link("Civic partners", "partners"))
	if signed {
		p.AccountLinks = append(p.AccountLinks, link("Profile", "profile"))
		if d.optionalBool(data["connections_enabled"]) {
			p.AccountLinks = append(p.AccountLinks, link("Connections", "connections"))
		}
		for _, pair := range [][2]string{{"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Account overview", "you"}, {"Settings", "settings"}, {"Support", "donate"}} {
			p.AccountLinks = append(p.AccountLinks, link(pair[0], pair[1]))
		}
	}
	for _, pair := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}} {
		p.Shell.Footer = append(p.Shell.Footer, link(pair[0], pair[1]))
	}
	if signed {
		p.Shell.Footer = append(p.Shell.Footer, link("Settings", "settings"))
	}
	if data["messages"] != nil {
		rows, ok := data["messages"].([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, row := range rows {
				p.Messages = append(p.Messages, accountCenterMessage{Text: d.text(row["message"]), Tags: d.text(row["tags"])})
			}
		}
	}
	p.Structured = publicNativeJSON(&d, data["structured_data"])
	p.Breadcrumb = publicNativeJSON(&d, data["breadcrumb_data"])
	p.Title = tr("Social Activities")
	p.OGTitle = tr("Social Activities")
	p.Description = tr(publicNativeDefaultDescription)
	p.OGDescription = tr(publicNativeDefaultOGDescription)
	p.FooterIntro = r.publicNativeBlock(p.Shell.Language, publicNativeFooterIntro)
	switch p.Kind {
	case "landing":
	case "communities_graph":
		p.Title = tr("Community graph")
		for _, row := range d.rows(data["communities"]) {
			p.Communities = append(p.Communities, familyLink{Label: d.text(row["name"]), URL: routeURL("community_detail", d.text(row["slug"]))})
		}
	case "discover":
		for _, row := range d.rows(data["activities"]) {
			item := publicNativeDiscoveryActivity{Title: d.text(row["title"]), Type: d.optionalText(d.row(row["activity_type"])["name"]), Start: d.stamp(row["starts_at"], "j M Y, H:i")}
			if row["place"] != nil {
				item.Place = d.optionalText(d.row(row["place"])["name"])
			}
			p.Activities = append(p.Activities, item)
		}
		for _, row := range d.rows(data["groups"]) {
			item := publicNativeDiscoveryGroup{Title: d.text(row["title"]), Description: publicNativeTruncateChars(d.optionalText(row["description"]), 200)}
			if row["activity_type"] != nil {
				item.Type = d.optionalText(d.row(row["activity_type"])["name"])
			} else if row["category"] != nil {
				item.Type = d.optionalText(d.row(row["category"])["name"])
			}
			if row["area"] != nil {
				item.Area = d.optionalText(d.row(row["area"])["name"])
			}
			p.Groups = append(p.Groups, item)
		}
	case "places":
		p.Title = tr("Places")
		vocabulary := []publicNativeVocabulary{}
		for _, row := range d.rows(data["categories"]) {
			p.Categories = append(p.Categories, publicNativeCategory{Slug: d.text(row["slug"]), Name: d.text(row["name"])})
		}
		for _, row := range d.rows(data["type_vocabulary"]) {
			aliases := d.strings(row["aliases"])
			if aliases == nil {
				aliases = []string{}
			}
			vocabulary = append(vocabulary, publicNativeVocabulary{Slug: d.text(row["slug"]), Name: d.text(row["name"]), Aliases: aliases, Category: d.text(row["category"]), CategoryName: d.text(row["categoryName"]), TopCategory: d.text(row["topCategory"]), TopCategoryName: d.text(row["topCategoryName"])})
		}
		raw, err := json.Marshal(vocabulary)
		if err != nil {
			d.fail()
		} else {
			p.MapVocabulary = template.JS(raw)
		}
	case "event_detail":
		row := d.row(data["event"])
		eventID := d.id(row["pk"])
		e := publicNativeEvent{Title: d.text(row["title"]), Start: d.stamp(row["starts_at"], "l j F Y, H:i"), End: d.stamp(row["ends_at"], "H:i"), Website: notificationInboxSafeHref(d.optionalText(row["url"])), POC: d.optionalBool(row["local_poc"]), Discoverable: d.boolean(data["event_discoverable"]), Unverified: d.optionalText(data["event_reliability"]) == "unverified", CanReport: d.boolean(data["can_report_event"]), CanConvene: d.boolean(data["can_convene"]), ReportURL: routeURL("event_report", eventID), ResetURL: routeURL("event_report_reset", eventID), GaugeURL: routeURL("gauge_create") + "?event=" + strconv.FormatInt(eventID, 10)}
		if row["activity_type"] != nil {
			e.Type = d.optionalText(d.row(row["activity_type"])["name"])
		}
		if row["place"] != nil {
			place := d.row(row["place"])
			placeID := d.id(place["pk"])
			placeURL := routeURL("place_detail", placeID)
			e.At = r.publicNativeBlock(p.Shell.Language, "at <a href=\"%(place_url)s\">%(place_name)s</a>", placeURL, d.optionalText(place["display_name"]))
			e.CanOrganize = e.Discoverable && signed
			e.OrganizeURL = routeURL("activity_create") + "?place=" + strconv.FormatInt(placeID, 10)
			if row["activity_type"] != nil {
				e.OrganizeURL += "&activity_type=" + strconv.FormatInt(d.id(d.row(row["activity_type"])["pk"]), 10)
			}
			e.OrganizeURL += "&starts_at=" + url.QueryEscape(d.stamp(row["starts_at"], "Y-m-d\\TH:i"))
		}
		if !e.Discoverable {
			e.Status = r.publicNativeBlock(p.Shell.Language, "Source status: %(status)s. This event is not listed as an upcoming activity.", d.text(row["get_lifecycle_status_display"]))
		}
		description := d.optionalText(row["description"])
		e.Description = publicNativeParagraphs(description)
		p.Title, p.OGTitle, p.OGType = e.Title, e.Title, "event"
		p.OGDescription = e.Title
		if description != "" {
			p.Description = publicNativeDescription(description, 30)
			p.OGDescription = p.Description
		} else {
			p.Description = html.UnescapeString(string(r.publicNativeBlock(p.Shell.Language, "%(title)s — an event you can attend in person.", e.Title)))
		}
		if data["related_landing"] != nil {
			related := d.row(data["related_landing"])
			e.Related = r.publicNativeBlock(p.Shell.Language, "More: <a href=\"%(url)s\">%(name)s</a>", notificationInboxSafeHref(d.text(related["url"])), d.text(related["name"]))
		}
		e.Credit = publicNativeCreditProjection(&d, data["attribution_credit"], d.optionalText(row["get_source_display"]))
		if e.Discoverable {
			e.Share = publicNativeShareView{Share: publicNativeShareProjection(&d, data, "event"), CSRF: p.Shell.CSRF, Next: p.LanguageNext, Copy: p.Copy}
		}
		p.Event = e
	case "place_detail":
		place, err := r.publicPlaceProjection(request, data)
		if err != nil {
			return publicNativePage{}, err
		}
		p.Place = &place
		p.Title, p.OGTitle, p.OGType = place.Title, place.Title, "place"
		p.Description, p.OGDescription = place.Description, place.OGDescription
	case "open_data":
		p.Title, p.OGTitle = tr("Open data"), tr("Open data")
		p.Description = tr(publicNativeOpenDescription)
		p.OGDescription = tr(publicNativeOpenOGDescription)
		p.Snapshot = d.boolean(data["snapshot_available"])
	}
	if d.err != nil {
		return publicNativePage{}, errPublicNativeSnapshot
	}
	return p, nil
}
func (r *Renderer) renderPublicNative(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	p, err := r.publicNativePage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadPublicNativeTemplates()
	if err != nil {
		return nil, err
	}
	return executePublicNativeTemplate(parsed, p)
}
func executePublicNativeTemplate(parsed *template.Template, p publicNativePage) ([]byte, error) {
	var output bytes.Buffer
	if err := parsed.ExecuteTemplate(&output, "public-discovery", p); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
