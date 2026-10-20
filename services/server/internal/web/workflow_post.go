package web

import (
	"html/template"
	"strconv"
	"strings"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
)

func (r *Renderer) workflowPost(d *groupDetailDecoder, row map[string]any, actor platform.Actor, page groupDetailPage, replies, announcement bool, translate func(string) string) groupDetailPost {
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
	// Copy the producer's already-authorized mention selection. Only an exact
	// escape-first BodyMarkup result can enter the existing HTML body context.
	roster := map[string]bool{}
	for _, name := range d.strings(row["workflow_body_mentions"]) {
		roster[name] = true
	}
	if supplied := row["body_html"]; supplied != nil {
		value, ok := supplied.(*pongo2.Value)
		rendered := BodyMarkup(p.Body, roster, actor.Cohort == "adult")
		if !ok || value.String() != rendered {
			d.fail()
		} else {
			// Only the exact escape-first BodyMarkup result becomes trusted HTML.
			p.BodyHTML = template.HTML(rendered)
		}
	}
	p.ReplyURL = "?reply_to=" + strconv.FormatInt(p.ID, 10) + "#compose"
	p.ReactURL, p.DissentURL, p.ConcernURL = routeURL("activity_post_react", page.ID, p.ID), routeURL("activity_post_dissent", page.ID, p.ID), routeURL("activity_post_concern", page.ID, p.ID)
	p.EditURL, p.DeleteURL = routeURL("activity_post_edit", page.ID, p.ID), routeURL("activity_post_delete", page.ID, p.ID)
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
				p.Replies = append(p.Replies, r.workflowPost(d, child, actor, page, false, false, translate))
			}
		}
	}
	return p
}
