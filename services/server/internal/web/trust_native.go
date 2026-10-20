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
	"strconv"
	"strings"
	"sync"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// Presentation snapshots only. Profile, AccountView and ReportTarget own all
// authority, tier clamping and self scoping; POST services recheck their actions.
// No account model, raw report/audit row, attestation or identity proof is kept.
type trustPage struct {
	Shell                                                                                    familyShell
	Mode, Title, Nonce, PublicID, LanguageNext, GoogleVerification, BingVerification, Locale string
	Fragment, Authenticated, HasGuardians                                                    bool
	Avatar                                                                                   template.URL
	Unread                                                                                   int64
	AccountLinks                                                                             []familyLink
	Messages                                                                                 []accountCenterMessage
	Links                                                                                    map[string]string
	T                                                                                        func(string) string
	F                                                                                        func(string, ...string) string
	Rich                                                                                     func(string, ...string) []trustRun
	Person                                                                                   trustPerson
	Entries                                                                                  []trustEntry
	Decisions                                                                                []trustDecision
	Reports                                                                                  []trustReport
	Privacy                                                                                  trustPrivacy
	Wallet                                                                                   trustWallet
	Destroyed, Retained                                                                      []string
	Report                                                                                   trustReportForm
}
type trustPerson struct {
	PublicID, Display, Username, Tier, About, ProfileURL, Photo, ReportURL, BlockURL string
	Avatar                                                                           template.URL
	AvatarSource                                                                     template.HTMLAttr
	Verified, Connected, JoinRequest, CanConnect, Pending, CanMessage                bool
	Activities, Groups                                                               string
	ActivityOverflow, GroupOverflow                                                  int64
	Interests                                                                        []string
}
type trustEntry struct{ Label, When string }
type trustDecision struct {
	ID                                         int64
	Action, Scope, Reason, When, AppealStatus  string
	Sanction, Active, AuthorDeleted, CanAppeal bool
}
type trustReport struct{ Reason, Status, Detail, Outcome, Filed, Reviewed string }
type trustPrivacy struct {
	Provenance, Access, HasToken                           bool
	Band, Provider, Interests, Muted, Decisions, Donations string
	Retention                                              []trustEntry
}
type trustWallet struct {
	Verified, Available, Sandbox, Challenge bool
	State, RequestText                      string
	Request                                 template.JS // Only JSON marshalled from trustWalletRequest below.
}
type trustWalletRequest struct {
	Nonce      string                `json:"nonce"`
	Audience   string                `json:"audience"`
	State      string                `json:"state"`
	Definition trustWalletDefinition `json:"presentation_definition"`
}
type trustWalletDefinition struct {
	ID     string             `json:"id"`
	Inputs []trustWalletInput `json:"input_descriptors"`
}
type trustWalletInput struct {
	ID          string                 `json:"id"`
	Format      trustWalletFormat      `json:"format"`
	Constraints trustWalletConstraints `json:"constraints"`
}
type trustWalletFormat struct {
	JWT trustWalletAlgorithm `json:"jwt_vc"`
}
type trustWalletAlgorithm struct {
	Algorithms []string `json:"alg"`
}
type trustWalletConstraints struct {
	Fields []trustWalletClaim `json:"fields"`
}
type trustWalletClaim struct {
	Path []string `json:"path"`
}
type trustReportForm struct {
	Target, Label, ID, Reason, Detail, Error string
	Choices                                  []familyOption
}
type trustRun struct{ Kind, Text, URL string }

var nativeTrustTemplates = map[string]bool{
	"web/person.html": true, "web/_person_card.html": true,
	"web/activity_log.html": true, "web/safety_record.html": true,
	"web/my_privacy.html": true, "web/verify_age.html": true,
	"web/account_delete.html": true, "web/report.html": true,
	"web/privacy.html": true, "web/terms.html": true,
}

//go:embed pages/trust.gohtml
var trustPages embed.FS
var errTrustSnapshot = errors.New("invalid native trust snapshot")
var loadTrustTemplates = sync.OnceValues(func() (*template.Template, error) {
	return template.New("trust").ParseFS(trustPages, "pages/trust.gohtml")
})

