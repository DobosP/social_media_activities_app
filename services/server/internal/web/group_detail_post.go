package web

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func (r *Renderer) groupDetailPost(d *groupDetailDecoder, row map[string]any, actor platform.Actor, page groupDetailPage, replies, announcement bool, translate func(string) string) groupDetailPost {
	p := groupDetailPost{Copy: page.Copy, ID: d.id(row["id"]), AuthorID: d.id(row["author_id"]), Body: d.text(row["body"]), CSRF: page.Shell.CSRF, Member: page.Member, DissentControls: page.DissentControls}
	p.Author = d.person(row["author"], actor.ID, !announcement)
	if p.Author.ID != p.AuthorID {
		d.fail()
	}
	p.Mine = p.AuthorID == actor.ID
	if hidden := row["is_hidden"]; hidden != nil && d.boolean(hidden) {
		d.fail()
	}
	p.When, p.ISOTime = d.stamp(row["created_at"], "j M H:i"), d.stamp(row["created_at"], "c")
	p.Aria = strings.ReplaceAll(strings.ReplaceAll(translate("Message from %(author)s at %(at)s"), "%(author)s", p.Author.Name), "%(at)s", p.When)
	p.Lines = strings.Split(p.Body, "\n")
	p.ReportURL = routeURL("report") + "?type=post&id=" + strconv.FormatInt(p.ID, 10)
	if announcement {
		return p
	}
	p.Edited = d.optionalBool(row["is_edited"])
	p.Dissent, p.Concern = d.boolean(row["dissent_mine"]), d.boolean(row["concern_mine"])
	p.ConcernIntro = translate(groupDetailConcernIntro)
	// socialPostModels obtains the thread's activity/group IDs from the same
	// canonical thread. The XOR owner constraint makes activity ID zero for a
	// group, so its mention roster is empty; allowLinks is exactly actor.Cohort
	// == adult for every ordinary role. Metadata-only broadcasts use plain text
	// above and never enter this producer. Hidden rows are excluded before this
	// projection; snippet text is the producer's already-clamped safe text.
	if supplied := row["body_html"]; supplied != nil {
		value, ok := supplied.(*pongo2.Value)
		rendered := BodyMarkup(p.Body, map[string]bool{}, actor.Cohort == "adult")
		if !ok || value.String() != rendered {
			d.fail()
		} else {
			// Only the exact escape-first BodyMarkup result becomes trusted HTML.
			p.BodyHTML = template.HTML(rendered)
		}
	}
	p.ReplyURL = "?reply_to=" + strconv.FormatInt(p.ID, 10) + "#compose"
	p.ReactURL, p.DissentURL, p.ConcernURL = routeURL("group_post_react", page.ID, p.ID), routeURL("group_post_dissent", page.ID, p.ID), routeURL("group_post_concern", page.ID, p.ID)
	p.EditURL, p.DeleteURL = routeURL("group_post_edit", page.ID, p.ID), routeURL("group_post_delete", page.ID, p.ID)
	if snippet := row["snippet"]; snippet != nil {
		value := d.row(snippet)
		p.Snippet = &groupDetailSnippet{ID: d.id(value["pk"]), Author: d.text(value["author"]), Text: d.text(value["text"])}
		p.Snippet.Label = strings.ReplaceAll(translate("Replying to %(author)s"), "%(author)s", p.Snippet.Author)
	}
	if share := row["share"]; share != nil {
		value := d.row(share)
		p.Share = &groupDetailShare{Kind: d.text(value["kind"])}
		switch p.Share.Kind {
		case "activity", "place", "event":
			obj := d.row(value["obj"])
			p.Share.URL = routeURL(p.Share.Kind+"_detail", d.id(obj["id"]))
			if p.Share.Kind == "place" {
				p.Share.Title, p.Share.Place = d.text(obj["name"]), d.optionalText(obj["address_city"])
			} else {
				p.Share.Title, p.Share.When = d.text(obj["title"]), d.stamp(obj["starts_at"], "D j M, H:i")
				if place := obj["place"]; place != nil {
					p.Share.Place = d.optionalText(d.row(place)["name"])
				}
			}
		case "gone":
		default:
			d.fail()
		}
	}
	for _, value := range d.optionalRows(row["attachment_list"]) {
		attachment := groupDetailAttachment{Kind: d.text(value["kind"]), Blocked: d.boolean(value["blocked"]), Processing: d.boolean(value["processing"]), Failed: d.boolean(value["failed"]), Expired: d.boolean(value["expired"]), Filename: d.text(value["original_filename"])}
		if attachment.Kind != "image" && attachment.Kind != "video" && attachment.Kind != "file" {
			d.fail()
		}
		if !attachment.Blocked && !attachment.Processing && !attachment.Failed && !attachment.Expired {
			attachment.URL, attachment.Thumb, attachment.Poster = d.localMedia(value["url"]), d.localMedia(value["thumb_url"]), d.localMedia(value["poster_url"])
			if attachment.URL == "" {
				d.fail()
			}
			if attachment.Thumb == "" {
				attachment.Thumb = attachment.URL
			}
		}
		if expiry := groupDetailExpiry(d, value["expires_at"]); expiry != "" {
			attachment.Expiry = strings.ReplaceAll(translate("disappears %(when)s"), "%(when)s", expiry)
		}
		if attachment.Filename == "" {
			attachment.Filename = "document.pdf"
		}
		p.Media = append(p.Media, attachment)
	}
	p.Sentiment = append([]string(nil), d.strings(row["sentiment_lines"])...)
	reactions := d.strings(row["reaction_mine"])
	for _, facet := range page.ReactionFacets {
		f := facet
		for _, selected := range reactions {
			if selected == f.Slug {
				f.Mine = true
			}
		}
		p.Facets = append(p.Facets, f)
	}
	if nested := row["replies"]; nested != nil {
		children := d.rows(d.row(nested)["all"])
		if !replies && len(children) != 0 || len(children) > 900 {
			d.fail()
		}
		if replies {
			for _, child := range children {
				p.Replies = append(p.Replies, r.groupDetailPost(d, child, actor, page, false, false, translate))
			}
		}
	}
	return p
}

