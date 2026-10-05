package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
)

var publicActionNames = map[string]bool{"place_propose": true, "place_confirm": true, "edge_vote": true, "fact_vote": true, "place_correction_propose": true, "place_correction_confirm": true, "place_open_now_report": true, "place_open_now_reset": true, "place_closure_report": true, "place_closure_reset": true, "place_claim": true, "place_official_image": true, "event_report": true, "event_report_reset": true}

func init() {
	formPages["place_claim"] = true
	for name := range publicActionNames {
		actions[name] = actionSpec{}
		if name != "place_propose" && name != "place_claim" && name != "place_official_image" {
			actionOnly[name] = true
		}
	}
}
func (s *Server) publicRenderError(w http.ResponseWriter, r *http.Request, a platform.Actor, name, message string) {
	data, template, _, err := s.PublicView(r, a, name)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	data["messages"] = []socialMessage{{"message": message, "tags": "error"}}
	if form := spaMap(data["form"]); len(form) > 0 {
		form["non_field_errors"] = pongoErrorPrefix(message, "")
	}
	if s.Auth != nil {
		data["csrf"] = s.Auth.EnsureCSRF(w, r)
	}
	if err = s.Renderer.Render(w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}

// PublicAction runs after the common authenticated same-origin CSRF gate and
// calls the same domain mutations as the JSON API. Child consent, publication,
// quorum and media gates remain in those services.
func (s *Server) PublicAction(w http.ResponseWriter, r *http.Request, a platform.Actor, name string) bool {
	if !publicActionNames[name] {
		return false
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		platform.Error(w, 405, "Submit this form with POST.")
		return true
	}
	if a.ID <= 0 || !a.IsActive {
		platform.Fail(w, platform.ErrForbidden)
		return true
	}
	if r.ParseForm() != nil {
		platform.Fail(w, platform.ErrInvalid)
		return true
	}
	ctx := r.Context()
	place := id(r, "pk")
	target := routeURL("place_detail", place)
	view := "place_detail"
	var err error
	switch name {
	case "place_propose":
		view = "place_propose"
		if gate := platform.Participate(ctx, s.DB, a); gate != nil {
			if errors.Is(gate, platform.ErrForbidden) {
				http.Redirect(w, r, "/profile/", 302)
			} else {
				platform.Fail(w, gate)
			}
			return true
		}
		in := social.PlaceProposalInput{Name: strings.TrimSpace(r.PostForm.Get("name")), AllowNearby: publicChecked(r.PostForm.Get("allow_nearby"))}
		in.Lon, err = strconv.ParseFloat(r.PostForm.Get("lon"), 64)
		if err == nil {
			in.Lat, err = strconv.ParseFloat(r.PostForm.Get("lat"), 64)
		}
		if err == nil {
			in.ActivityType, err = strconv.ParseInt(r.PostForm.Get("activity_type"), 10, 64)
		}
		if err != nil || math.IsNaN(in.Lon) || math.IsNaN(in.Lat) || math.IsInf(in.Lon, 0) || math.IsInf(in.Lat, 0) {
			err = platform.ErrInvalid
			break
		}
		var proposal int64
		proposal, err = s.Social.ProposePlace(ctx, a, in)
		if err == nil {
			var venue int64
			err = s.DB.QueryRow(ctx, `SELECT place_id FROM social_userplaceproposal WHERE id=$1`, proposal).Scan(&venue)
			target = routeURL("place_detail", venue)
			if r.PostForm.Get("return_to") == "organize" {
				target = routeURL("activity_create") + "?place=" + fmt.Sprint(venue)
			}
		} else {
			var duplicate *social.DuplicatePlace
			if errors.As(err, &duplicate) && !duplicate.Soft {
				http.Redirect(w, r, routeURL("place_detail", duplicate.ID), 302)
				return true
			}
		}
	case "place_confirm":
		target, view = routeURL("places_pending"), "places_pending"
		err = s.Social.ConfirmPlace(ctx, a, id(r, "proposal_id"))
	case "edge_vote":
		var matching bool
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_placeactivity WHERE id=$1 AND place_id=$2)`, id(r, "edge_id"), place).Scan(&matching)
		if err == nil && !matching {
			err = platform.ErrNotFound
		}
		if err == nil {
			err = s.Catalog.VoteEdge(ctx, a, id(r, "edge_id"), r.PostForm.Get("vote"))
		}
	case "fact_vote":
		err = s.Catalog.VoteFact(ctx, a, place, r.PostForm.Get("fact_key"), r.PostForm.Get("value") == "yes")
	case "place_correction_propose":
		_, err = s.Catalog.ProposeCorrection(ctx, a, place, r.PostForm.Get("field"), r.PostForm.Get("proposed_value"))
	case "place_correction_confirm":
		var matching bool
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_placecorrection WHERE id=$1 AND place_id=$2)`, id(r, "correction_id"), place).Scan(&matching)
		if err == nil && !matching {
			err = platform.ErrNotFound
		}
		if err == nil {
			err = s.Catalog.ConfirmCorrection(ctx, a, id(r, "correction_id"))
		}
	case "place_open_now_report", "place_closure_report":
		_, err = s.Catalog.ReportVenue(ctx, a, place, name == "place_closure_report")
	case "place_open_now_reset", "place_closure_reset":
		if !a.IsStaff {
			err = platform.ErrNotFound
		} else {
			_, err = s.Catalog.ClearVenueReports(ctx, a, place, name == "place_closure_reset")
		}
	case "place_claim":
		view = "place_claim"
		in := catalog.ClaimInput{OrgName: r.PostForm.Get("org_name"), Kind: r.PostForm.Get("kind"), OfficialWebsite: r.PostForm.Get("official_website"), ContactEmail: r.PostForm.Get("contact_email"), CUI: r.PostForm.Get("cui"), Evidence: r.PostForm.Get("evidence")}
		if in.OfficialWebsite != "" {
			if !strings.Contains(in.OfficialWebsite, "://") {
				in.OfficialWebsite = "https://" + in.OfficialWebsite
			}
			u, e := url.Parse(in.OfficialWebsite)
			if e != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || utf8.RuneCountInString(in.OfficialWebsite) > 500 {
				err = platform.ErrInvalid
				break
			}
		}
		if in.ContactEmail != "" {
			address, e := mail.ParseAddress(in.ContactEmail)
			if e != nil || address.Address != in.ContactEmail {
				err = platform.ErrInvalid
				break
			}
		}
		_, err = s.Catalog.FileClaim(ctx, a, place, in)
	case "place_official_image":
		var allowed bool
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM places_place p WHERE p.id=$1 AND `+catalog.PolicyFromContext(r.Context()).PlaceSQL()+` AND ($2 OR EXISTS(SELECT 1 FROM places_placeclaim c JOIN places_partner partner ON partner.id=c.partner_id WHERE c.place_id=p.id AND c.claimant_id=$3 AND c.status='approved' AND c.kind='business' AND partner.is_verified AND partner.is_active AND partner.kind='business' AND partner.place_id=c.place_id)))`, place, a.IsStaff, a.ID).Scan(&allowed)
		if err == nil && !allowed {
			err = platform.ErrNotFound
		}
		if err != nil {
			break
		}
		path := ""
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			var cleanup func()
			path, _, cleanup, err = s.socialReadUpload(w, r, "image", 10<<20)
			defer cleanup()
			if err != nil {
				break
			}
		}
		if r.PostForm.Get("remove") != "" {
			err = s.Media.DeletePlaceCover(ctx, a, place)
			break
		}
		if path == "" {
			err = platform.ErrInvalid
			break
		}
		if !publicChecked(r.PostForm.Get("rights_confirmed")) || utf8.RuneCountInString(r.PostForm.Get("alt_text")) > 140 {
			err = platform.ErrInvalid
			break
		}
		_, err = s.Media.UploadPlaceCover(ctx, a, place, path, r.PostForm.Get("alt_text"))
	case "event_report":
		target, view = routeURL("event_detail", place), "event_detail"
		_, err = s.Catalog.ReportEvent(ctx, a, place, r.PostForm.Get("kind"))
	case "event_report_reset":
		target, view = routeURL("event_detail", place), "event_detail"
		if !a.IsStaff {
			err = platform.ErrNotFound
		} else {
			_, err = s.Catalog.ClearEventReports(ctx, a, place)
		}
	}
	if err != nil {
		if errors.Is(err, platform.ErrNotFound) || errors.Is(err, platform.ErrBusy) && r.Header.Get("X-Requested-With") == "fetch" {
			platform.Fail(w, err)
		} else if errors.Is(err, platform.ErrBusy) {
			s.publicRenderError(busyPage(w), r, a, view, mediaBusyMessage)
		} else {
			s.publicRenderError(w, r, a, view, err.Error())
		}
		return true
	}
	http.Redirect(w, r, target, 302)
	return true
}
func publicChecked(raw string) bool {
	return raw != "" && raw != "false" && raw != "False" && raw != "0"
}