// Same image-only URI boundary as groupDetailDecoder.person. This helper does
// not require or fabricate a target's private numeric ID to render its card.
func trustAvatar(d *groupDetailDecoder, value any, required bool) template.URL {
	raw := d.optionalText(value)
	if raw == "" {
		if required {
			d.fail()
		}
		return ""
	}
	const prefix = "data:image/svg+xml;base64,"
	if !strings.HasPrefix(raw, prefix) || len(raw) > 1<<20 {
		d.fail()
		return ""
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(raw, prefix))
	if err != nil || !strings.HasPrefix(string(decoded), "<svg ") {
		d.fail()
		return ""
	}
	return template.URL(raw)
}

// Only the already-vetted generated SVG image URI may use this fixed attribute.
// html/template escapes '+' in quoted values, while the established profile
// response contract retains the exact generated URI. Escape HTML metacharacters
// before marking the complete, fixed-name src attribute; no caller HTML or
// arbitrary URL is trusted, and uploaded-photo URLs use their existing path.
func trustProfileAvatar(d *groupDetailDecoder, value any) (template.URL, template.HTMLAttr) {
	avatar := trustAvatar(d, value, true)
	if avatar == "" {
		return "", ""
	}
	return avatar, template.HTMLAttr(`src="` + html.EscapeString(string(avatar)) + `"`)
}

// Translations are escaped text. Rich legal copy permits only the same literal
// tag sequence as the repository source, and never returns template.HTML.
// A catalogue entry cannot introduce an element, attribute or external link.
func trustRichRuns(source, translated, privacyURL string) []trustRun {
	split := func(text string) ([]trustRun, string) {
		out := []trustRun{}
		shape := ""
		for text != "" {
			pos := strings.IndexByte(text, '<')
			if pos < 0 {
				out = append(out, trustRun{Kind: "text", Text: html.UnescapeString(text)})
				break
			}
			if pos > 0 {
				out = append(out, trustRun{Kind: "text", Text: html.UnescapeString(text[:pos])})
				text = text[pos:]
			}
			matched := false
			for _, tag := range []struct{ literal, kind string }{{"<strong>", "strong"}, {"</strong>", "/strong"}, {"<em>", "em"}, {"</em>", "/em"}, {"<br>", "br"}, {`<a href="%(privacy_url)s">`, "link"}, {`<a href="%(settings_url)s">`, "link"}, {"</a>", "/link"}} {
				if strings.HasPrefix(text, tag.literal) {
					target := privacyURL
					if tag.literal == `<a href="%(settings_url)s">` {
						target = routeURL("settings")
					}
					out = append(out, trustRun{Kind: tag.kind, URL: target})
					shape += tag.literal
					text = text[len(tag.literal):]
					matched = true
					break
				}
			}
			if !matched {
				return nil, "INVALID"
			}
		}
		return out, shape
	}
	original, shape := split(source)
	candidate, candidateShape := split(translated)
	if shape == "INVALID" {
		return []trustRun{{Kind: "text", Text: html.UnescapeString(source)}}
	}
	if candidateShape != shape {
		return original
	}
	return candidate
}