func (r *Renderer) groupDetailActivity(d *groupDetailDecoder, row map[string]any, accent bool, translate func(string) string) groupDetailActivity {
	a := groupDetailActivity{Title: d.text(row["title"]), URL: routeURL("activity_detail", d.id(row["id"])), Type: d.text(d.row(row["activity_type"])["name"]), When: d.stamp(row["starts_at"], "D j M, H:i"), Reason: d.optionalText(row["rec_reason"]), Beginners: d.optionalBool(row["beginners_welcome"]), Guardian: d.optionalBool(row["guardian_accompanied"]), Accent: accent}
	a.BeginnersText, a.GuardianText = translate("beginners welcome"), translate("guardian-accompanied")
	if secondary := row["secondary_types"]; secondary != nil {
		for _, value := range d.rows(d.row(secondary)["all"]) {
			a.Secondary = append(a.Secondary, d.text(value["name"]))
		}
	}
	if band := d.optionalText(row["cost_band"]); band != "" && band != "unspecified" {
		a.Cost = d.text(row["get_cost_band_display"])
	}
	if difficulty := d.optionalText(row["difficulty"]); difficulty != "" && difficulty != "unspecified" {
		a.Difficulty = d.text(row["get_difficulty_display"])
	}
	if status := d.optionalText(row["status"]); status != "" && status != "open" {
		a.Status = d.text(row["get_status_display"])
	}
	place := d.row(row["place"])
	a.Place, a.City = d.optionalText(place["display_name"]), d.optionalText(place["address_city"])
	if a.Place == "" {
		a.Place = translate("a place")
	}
	if value := row["match_pct"]; value != nil {
		n := d.integer(value)
		if n != 0 {
			a.Match = strings.ReplaceAll(translate("%(pct)s% match"), "%(pct)s", strconv.FormatInt(n, 10))
		}
	}
	if value := row["distance"]; value != nil {
		km, ok := d.row(value)["km"].(float64)
		if !ok || math.IsNaN(km) || math.IsInf(km, 0) {
			d.fail()
		} else {
			a.Distance = strings.ReplaceAll(translate("%(km)s km away"), "%(km)s", fmt.Sprintf("%.1f", km))
		}
	}
	words := strings.Fields(d.optionalText(row["description"]))
	if len(words) > 22 {
		a.Description = strings.Join(words[:22], " ") + "…"
	} else {
		a.Description = strings.Join(words, " ")
	}
	if accent {
		visual := d.row(row["visual"])
		if d.text(visual["kind"]) == "activity_cover_photo" {
			a.PhotoURL = d.localMedia(visual["url"])
			a.PhotoAlt = d.optionalText(visual["alt"])
			if a.PhotoAlt == "" {
				a.PhotoAlt = a.Title
			}
		} else {
			// The original activity_accent tag has this exact deterministic producer.
			a.AccentSVG = template.HTML(activitySVG(row).String())
		}
	}
	return a
}
