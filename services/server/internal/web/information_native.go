package web

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Presentation only. Ledger/Mine/paymentView retain every query, status,
// campaign/partner visibility and payment authority. No totals are recomputed.
type informationPage struct {
	Shell                                                                                      familyShell
	Copy                                                                                       map[string]string
	Kind, Title, Nonce, PublicID, LanguageNext, GoogleVerification, BingVerification, OGLocale string
	Authenticated                                                                              bool
	Avatar                                                                                     template.URL
	Navigation, AccountLinks                                                                   []familyLink
	Messages                                                                                   []accountCenterMessage
	Unread                                                                                     int64
	Form                                                                                       informationDonationForm
	Lead, RaisedLabel, SpendingLabel, FooterIntro                                              template.HTML
	Anchors                                                                                    []informationAnchor
	Spend                                                                                      []informationSpend
	InKind                                                                                     []informationInKind
	Outcomes                                                                                   []informationOutcome
	Campaigns                                                                                  []informationCampaign
	Completed                                                                                  []informationCloseout
	Partners                                                                                   []informationPartner
	Donations                                                                                  []informationReceipt
}
type informationDonationForm struct {
	Amount, Campaign, Error string
	Choices                 []familyOption
}
type informationAnchor struct{ Currency, Money, Label string }
type informationSpend struct {
	Category, Money, Currency, Period string
	Label                             template.HTML
}
type informationInKind struct {
	Category, Quantity, Unit, Money string
	HasQuantity, HasMoney           bool
	Estimate                        template.HTML
}
type informationOutcome struct {
	Headline, Detail, Period string
	Partner                  template.HTML
}
type informationCampaign struct {
	Title, Percent, Partner, PartnerBlurb, PartnerURL, DonateURL string
	Description                                                  [][]string
	MeterLabel                                                   string
	RaisedLabel                                                  template.HTML
}
type informationCloseout struct {
	Title       string
	Outcome     [][]string
	RaisedLabel template.HTML
	Spend       []informationSpend
}
type informationPartner struct {
	Name, Kind, Blurb, Website string
	Steward                    template.HTML
}
type informationReceipt struct {
	Money, Currency, Status, Campaign, When string
	Recurring                               bool
	Reference                               template.HTML
}

//go:embed pages/information.gohtml
var informationPages embed.FS
var errInformationSnapshot = errors.New("invalid native public information snapshot")
var loadInformationTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("information").ParseFS(informationPages, "pages/information.gohtml")
})

func informationTemplate(name string) bool {
	switch name {
	case "web/donate.html", "web/transparency.html", "web/campaigns.html", "web/partners.html", "web/my_donations.html":
		return true
	}
	return false
}

// This exact numeric conversion is display only: it matches the existing cents
// filter's float64/100 and %.2f formatting, without changing stored amounts.
func informationNumber(value any) (raw, money string, truth bool, ok bool) {
	switch n := value.(type) {
	case int:
		raw = strconv.Itoa(n)
	case int64:
		raw = strconv.FormatInt(n, 10)
	case float64:
		raw = fmt.Sprintf("%f", n)
	case json.Number:
		raw = n.String()
	case string:
		raw = n
	default:
		return "", "", false, false
	}
	number, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return "", "", false, false
	}
	return raw, fmt.Sprintf("%.2f", pongo2.AsValue(value).Float()/100), pongo2.AsValue(value).IsTrue(), true
}
func informationParagraphs(value string) [][]string {
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	if value == "" {
		return nil
	}
	paragraphs := [][]string{}
	for _, block := range strings.Split(value, "\n\n") {
		paragraphs = append(paragraphs, strings.Split(block, "\n"))
	}
	return paragraphs
}

// Only these repository-owned literal msgids can cross the trusted-copy seam.
// Every dynamic argument uses the existing translatedBlock HTML escaping.
func (r *Renderer) informationBlock(language, message string, values ...any) template.HTML {
	if !informationBlockMessages[message] {
		return ""
	}
	return template.HTML(translatedBlock(r.catalog, language, base64.RawStdEncoding.EncodeToString([]byte(message)), 1, values...).String())
}