func (r *Renderer) trustPage(request *http.Request, name string, data pongo2.Context) (trustPage, error) {
	if !nativeTrustTemplates[name] {
		return trustPage{}, errTrustSnapshot
	}
	actor, authenticated := platform.ActorFrom(request)
	public := name == "web/privacy.html" || name == "web/terms.html"
	if !public && !authenticated {
		return trustPage{}, platform.ErrForbidden
	}
	d := groupDetailDecoder{}
	p := trustPage{Mode: strings.TrimSuffix(strings.TrimPrefix(name, "web/"), ".html"), Fragment: name == "web/_person_card.html", Authenticated: authenticated, Links: map[string]string{}}
	lang := d.text(data["language"])
	if lang != "en" && lang != "ro" {
		d.fail()
	}
	p.T = func(message string) string { return html.UnescapeString(r.catalog.translate(lang, message, 1)) }
	p.F = func(message string, args ...string) string {
		translated := p.T(message)
		for i := 0; i+1 < len(args); i += 2 {
			translated = strings.ReplaceAll(translated, "%("+args[i]+")s", args[i+1])
		}
		return translated
	}
	p.Rich = func(message string, args ...string) []trustRun {
		runs := trustRichRuns(message, r.catalog.translate(lang, message, 1), routeURL("privacy"))
		for i := range runs {
			for j := 0; j+1 < len(args); j += 2 {
				runs[i].Text = strings.ReplaceAll(runs[i].Text, "%("+args[j]+")s", args[j+1])
			}
		}
		return runs
	}
	for _, key := range []string{"home", "profile", "connection_request", "connection_message", "activity_log", "safety_record", "safety_record_appeal", "my_privacy", "verify_age", "interests", "access_preferences", "notification_preferences", "my_donations", "my_guardians", "settings", "account_export", "account_delete", "privacy", "terms", "logout", "login", "register", "set_language", "activity_list", "activity_create", "messages", "you"} {
		p.Links[key] = routeURL(key)
	}
	if !p.Fragment {
		ctx := d.row(data["request"])
		p.Nonce, p.LanguageNext = d.text(ctx["csp_nonce"]), d.text(ctx["path"])
		p.Shell = familyShell{Language: lang, English: lang == "en", Theme: d.text(data["display_theme"]), Text: d.text(data["display_text"]), Motion: d.text(data["display_motion"]), SiteName: d.text(data["site_name"]), CSRF: d.text(data["csrf"]), Canonical: d.text(data["canonical_url"]), DisplayName: actor.DisplayName}
		p.GoogleVerification = d.optionalText(data["google_site_verification"])
		p.BingVerification = d.optionalText(data["bing_site_verification"])
		p.Locale = d.optionalText(data["og_locale"])
		if p.Locale == "" {
			p.Locale = "en_GB"
		}
		if p.Shell.DisplayName == "" {
			p.Shell.DisplayName = actor.Username
		}
		if (!public && p.Shell.CSRF == "") || p.Nonce == "" || p.Shell.SiteName == "" || p.LanguageNext != request.URL.Path || !accountCenterCanonical(p.Shell.Canonical) {
			d.fail()
		}
		if p.Shell.Theme != "auto" && p.Shell.Theme != "light" && p.Shell.Theme != "dark" && p.Shell.Theme != "contrast" || p.Shell.Text != "normal" && p.Shell.Text != "large" && p.Shell.Text != "larger" || p.Shell.Motion != "auto" && p.Shell.Motion != "reduce" && p.Shell.Motion != "full" {
			d.fail()
		}
		if authenticated {
			p.PublicID = actor.PublicID
			avatar := data["avatar_uri"]
			if avatar == nil {
				if user, ok := data["user"].(map[string]any); ok {
					avatar = user["avatar_uri"]
				}
			}
			p.Avatar = trustAvatar(&d, avatar, false)
			if data["unread_notifications"] != nil {
				p.Unread = d.integer(data["unread_notifications"])
				if p.Unread < 0 {
					d.fail()
				}
			}
		}
		p.HasGuardians = d.optionalBool(data["has_guardians"])
		link := func(label, route string) familyLink { return familyLink{Label: p.T(label), URL: routeURL(route)} }
		if authenticated {
			for _, entry := range [][2]string{{"Browse", "activity_list"}, {"Organize", "activity_create"}} {
				p.Shell.Navigation = append(p.Shell.Navigation, link(entry[0], entry[1]))
			}
		}
		for _, entry := range [][2]string{{"Places", "places_map"}, {"Events", "events_list"}} {
			p.Shell.Navigation = append(p.Shell.Navigation, link(entry[0], entry[1]))
		}
		if authenticated {
			for _, entry := range [][2]string{{"Communities &amp; groups", "communities"}, {"Gauge interest", "gauges"}} {
				p.Shell.Navigation = append(p.Shell.Navigation, link(entry[0], entry[1]))
			}
		}
		p.Shell.Navigation = append(p.Shell.Navigation, link("Civic partners", "partners"))
		if authenticated {
			for _, entry := range [][2]string{{"Notifications", "notifications"}, {"Messages", "messages"}} {
				p.Shell.Navigation = append(p.Shell.Navigation, link(entry[0], entry[1]))
			}
			p.AccountLinks = append(p.AccountLinks, link("Profile", "profile"))
			if d.optionalBool(data["connections_enabled"]) {
				p.AccountLinks = append(p.AccountLinks, link("Connections", "connections"))
			}
			for _, entry := range [][2]string{{"My meetups", "my_meetups"}, {"Run my meetups", "organize"}, {"Account overview", "you"}, {"Settings", "settings"}, {"Support", "donate"}} {
				p.AccountLinks = append(p.AccountLinks, link(entry[0], entry[1]))
			}
		}
		for _, entry := range [][2]string{{"Privacy", "privacy"}, {"Terms", "terms"}, {"Transparency", "transparency"}, {"Open data", "open_data"}, {"Support the platform", "donate"}, {"Display", "display_preferences"}} {
			p.Shell.Footer = append(p.Shell.Footer, link(entry[0], entry[1]))
		}
		if authenticated {
			p.Shell.Footer = append(p.Shell.Footer, link("Settings", "settings"))
		}
		if raw := data["messages"]; raw != nil {
			messages, ok := raw.([]socialMessage)
			if !ok {
				d.fail()
			} else {
				for _, message := range messages {
					p.Messages = append(p.Messages, accountCenterMessage{Text: d.text(message["message"]), Tags: d.optionalText(message["tags"])})
				}
			}
		}
	}
	// Self-only data must match the authenticated subject. No secondary adult,
	// cohort, identity or active-state authorization is introduced in this view.
	if name == "web/activity_log.html" || name == "web/safety_record.html" || name == "web/my_privacy.html" || name == "web/verify_age.html" || name == "web/account_delete.html" {
		user := d.row(data["user"])
		if d.id(user["id"]) != actor.ID || d.text(user["public_id"]) != actor.PublicID {
			d.fail()
		}
	}
	count := func(value any) int64 {
		n := d.integer(value)
		if n < 0 || int64(int(n)) != n {
			d.fail()
		}
		return n
	}
	switch name {
	case "web/person.html", "web/_person_card.html":
		card := d.row(data["card"])
		avatar, avatarSource := trustProfileAvatar(&d, card["avatar"])
		p.Person = trustPerson{PublicID: d.text(card["public_id"]), Display: d.text(card["display"]), Tier: d.text(card["tier"]), Avatar: avatar, AvatarSource: avatarSource}
		person := &p.Person
		if person.PublicID == "" || person.Display == "" || (person.Tier != "stranger" && person.Tier != "shared" && person.Tier != "connected") {
			d.fail()
		}
		person.About = p.F("About %(name)s", "name", person.Display)
		person.ProfileURL = routeURL("person", person.PublicID)
		if person.Tier != "stranger" {
			person.Username = d.text(card["username"])
			if person.Username == person.Display {
				person.Username = ""
			}
			person.Verified = d.boolean(card["verified"])
			person.Connected = d.boolean(card["connected"])
			person.CanConnect = d.boolean(card["can_connect"])
			person.Pending = d.boolean(card["request_pending"])
			person.CanMessage = d.boolean(card["can_message"])
			shared := d.row(card["shared"])
			person.JoinRequest = d.boolean(shared["join_request"])
			person.Activities = strings.Join(d.strings(shared["activities"]), ", ")
			person.Groups = strings.Join(d.strings(shared["groups"]), ", ")
			person.ActivityOverflow = count(shared["activity_overflow"])
			person.GroupOverflow = count(shared["group_overflow"])
			if card["interests"] != nil {
				person.Interests = d.strings(card["interests"])
			}
			if !p.Fragment && d.boolean(card["show_photo"]) {
				person.Photo = d.localMedia(data["photo_url"])
			}
		}
		if !p.Fragment {
			target := d.id(d.row(data["person_user"])["id"])
			person.ReportURL = routeURL("report") + "?type=user&id=" + strconv.FormatInt(target, 10)
			person.BlockURL = routeURL("block_user", target)
		}
		p.Title = person.Display
	case "web/activity_log.html":
		p.Title = p.T("Your activity log")
		for _, row := range d.rows(data["entries"]) {
			p.Entries = append(p.Entries, trustEntry{Label: d.text(row["label"]), When: d.stamp(row["when"], "j M Y, H:i")})
		}
	case "web/safety_record.html":
		p.Title = p.T("Your safety record")
		for _, row := range d.rows(data["decisions"]) {
			value := trustDecision{ID: d.id(row["action_id"]), Action: d.text(row["action_label"]), Scope: d.text(row["scope"]), Reason: p.F("Reason: %(reason)s", "reason", d.text(row["reason_label"])), When: d.stamp(row["created_at"], "j M Y, H:i"), Sanction: d.boolean(row["is_sanction"]), Active: d.boolean(row["is_active"]), AuthorDeleted: d.boolean(row["content_author_deleted"]), CanAppeal: d.boolean(row["can_appeal"])}
			if !value.CanAppeal && row["appeal_status_label"] != nil {
				value.AppealStatus = p.F("Contest status: %(status)s", "status", d.text(row["appeal_status_label"]))
			}
			p.Decisions = append(p.Decisions, value)
		}
		for _, row := range d.rows(data["reports"]) {
			value := trustReport{Reason: d.text(row["reason_label"]), Status: d.text(row["status_label"]), Detail: d.optionalText(row["detail"]), Filed: p.F("Filed %(filed)s", "filed", d.stamp(row["created_at"], "j M Y"))}
			if outcome := d.optionalText(row["resolution"]); outcome != "" {
				value.Outcome = p.F("Outcome: %(resolution)s", "resolution", outcome)
			}
			if row["handled_at"] != nil {
				value.Reviewed = p.F("reviewed %(reviewed)s", "reviewed", d.stamp(row["handled_at"], "j M Y"))
			}
			p.Reports = append(p.Reports, value)
		}
	case "web/my_privacy.html":
		p.Title = p.T("What we know about you")
		if data["provenance"] != nil {
			provenance := d.row(data["provenance"])
			p.Privacy.Provenance = d.boolean(provenance["has_row"])
			if p.Privacy.Provenance {
				p.Privacy.Band = d.text(provenance["band_display"])
				if provider := d.optionalText(provenance["provider"]); provider != "" {
					p.Privacy.Provider = p.F("Verified via %(provider)s.", "provider", provider)
				}
			}
		}
		p.Privacy.Access = d.boolean(data["access_set"])
		p.Privacy.HasToken = d.boolean(data["api_token_exists"])
		p.Privacy.Interests = p.F("Activity types you've chosen: %(n)s.", "n", strconv.FormatInt(count(data["interests_count"]), 10))
		p.Privacy.Donations = p.F("Donations on record: %(n)s.", "n", strconv.FormatInt(count(data["donations_count"]), 10))
		p.Privacy.Decisions = p.F("Moderation decisions about you: %(d)s · Reports you've filed: %(r)s.", "d", strconv.FormatInt(count(data["decisions_count"]), 10), "r", strconv.FormatInt(count(data["reports_count"]), 10))
		muted := d.strings(data["muted_labels"])
		if len(muted) > 0 {
			p.Privacy.Muted = p.F("You've muted: %(kinds)s.", "kinds", strings.Join(muted, ", "))
		}
		retention, ok := data["retention"].([]map[string]string)
		if !ok {
			d.fail()
		} else {
			for _, row := range retention {
				p.Privacy.Retention = append(p.Privacy.Retention, trustEntry{Label: d.text(row["category"]), When: d.text(row["ttl_description"])})
			}
		}
		for _, entry := range [][2]string{{"Overview", "you"}, {"Profile", "profile"}, {"Interests", "interests"}, {"Display", "display_preferences"}, {"Privacy &amp; data", "my_privacy"}, {"Donations", "my_donations"}} {
			p.Shell.AccountTabs = append(p.Shell.AccountTabs, familyLink{Label: p.T(entry[0]), URL: routeURL(entry[1]), Current: entry[1] == "my_privacy"})
		}
		if p.HasGuardians {
			p.Shell.AccountTabs = append(p.Shell.AccountTabs, familyLink{Label: p.T("Guardians"), URL: routeURL("my_guardians")})
		}
		p.Shell.AccountTabs = append(p.Shell.AccountTabs, familyLink{Label: p.T("Settings"), URL: routeURL("settings")})
	case "web/verify_age.html":
		p.Title = p.T("Verify your age")
		p.Wallet = trustWallet{Verified: d.boolean(data["verified"]), Available: d.boolean(data["native_available"]), Sandbox: d.boolean(data["sandbox"])}
		if p.Wallet.Available && data["native_challenge"] != nil {
			p.Wallet.Challenge = true
			source := d.row(data["native_challenge"])
			public := trustWalletRequest{Nonce: d.text(source["nonce"]), Audience: d.text(source["audience"]), State: d.text(source["state"])}
			definition := d.row(source["presentation_definition"])
			if d.text(definition["id"]) != "age-verification" {
				d.fail()
			}
			inputs := d.rows(definition["input_descriptors"])
			if len(inputs) != 1 {
				d.fail()
			} else {
				input := inputs[0]
				if d.text(input["id"]) != "age-attestation" {
					d.fail()
				}
				alg := d.strings(d.row(d.row(input["format"])["jwt_vc"])["alg"])
				if len(alg) != 1 || alg[0] != "ES256" {
					d.fail()
				}
				fields := d.rows(d.row(input["constraints"])["fields"])
				if len(fields) != 2 {
					d.fail()
				} else {
					for i, path := range []string{"$.age_over_16", "$.age_over_18"} {
						paths := d.strings(fields[i]["path"])
						if len(paths) != 1 || paths[0] != path {
							d.fail()
						}
					}
				}
			}
			if public.Nonce == "" || public.Audience == "" || public.State == "" {
				d.fail()
			}
			public.Definition = trustWalletDefinition{ID: "age-verification", Inputs: []trustWalletInput{{ID: "age-attestation", Format: trustWalletFormat{JWT: trustWalletAlgorithm{Algorithms: []string{"ES256"}}}, Constraints: trustWalletConstraints{Fields: []trustWalletClaim{{Path: []string{"$.age_over_16"}}, {Path: []string{"$.age_over_18"}}}}}}}
			raw, err := json.Marshal(public)
			if err != nil {
				d.fail()
			} else {
				p.Wallet.Request = template.JS(raw)
				p.Wallet.RequestText = string(raw)
				p.Wallet.State = public.State
			}
		}
	case "web/account_delete.html":
		p.Title = p.T("Delete my account")
		preview := d.row(data["preview"])
		destroyed, ok := preview["destroyed"].(map[string]int64)
		if !ok {
			d.fail()
		}
		retained, ok := preview["retained"].(map[string]int64)
		if !ok {
			d.fail()
		}
		plural := func(singular, multiple string, n int64) string {
			message := singular
			if n != 1 {
				message = multiple
			}
			return strings.ReplaceAll(html.UnescapeString(r.catalog.translate(lang, message, int(n))), "%(n)s", strconv.FormatInt(n, 10))
		}
		for _, item := range trustDestroyedCopy {
			n, exists := destroyed[item[0]]
			if !exists {
				d.fail()
			}
			p.Destroyed = append(p.Destroyed, plural(item[1], item[2], count(n)))
		}
		for _, item := range trustRetainedCopy {
			n, exists := retained[item[0]]
			if !exists {
				d.fail()
			}
			p.Retained = append(p.Retained, plural(item[1], item[2], count(n)))
		}
	case "web/report.html":
		p.Title = p.T("Report")
		p.Report = trustReportForm{Target: d.text(data["target_type"]), Label: d.text(data["target_label"]), ID: strconv.FormatInt(d.id(data["target_id"]), 10)}
		if p.Report.Target == "" {
			d.fail()
		}
		form := d.row(data["form"])
		// Already-sanitized widget values are structured by the existing builder.
		// Never parse as_p, label_tag, widgets or an arbitrary error HTML prefix.
		fields := d.rows(form["fields"])
		if len(fields) != 2 {
			d.fail()
		} else {
			for i, key := range []string{"reason", "detail"} {
				if d.text(fields[i]["name"]) != key {
					d.fail()
				}
				value := d.text(fields[i]["value"])
				if key == "reason" {
					p.Report.Reason = value
				} else {
					p.Report.Detail = value
				}
			}
		}
		for _, choice := range trustReportReasons {
			p.Report.Choices = append(p.Report.Choices, familyOption{Value: choice.Value, Label: choice.Label, Selected: choice.Value == p.Report.Reason})
		}
		if raw := form["native_trust_error"]; raw != nil {
			p.Report.Error = d.text(raw)
			if p.Report.Error != "Choose a valid report reason." {
				d.fail()
			}
		}
	case "web/privacy.html":
		p.Title = p.T("Privacy Policy")
	case "web/terms.html":
		p.Title = p.T("Terms of Service")
	}
	if d.err != nil {
		return trustPage{}, errTrustSnapshot
	}
	return p, nil
}

var trustReportReasons = []option{{"grooming", "Grooming / predatory contact"}, {"harassment", "Harassment / bullying"}, {"csam", "Child sexual abuse material"}, {"spam", "Spam"}, {"off_platform", "Unsafe off-platform / meetup risk"}, {"other", "Other"}}

func (r *Renderer) renderTrust(request *http.Request, name string, data pongo2.Context) ([]byte, error) {
	page, err := r.trustPage(request, name, data)
	if err != nil {
		return nil, err
	}
	parsed, err := loadTrustTemplates()
	if err != nil {
		return nil, err
	}
	return executeTrustTemplate(parsed, page)
}
func executeTrustTemplate(parsed *template.Template, page trustPage) ([]byte, error) {
	var output bytes.Buffer
	target := "trust"
	if page.Fragment {
		target = "trust-person-card"
	}
	if err := parsed.ExecuteTemplate(&output, target, page); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
