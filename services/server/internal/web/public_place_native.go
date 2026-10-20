package web

import (
	"encoding/base64"
	"errors"
	"html"
	"html/template"
	"maps"
	"net/http"
	"strconv"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

// This is a presentation of publicPlaceDetail's selected result. Publication,
// contribution, quorum, membership and accessibility decisions stay upstream.
type publicPlaceDetail struct {
	ID                                                                                                                  int64
	Title, Heading, Description, OGDescription, Address, Country, Website                                               string
	OpenLabel, OpenClass, AccessLabel, AccessClass, PostedHours                                                         string
	LocalPOC, LocalMode, Past, HasHours, HasPreference, CanContribute, KidFacts                                         bool
	OfficialClaim, HasCover                                                                                             bool
	OrganizeURL, ImageURL, ClaimURL, CorrectionURL, FactURL, HoursReportURL, HoursResetURL, ClosureURL, ClosureResetURL string
	Visual                                                                                                              publicPlaceVisual
	Categories                                                                                                          []string
	Pending, OfficialNote, LoginNote, AccessNote, CityNote                                                              template.HTML
	Partner                                                                                                             *publicPlacePartner
	Official                                                                                                            *publicPlacePartner
	Credit                                                                                                              publicNativeCredit
	Meetups                                                                                                             []groupDetailActivity
	Events                                                                                                              []publicPlaceEvent
	Access, Venue, RecordedAccess, RecordedVenue                                                                        []publicPlaceFact
	Edges                                                                                                               []publicPlaceEdge
	Corrections                                                                                                         []publicPlaceCorrection
	Brief                                                                                                               []publicPlaceBrief
	Related                                                                                                             []familyLink
	CityURL                                                                                                             string
	Share                                                                                                               publicNativeShareView
	ImageForm                                                                                                           workflowForm
}
type publicPlacePartner struct{ Name, URL, Kind, Blurb string }
type publicPlaceVisual struct {
	URL, Alt, Attribution, License, SourceURL string
	AccentURL                                 template.URL
}
type publicPlaceEvent struct {
	Title, URL, When string
	LocalPOC, Past   bool
}
type publicPlaceFact struct {
	Key, Label, State, StateLabel, Class string
	FromMap, VotedYes, VotedNo           bool
	Counts                               template.HTML
}
type publicPlaceEdge struct {
	Name, URL, MyVote, Class string
	Confirmed, Disputed      bool
	Counts, Disputes         template.HTML
}
type publicPlaceCorrection struct {
	Label, Value, URL   string
	Proposer, Confirmed bool
	Counts              template.HTML
}
type publicPlaceBrief struct{ Label, Sentence string }

var publicPlaceBlockMessages = []string{
	"%(name)s — a place to meet up for in-person group activities. %(addr)s",
	"%(name)s — a place to meet up for in-person group activities.",
	"This place is pending &mdash; it needs\n    %(confirmed)s of %(required)s\n    confirmations before it's public. For now only you and moderators can see it.",
	"Maintained with %(name)s",
	"<a href=\"%(login_url)s\">Log in</a> or\n    <a href=\"%(register_url)s\">sign up</a> to organise or join activities here.",
	"%(confirms)s/%(required)s confirms",
	", %(disputes)s disputed",
	"<a href=\"%(access_prefs_url)s\">Set your access preferences</a> to see at a glance whether a venue matches your needs.",
	"%(yes)s yes / %(no)s no (needs %(required)s)",
	"Discover more in %(city)s &rarr;",
}

func publicPlacePartnerProjection(d *groupDetailDecoder, value any) *publicPlacePartner {
	if value == nil {
		return nil
	}
	row := d.row(value)
	return &publicPlacePartner{Name: d.text(row["name"]), URL: notificationInboxSafeHref(d.optionalText(row["website"])), Kind: d.optionalText(row["get_kind_display"]), Blurb: d.optionalText(row["blurb"])}
}

func (r *Renderer) publicPlaceProjection(request *http.Request, data pongo2.Context) (publicPlaceDetail, error) {
	d := &groupDetailDecoder{}
	language := d.text(data["language"])
	tr := func(message string) string { return r.publicNativeCopy(language, message) }
	block := func(message string, values ...any) template.HTML {
		return r.publicNativeBlock(language, message, values...)
	}
	place := d.row(data["place"])
	p := publicPlaceDetail{ID: d.id(place["id"]), Title: d.optionalText(data["place_label"]), Address: d.optionalText(place["display_address"]), Country: d.optionalText(place["address_country"]), Website: notificationInboxSafeHref(d.optionalText(place["website"])), PostedHours: d.optionalText(place["posted_hours_text"])}
	p.Heading = p.Title
	if p.Title == "" {
		p.Title = tr("Place")
		p.Heading = tr("Unnamed place")
	}
	// The original metadata uses the supplied (possibly blank) place_label.
	p.Description = html.UnescapeString(string(block(publicPlaceBlockMessages[0], data["place_label"], p.Address)))
	p.OGDescription = html.UnescapeString(string(block(publicPlaceBlockMessages[1], data["place_label"])))
	p.LocalPOC, p.LocalMode, p.Past = d.optionalBool(place["local_poc"]), d.optionalBool(data["local_poc_mode"]), d.optionalBool(data["local_poc_past"])
	p.HasPreference, p.CanContribute, p.KidFacts = d.boolean(data["has_access_pref"]), d.boolean(data["can_contribute"]), d.boolean(data["has_kid_facts"])
	// Pongo's original conditional tests the provided parsed schedule's truth.
	p.HasHours = pongo2.AsValue(place["display_opening_hours"]).IsTrue()
	switch open := data["open_now"].(type) {
	case nil:
		if p.HasHours {
			p.OpenLabel = tr("Posted hours couldn't be read for right now.")
		}
	case bool:
		p.OpenLabel, p.OpenClass = tr("Closed now"), "fact--false"
		if open {
			p.OpenLabel, p.OpenClass = tr("Open now"), "fact--true"
		}
	case string:
		if open != "unverified" {
			d.fail()
		} else {
			p.OpenLabel, p.OpenClass = tr("Hours may be out of date"), "fact--limited"
		}
	default:
		d.fail()
	}
	if p.HasPreference {
		switch d.text(data["access_match"]) {
		case "match":
			p.AccessLabel, p.AccessClass = tr("Matches your access needs"), "fact--true"
		case "mismatch":
			p.AccessLabel, p.AccessClass = tr("May not meet your access needs"), "fact--false"
		case "unknown":
			p.AccessLabel = tr("Some of your access needs aren't recorded for this venue.")
		default:
			d.fail()
		}
	} else {
		p.AccessNote = block(publicPlaceBlockMessages[7], routeURL("access_preferences"))
	}
	for _, category := range d.optionalRows(data["category_chips"]) {
		p.Categories = append(p.Categories, d.text(category["name"]))
	}
	visual := d.row(data["place_visual"])
	switch d.text(visual["kind"]) {
	case "place_cover_photo":
		p.Visual = publicPlaceVisual{URL: d.localMedia(visual["url"]), Alt: d.optionalText(visual["alt"]), Attribution: d.optionalText(visual["attribution"]), License: d.optionalText(visual["license_name"]), SourceURL: notificationInboxSafeHref(d.optionalText(visual["source_page_url"]))}
	case "accent":
		// Never trust an arbitrary supplied SVG. Match the deterministic producer
		// exactly before placing those same bytes in a separate decorative image.
		// The image boundary keeps clipped SVG geometry local to its cover.
		seedName := d.optionalText(place["_display_name"])
		if seedName == "" {
			seedName = strconv.FormatInt(p.ID, 10)
		}
		expected := activitySVG("place:" + d.optionalText(place["_seed_hint"]) + ":" + seedName).String()
		supplied, ok := visual["svg"].(*pongo2.Value)
		if !ok || supplied == nil || supplied.String() != expected {
			d.fail()
		} else {
			p.Visual.AccentURL = template.URL("data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(expected)))
		}
	default:
		d.fail()
	}
	p.Partner, p.Official = publicPlacePartnerProjection(d, data["partner"]), publicPlacePartnerProjection(d, data["official_partner"])
	if p.Official != nil {
		p.OfficialNote = block(publicPlaceBlockMessages[3], p.Official.Name)
	}
	if data["pending_proposal"] != nil {
		pending := d.row(data["pending_proposal"])
		// Use the actual producer count, rather than the old template's absent
		// Django relation path pending_proposal.confirmations.count.
		confirmed, required := d.integer(pending["confirmations_count"]), d.integer(pending["required_confirmations"])
		if confirmed < 0 || required < 0 {
			d.fail()
		}
		p.Pending = block(publicPlaceBlockMessages[2], confirmed, required)
	}
	p.Credit = publicNativeCreditProjection(d, data["attribution_credit"], d.optionalText(place["get_source_display"]))
	p.OrganizeURL, p.ImageURL, p.ClaimURL = routeURL("activity_create")+"?place="+strconv.FormatInt(p.ID, 10), routeURL("place_official_image", p.ID), routeURL("place_claim", p.ID)
	p.CorrectionURL, p.FactURL = routeURL("place_correction_propose", p.ID), routeURL("fact_vote", p.ID)
	p.HoursReportURL, p.HoursResetURL = routeURL("place_open_now_report", p.ID), routeURL("place_open_now_reset", p.ID)
	p.ClosureURL, p.ClosureResetURL = routeURL("place_closure_report", p.ID), routeURL("place_closure_reset", p.ID)
	p.OfficialClaim, p.HasCover = pongo2.AsValue(data["official_claim"]).IsTrue(), pongo2.AsValue(place["cover"]).IsTrue()
	actor, signed := platform.ActorFrom(request)
	if p.OfficialClaim {
		if !signed {
			d.fail()
		}
		p.ImageForm = r.workflowForm(data["official_image_form"], "PlaceOfficialImageForm", actor, d, tr)
	}
	if signed {
		for _, row := range d.rows(data["meetups"]) {
			// This producer returns HydrateActivities rows directly. Copy only the
			// presentation aliases expected by the existing shared activity card;
			// do not run socialActivityModels or mutate hydrated nested maps.
			card := maps.Clone(row)
			card["activity_type"] = row["activity_type_obj"]
			card["place"] = row["place_obj"]
			card["secondary_types"] = map[string]any{"all": row["secondary_types"]}
			labels := socialModel(map[string]any{"status": row["status"], "cost_band": row["cost_band"], "difficulty": row["difficulty"]})
			for _, key := range []string{"get_status_display", "get_cost_band_display", "get_difficulty_display"} {
				card[key] = labels[key]
			}
			p.Meetups = append(p.Meetups, r.groupDetailActivity(d, card, d.optionalBool(data["show_accent"]), tr))
		}
		for _, row := range d.rows(data["events"]) {
			p.Events = append(p.Events, publicPlaceEvent{Title: d.text(row["title"]), URL: routeURL("event_detail", d.id(row["id"])), When: d.stamp(row["starts_at"], "D j M, H:i"), LocalPOC: d.optionalBool(row["local_poc"]), Past: d.optionalBool(row["local_poc_past"])})
		}
	} else {
		p.LoginNote = block(publicPlaceBlockMessages[4], routeURL("login"), routeURL("register"))
	}
	projectFacts := func(value any, venue bool) []publicPlaceFact {
		var facts []publicPlaceFact
		for _, row := range d.rows(value) {
			f := publicPlaceFact{Key: d.text(row["key"]), Label: d.text(row["label"]), State: d.text(row["state"])}
			f.StateLabel, f.Class = tr("not recorded"), "muted"
			switch f.State {
			case "true":
				f.StateLabel, f.Class = tr("yes"), "fact--true"
			case "false":
				f.StateLabel, f.Class = tr("no"), "fact--false"
			case "limited":
				f.StateLabel, f.Class = tr("limited"), "fact--limited"
			case "unknown":
			default:
				d.fail()
			}
			if venue {
				f.FromMap = d.boolean(row["osm_sourced"])
				if !f.FromMap {
					yes, no, required := d.integer(row["yes"]), d.integer(row["no"]), d.integer(row["required"])
					if yes < 0 || no < 0 || required < 0 {
						d.fail()
					}
					f.Counts = block(publicPlaceBlockMessages[8], yes, no, required)
				}
				// Catalog.VenueFacts supplies a nullable *bool; the pending-place
				// JSON projection supplies nil/bool. Preserve both producer shapes.
				var mine *bool
				switch vote := row["my_vote"].(type) {
				case nil:
				case *bool:
					mine = vote
				case bool:
					copied := vote
					mine = &copied
				default:
					d.fail()
				}
				if mine != nil {
					f.VotedYes, f.VotedNo = *mine, !*mine
				}
			}
			facts = append(facts, f)
		}
		return facts
	}
	p.Access, p.Venue = projectFacts(data["access_facts"], false), projectFacts(data["venue_facts"], true)
	p.RecordedAccess, p.RecordedVenue = projectFacts(data["recorded_access_facts"], false), projectFacts(data["recorded_venue_facts"], false)
	for _, row := range d.rows(data["edges"]) {
		summary := d.row(row["summary"])
		e := publicPlaceEdge{Name: d.text(d.row(row["activity"])["name"]), URL: routeURL("edge_vote", p.ID, d.id(row["id"])), MyVote: d.optionalText(summary["my_vote"]), Confirmed: d.boolean(summary["is_confirmed"]), Disputed: d.boolean(row["is_disputed"])}
		if e.MyVote != "" && e.MyVote != "confirm" && e.MyVote != "dispute" {
			d.fail()
		}
		if e.Confirmed {
			e.Class = "fact--true"
		} else if e.Disputed {
			e.Class = "fact--false"
		} else {
			confirms, required, disputes := d.integer(summary["confirms"]), d.integer(summary["required"]), d.integer(summary["disputes"])
			if confirms < 0 || required < 0 || disputes < 0 {
				d.fail()
			}
			e.Counts = block(publicPlaceBlockMessages[5], confirms, required)
			if disputes > 0 {
				e.Disputes = block(publicPlaceBlockMessages[6], disputes)
			}
		}
		p.Edges = append(p.Edges, e)
	}
	for _, row := range d.rows(data["corrections"]) {
		confirms, required := d.integer(row["confirms"]), d.integer(row["required"])
		if confirms < 0 || required < 0 {
			d.fail()
		}
		p.Corrections = append(p.Corrections, publicPlaceCorrection{Label: d.text(row["field_label"]), Value: d.text(row["proposed_value"]), URL: routeURL("place_correction_confirm", p.ID, d.id(row["id"])), Proposer: d.boolean(row["is_proposer"]), Confirmed: d.boolean(row["confirmed_by_me"]), Counts: block(publicPlaceBlockMessages[5], confirms, required)})
	}
	if data["place_brief"] != nil {
		rows, ok := data["place_brief"].([][]any)
		if !ok {
			d.fail()
		} else {
			for _, pair := range rows {
				if len(pair) != 2 {
					d.fail()
					continue
				}
				p.Brief = append(p.Brief, publicPlaceBrief{d.text(pair[0]), d.text(pair[1])})
			}
		}
	}
	for _, row := range d.optionalRows(data["related_landings"]) {
		p.Related = append(p.Related, familyLink{Label: d.text(row["name"]), URL: notificationInboxSafeHref(d.text(row["url"]))})
	}
	if data["related_city"] != nil {
		city := d.row(data["related_city"])
		p.CityURL = notificationInboxSafeHref(d.text(city["url"]))
		p.CityNote = block(publicPlaceBlockMessages[9], d.text(city["name"]))
	}
	p.Share = publicNativeShareView{Share: publicNativeShareProjection(d, data, "place"), CSRF: d.optionalText(data["csrf"]), Next: request.URL.Path, Copy: map[string]string{}}
	for _, key := range publicDiscoveryMessages {
		p.Share.Copy[key] = tr(key)
	}
	if d.err != nil {
		return publicPlaceDetail{}, errors.New("invalid native public place snapshot")
	}
	return p, nil
}