func (r *Renderer) informationPage(request *http.Request, name string, data pongo2.Context) (informationPage, error) {
	if !informationTemplate(name) {
		return informationPage{}, errInformationSnapshot
	}
	actor, signed := platform.ActorFrom(request)
	if name == "web/my_donations.html" && (!signed || actor.ID < 1 || !actor.IsActive) {
		return informationPage{}, platform.ErrForbidden
	}
	d := groupDetailDecoder{}
	p := informationPage{Kind: strings.TrimSuffix(strings.TrimPrefix(name, "web/"), ".html"), Copy: map[string]string{}, Authenticated: signed, PublicID: actor.PublicID}
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
		if d.text(user["public_id"]) != actor.PublicID || d.text(user["username"]) != actor.Username || d.text(user["display_name"]) != actor.DisplayName {
			d.fail()
		}
		if p.Shell.DisplayName == "" {
			p.Shell.DisplayName = actor.Username
		}
		p.Avatar = d.person(map[string]any{"id": actor.ID, "public_id": actor.PublicID, "display_name": actor.DisplayName, "username": actor.Username, "avatar_uri": user["avatar_uri"]}, actor.ID, false).Avatar
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
		if s, ok := p.Copy[message]; ok {
			return s
		}
		s := html.UnescapeString(r.catalog.translate(p.Shell.Language, message, 1))
		p.Copy[message] = s
		return s
	}
	for _, message := range informationTextMessages {
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
	if value := data["messages"]; value != nil {
		rows, ok := value.([]socialMessage)
		if !ok {
			d.fail()
		} else {
			for _, row := range rows {
				p.Messages = append(p.Messages, accountCenterMessage{Text: d.text(row["message"]), Tags: d.text(row["tags"])})
			}
		}
	}
	money := func(v any) string {
		_, m, _, ok := informationNumber(v)
		if !ok {
			d.fail()
		}
		return m
	}
	block := func(message string, values ...any) template.HTML {
		return r.informationBlock(p.Shell.Language, message, values...)
	}
	p.FooterIntro = block(informationFooterIntro)
	switch p.Kind {
	case "donate":
		p.Title = tr("Support us")
		p.Lead = block(informationDonationLead)
		form := d.row(data["form"])
		native, ok := form["native"].(informationDonationForm)
		if !ok {
			d.fail()
		}
		p.Form = native
		p.Form.Choices = append([]familyOption(nil), native.Choices...)
		if d.optionalText(form["errors"]) != native.Error {
			d.fail()
		}
		for _, row := range d.optionalRows(data["cost_anchors"]) {
			p.Anchors = append(p.Anchors, informationAnchor{Currency: d.text(row["currency"]), Money: money(row["amount_cents"]), Label: d.text(row["label"])})
		}
	case "transparency":
		p.Title = tr("Where the money goes")
		p.RaisedLabel = block("%(raised)s EUR", money(data["raised_cents"]))
		p.SpendingLabel = block("Total allocated so far: <strong>%(total)s EUR</strong>.", money(data["spend_total_cents"]))
		for _, row := range d.optionalRows(data["spend_rows"]) {
			p.Spend = append(p.Spend, informationSpend{Category: d.text(row["category"]), Label: block("%(total)s EUR", money(row["total_cents"]))})
		}
		for _, row := range d.optionalRows(data["in_kind_rows"]) {
			item := informationInKind{Category: d.text(row["category"]), Unit: d.optionalText(row["unit_text"])}
			if item.Unit == "" {
				item.Unit = tr("units")
			}
			if row["total_quantity"] != nil {
				var ok bool
				item.Quantity, _, item.HasQuantity, ok = informationNumber(row["total_quantity"])
				if !ok {
					d.fail()
				}
			}
			if row["total_cents"] != nil {
				var ok bool
				_, item.Money, item.HasMoney, ok = informationNumber(row["total_cents"])
				if !ok {
					d.fail()
				}
			}
			item.Estimate = block("%(total)s EUR (estimated value)", item.Money)
			p.InKind = append(p.InKind, item)
		}
		for _, row := range d.optionalRows(data["civic_outcomes"]) {
			item := informationOutcome{Headline: d.text(row["headline"]), Detail: d.optionalText(row["detail"]), Period: d.optionalText(row["period"])}
			if partner := d.optionalText(row["partner_name"]); partner != "" {
				item.Partner = block("with %(p)s", partner)
			}
			p.Outcomes = append(p.Outcomes, item)
		}
	case "campaigns":
		p.Title = tr("Campaigns")
		for _, row := range d.optionalRows(data["campaigns"]) {
			percent, _, _, ok := informationNumber(row["percent"])
			n, parseErr := strconv.ParseFloat(percent, 64)
			if !ok || parseErr != nil || n < 0 || n > 100 {
				d.fail()
			}
			raised, goal := money(row["raised_cents"]), money(row["goal_cents"])
			item := informationCampaign{Title: d.text(row["title"]), Percent: percent, Description: informationParagraphs(d.optionalText(row["description"])), Partner: d.optionalText(row["partner_name"]), PartnerBlurb: d.optionalText(row["partner_blurb"]), PartnerURL: notificationInboxSafeHref(d.optionalText(row["partner_website"])), DonateURL: routeURL("donate") + "?campaign=" + d.text(row["slug"]), MeterLabel: html.UnescapeString(string(block("raised %(raised)s of %(goal)s EUR goal", raised, goal))), RaisedLabel: block("Raised %(raised)s of %(goal)s EUR goal.", raised, goal)}
			p.Campaigns = append(p.Campaigns, item)
		}
		for _, row := range d.optionalRows(data["completed_campaigns"]) {
			item := informationCloseout{Title: d.text(row["title"]), Outcome: informationParagraphs(d.text(row["outcome"])), RaisedLabel: block("%(raised)s EUR raised &middot; closed %(when)s", money(row["raised_cents"]), d.stamp(row["closed_at"], "j M Y"))}
			for _, s := range d.optionalRows(row["spend_entries"]) {
				item.Spend = append(item.Spend, informationSpend{Category: d.text(s["category"]), Money: money(s["amount_cents"]), Currency: d.text(s["currency"]), Period: d.optionalText(s["period"])})
			}
			p.Completed = append(p.Completed, item)
		}
	case "partners":
		p.Title = tr("Civic & venue partners")
		for _, row := range d.optionalRows(data["partners"]) {
			item := informationPartner{Name: d.text(row["name"]), Kind: d.text(row["get_kind_display"]), Blurb: d.optionalText(row["blurb"]), Website: notificationInboxSafeHref(d.optionalText(row["website"]))}
			if row["place"] != nil {
				place := d.row(row["place"])
				placeName := d.optionalText(place["name"])
				if placeName == "" {
					placeName = tr("a venue")
				}
				item.Steward = block("Stewards <a href=\"%(place_url)s\">%(place_name)s</a>.", routeURL("place_detail", d.id(place["pk"])), placeName)
			}
			p.Partners = append(p.Partners, item)
		}
	case "my_donations":
		p.Title = tr("My donations")
		for _, pair := range [][2]string{{"Overview", "you"}, {"Profile", "profile"}, {"Interests", "interests"}, {"Display", "display_preferences"}, {"Privacy &amp; data", "my_privacy"}, {"Donations", "my_donations"}} {
			item := link(pair[0], pair[1])
			item.Current = pair[1] == "my_donations"
			p.Shell.AccountTabs = append(p.Shell.AccountTabs, item)
		}
		if d.optionalBool(data["has_guardians"]) {
			p.Shell.AccountTabs = append(p.Shell.AccountTabs, link("Guardians", "my_guardians"))
		}
		p.Shell.AccountTabs = append(p.Shell.AccountTabs, link("Settings", "settings"))
		for _, row := range d.optionalRows(data["donations"]) {
			item := informationReceipt{Money: money(row["amount_cents"]), Currency: d.text(row["currency"]), Status: d.text(row["get_status_display"]), Recurring: d.boolean(row["recurring"]), When: d.stamp(row["created_at"], "Y-m-d H:i")}
			if row["campaign"] != nil {
				item.Campaign = d.text(d.row(row["campaign"])["title"])
			}
			if reference := d.optionalText(row["external_ref"]); reference != "" {
				item.Reference = block("reference %(ref)s", reference)
			}
			p.Donations = append(p.Donations, item)
		}
	}
	if d.err != nil {
		return informationPage{}, errInformationSnapshot
	}
	return p, nil
}
func (r *Renderer) renderInformation(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	page, err := r.informationPage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadInformationTemplates()
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	if err = parsed.ExecuteTemplate(&output, "information", page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
