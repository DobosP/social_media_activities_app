package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5"
)

func pongoErrorPrefix(message string, value any) *pongo2.Value {
	return pongo2.AsSafeValue(`<ul class="errorlist"><li>` + escape(message) + `</li></ul>` + fmt.Sprint(value))
}

type socialMessage map[string]string

func (m socialMessage) String() string { return escape(m["message"]) }

func init() {
	// Register source routes which have no generic JSON-form equivalent.
	for _, name := range []string{"activity_photo", "activity_unsafe", "share_to_thread", "connection_respond", "connection_message"} {
		actions[name] = actionSpec{}
		actionOnly[name] = true
	}
}

func socialSafeNext(r *http.Request, fallback string) string {
	value := r.PostForm.Get("next")
	u, err := url.Parse(value)
	if err == nil && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && u.Host == "" && u.Scheme == "" && !strings.ContainsAny(value, "\\\r\n\x00") {
		return value
	}
	return fallback
}

func (s *Server) socialRenderError(w http.ResponseWriter, r *http.Request, a platform.Actor, name, message string) {
	s.socialRenderMessage(w, r, a, name, message, "error")
}

func (s *Server) socialRenderMessage(w http.ResponseWriter, r *http.Request, a platform.Actor, name, message, tags string) {
	data, template, handled, err := s.SocialView(r, a, name)
	if !handled {
		data, template, err = s.view(r, a, name)
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if target, ok := data["redirect"].(string); ok && target != "" {
		http.Redirect(w, r, target, 302)
		return
	}
	data["messages"] = []socialMessage{{"tags": tags, "message": message}}
	if form, ok := data["form"].(map[string]any); ok {
		form["errors"] = message
		form["non_field_errors"] = message
		form["as_p"] = pongoErrorPrefix(message, form["as_p"])
	}
	if s.Auth != nil {
		data["csrf"] = s.Auth.EnsureCSRF(w, r)
	} else {
		data["csrf"] = r.PostForm.Get("csrfmiddlewaretoken")
	}
	data["canonical_url"] = strings.TrimRight(s.Config.PublicURL, "/") + r.URL.Path
	if err = s.Renderer.Render(w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}

func socialActionError(err error) string {
	switch {
	case errors.Is(err, media.ErrBlocked):
		return "That file was blocked by safety screening."
	case errors.Is(err, media.ErrScanner):
		return "Safety screening is unavailable. Your file was not posted."
	case errors.Is(err, media.ErrRejected):
		return "That file couldn't be accepted."
	case errors.Is(err, platform.ErrForbidden):
		return "That action isn't available for your account."
	case errors.Is(err, platform.ErrInvalid):
		return "Check the form and try again."
	}
	return "This action is temporarily unavailable. Please try again."
}

// SocialAction receives a session-authenticated request only after the shared
// CSRF admission. It deliberately invokes the same native domain transactions
// as the API, while preserving source form names and HTML return paths.
func (s *Server) SocialAction(w http.ResponseWriter, r *http.Request, a platform.Actor, name string) bool {
	_, form := socialFormNames[name]
	known := form || strings.HasPrefix(name, "activity_") && name != "activity_log" || strings.HasPrefix(name, "group_") || strings.HasPrefix(name, "series_") || strings.HasPrefix(name, "gauge_") || name == "membership_vote" || name == "share_to_thread" || strings.HasPrefix(name, "connection_")
	if !known {
		return false
	}
	if s.Social == nil || s.DB == nil {
		platform.Error(w, 503, "Social actions unavailable.")
		return true
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		platform.Error(w, 405, "Use the form to submit this action.")
		return true
	}
	ctx := r.Context()
	pk := id(r, "pk")
	returnName := "activity_detail"
	kind := "activity"
	if strings.HasPrefix(name, "group_") {
		returnName, kind = "group_detail", "group"
	}
	if strings.HasPrefix(name, "series_") {
		returnName = "series_detail"
	}
	if strings.HasPrefix(name, "gauge_") {
		returnName = "gauge_detail"
	}
	if strings.HasPrefix(name, "connection_") {
		returnName = "connections"
	}
	if form {
		// Load the authorized form parent before validating posted fields.
		data, _, err := s.socialFormPage(r, a, name)
		if err != nil {
			platform.Fail(w, err)
			return true
		}
		if target, ok := data["redirect"].(string); ok && target != "" {
			http.Redirect(w, r, target, 302)
			return true
		}
		body, err := socialCleanForm(r, a, name)
		if err != nil {
			s.socialRenderError(w, r, a, name, err.Error())
			return true
		}
		created := int64(0)
		switch name {
		case "activity_create":
			var in social.ActivityInput
			err = socialDecodeBody(body, &in)
			if err == nil {
				created, err = s.Social.CreateActivity(ctx, a, in)
			}
			returnName = "activity_detail"
		case "series_create":
			var in social.SeriesInput
			err = socialDecodeBody(body, &in)
			if err == nil {
				created, err = s.Social.CreateSeries(ctx, a, in)
			}
			returnName = "series_detail"
		case "group_create":
			var in social.GroupInput
			err = socialDecodeBody(body, &in)
			if err == nil {
				created, err = s.Social.CreateGroup(ctx, a, in)
			}
			returnName = "group_detail"
		case "gauge_create":
			var in social.GaugeInput
			err = socialDecodeBody(body, &in)
			if err == nil {
				created, err = s.Social.ProposeGauge(ctx, a, in)
			}
			returnName = "gauge_detail"
		case "gauge_convert":
			var in social.GaugeConversion
			err = socialDecodeBody(body, &in)
			if err == nil {
				created, err = s.Social.ConvertGauge(ctx, a, pk, in)
			}
			returnName = "activity_detail"
		case "activity_edit":
			activity := spaMap(data["activity"])
			place, _ := body["place"].(*int)
			delete(body, "place")
			delete(body, "activity_type")
			changes := map[string]json.RawMessage{}
			for key, value := range body {
				raw, e := json.Marshal(value)
				if e != nil {
					err = e
					break
				}
				changes[key] = raw
			}
			if err == nil && place != nil && int64(*place) != int64(spaInt(activity["place_id"])) {
				err = s.Social.MoveActivity(ctx, a, pk, int64(*place))
			}
			if err == nil {
				err = s.Social.UpdateActivity(ctx, a, pk, changes)
			}
			returnName = "activity_detail"
		}
		if err != nil {
			s.socialRenderError(w, r, a, name, socialActionError(err))
			return true
		}
		if created > 0 {
			pk = created
		}
		http.Redirect(w, r, routeURL(returnName, pk), 302)
		return true
	}
	// Parent visibility is established before all identifier-addressed actions.
	var parent map[string]any
	if strings.HasPrefix(name, "activity_") || name == "membership_vote" {
		var err error
		parent, err = s.socialActivity(ctx, a, pk)
		if err != nil {
			platform.Fail(w, err)
			return true
		}
	} else if strings.HasPrefix(name, "group_") {
		raw, err := s.Social.Group(ctx, a, pk)
		if err != nil {
			platform.Fail(w, err)
			return true
		}
		parent = rawObject(raw)
		if name == "group_post" && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			var thread int64
			if err = s.DB.QueryRow(ctx, `SELECT id FROM social_thread WHERE group_id=$1`, pk).Scan(&thread); err != nil {
				platform.Fail(w, err)
				return true
			}
			parent["thread"] = map[string]any{"id": thread}
		}
	} else if strings.HasPrefix(name, "series_") {
		if _, err := s.Social.Series(ctx, a, pk); err != nil {
			platform.Fail(w, err)
			return true
		}
	} else if strings.HasPrefix(name, "gauge_") {
		if _, err := s.Social.Gauge(ctx, a, pk); err != nil {
			platform.Fail(w, err)
			return true
		}
	}
	if strings.Contains(name, "_post_") {
		var matches bool
		table, column := "social_activity", "activity_id"
		if kind == "group" {
			table, column = "social_group", "group_id"
		}
		err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_post p JOIN social_thread t ON t.id=p.thread_id JOIN `+table+` owner ON owner.id=t.`+column+` WHERE p.id=$1 AND owner.id=$2)`, id(r, "post_id"), pk).Scan(&matches)
		if err != nil {
			platform.Fail(w, err)
			return true
		}
		if !matches {
			platform.Fail(w, platform.ErrNotFound)
			return true
		}
	}
	var err error
	mine := false
	postID := int64(0)
	target := routeURL(returnName, pk)
	switch name {
	case "activity_unsafe":
		var eligible bool
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_membership m JOIN social_activity act ON act.id=m.activity_id WHERE act.id=$2 AND m.user_id=$1 AND m.state='member' AND m.role<>'guardian' AND act.owner_id<>$1)`, a.ID, pk).Scan(&eligible)
		if err == nil && !eligible {
			err = platform.ErrForbidden
		}
		if err == nil {
			if s.Safety == nil {
				err = errors.New("safety unavailable")
			} else {
				result, e := s.Safety.UnsafeReport(ctx, a, pk)
				err = e
				message := "Thank you for telling us. A moderator has been alerted. You can leave this activity any time."
				if errors.Is(err, safety.ErrRate) {
					err = nil
					message = "We've got your earlier alert and a moderator is looking. If you're in danger right now, tell a trusted adult or call your local emergency number."
				} else if result.Repeat {
					message = "Thanks — we already have your alert and a moderator is looking. You can leave this activity any time."
				} else if result.GuardiansAlerted > 0 {
					message = "Thank you for telling us. A moderator has been alerted, and the grown-ups who look after you have been told. You can leave this activity any time."
				}
				if err == nil {
					s.socialRenderMessage(w, r, a, "activity_detail", message, "success")
					return true
				}
			}
		}
	case "activity_join":
		_, err = s.Social.Join(ctx, a, pk)
	case "activity_leave":
		_, err = s.Social.Leave(ctx, a, pk)
	case "activity_cancel":
		err = s.Social.CancelActivity(ctx, a, pk, r.PostForm.Get("reason"))
	case "activity_set_supervision":
		err = s.Social.SetSupervision(ctx, a, pk, r.PostForm.Get("supervised") == "on")
	case "activity_add_supervisor":
		n, e := socialPositive(r.PostForm.Get("user_id"), true)
		err = e
		if err == nil {
			_, err = s.Social.AddGuardian(ctx, a, pk, int64(*n))
		}
	case "activity_grant_coorg", "activity_revoke_coorg", "activity_transfer_owner":
		n, e := socialPositive(r.PostForm.Get("user_id"), true)
		err = e
		action := map[string]string{"activity_grant_coorg": "grant_organizer", "activity_revoke_coorg": "revoke_organizer", "activity_transfer_owner": "transfer"}[name]
		if err == nil {
			_, err = s.Social.ChangeOrganizer(ctx, a, pk, int64(*n), action)
		}
	case "activity_rsvp":
		err = s.Social.RSVP(ctx, a, pk, r.PostForm.Get("intent"))
	case "activity_support_companion":
		err = s.Social.SupportCompanion(ctx, a, pk, r.PostForm.Get("brings") == "on")
	case "activity_met":
		err = s.Social.MetConfirmed(ctx, a, pk, r.PostForm.Get("met") != "no")
	case "activity_arrived", "activity_transit", "activity_departing":
		_, err = s.Social.Presence(ctx, a, pk, strings.TrimPrefix(name, "activity_"), r.PostForm.Get("status"))
	case "activity_listing_toggle":
		err = s.Social.SetActivityListing(ctx, a, pk, r.PostForm.Get("listed") == "1")
	case "membership_vote":
		var matches bool
		err = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE id=$1 AND activity_id=$2)`, id(r, "membership_id"), pk).Scan(&matches)
		if err == nil && !matches {
			platform.Fail(w, platform.ErrNotFound)
			return true
		}
		if err == nil {
			err = s.Social.Vote(ctx, a, id(r, "membership_id"), r.PostForm.Get("vote") == "approve", false)
		}
	case "group_join":
		err = s.Social.JoinGroup(ctx, a, pk)
	case "group_leave":
		err = s.Social.LeaveGroup(ctx, a, pk)
	case "group_archive":
		err = s.Social.ArchiveGroup(ctx, a, pk)
		target = "/communities/"
	case "group_listing_toggle":
		err = s.Social.SetGroupListing(ctx, a, pk, r.PostForm.Get("listed") == "1")
	case "group_ask":
		_, err = s.Social.AskGroup(ctx, a, pk, r.PostForm.Get("prompt"))
	case "series_pause", "series_resume", "series_end":
		err = s.Social.TransitionSeries(ctx, a, pk, strings.TrimPrefix(name, "series_"))
	case "series_set_next_note":
		note := strings.TrimSpace(r.PostForm.Get("next_instance_note"))
		if utf8.RuneCountInString(note) > 500 {
			err = platform.ErrInvalid
		} else {
			err = s.Social.SetSeriesNote(ctx, a, pk, note)
		}
	case "gauge_interested", "gauge_uninterested":
		err = s.Social.MarkGauge(ctx, a, pk, name == "gauge_interested")
	case "activity_post", "group_post", "activity_announce", "group_announce":
		var path, filename string
		var cleanup func()
		cleanup = func() {}
		if (name == "activity_post" || name == "group_post") && strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			write, e := s.Social.CanWriteThread(ctx, s.DB, a, spaID(spaMap(parent["thread"])))
			if e != nil {
				err = e
			} else if !write {
				err = platform.ErrForbidden
			} else {
				path, filename, cleanup, err = s.socialReadUpload(w, r, "attachment", 80<<20)
			}
		}
		defer cleanup()
		in := social.PostInput{Body: strings.TrimSpace(r.PostForm.Get("body")), Ping: r.PostForm.Get("ping") == "on"}
		if raw := r.PostForm.Get("reply_to"); raw != "" {
			n, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || n < 1 {
				err = platform.ErrInvalid
			} else {
				in.ReplyTo = &n
			}
		}
		if err == nil && path != "" {
			if s.Media == nil {
				err = media.ErrProcessing
			} else {
				var ttl *int64
				if raw := r.PostForm.Get("disappear"); raw != "" {
					n, e := strconv.ParseInt(raw, 10, 64)
					if e != nil || !s.Media.ValidDisappearanceOption(a.Cohort, n) {
						err = platform.ErrInvalid
					} else {
						ttl = &n
					}
				}
				if err == nil {
					var prepared *media.PreparedAttachment
					prepared, err = s.Media.PrepareThreadAttachment(ctx, a, spaID(spaMap(parent["thread"])), path, filename, ttl)
					if err == nil {
						defer prepared.Finish(ctx, false)
						postID, err = s.Social.WritePostAttached(ctx, a, kind, pk, in, false, prepared.Publish)
						prepared.Finish(ctx, err == nil)
					}
				}
			}
		}
		if err == nil && path == "" {
			postID, err = s.Social.WritePost(ctx, a, kind, pk, in, strings.HasSuffix(name, "_announce"))
		}
	case "activity_photo":
		var path string
		var cleanup func()
		write, e := s.Social.CanWriteThread(ctx, s.DB, a, spaID(spaMap(parent["thread"])))
		if e != nil {
			err = e
		} else if !write {
			err = platform.ErrForbidden
		} else {
			path, _, cleanup, err = s.socialReadUpload(w, r, "image", 5<<20)
			if cleanup != nil {
				defer cleanup()
			}
			if err == nil && path != "" {
				if s.Media == nil {
					err = media.ErrProcessing
				} else {
					_, err = s.Media.UploadPhoto(ctx, a, "thread", spaID(spaMap(parent["thread"])), path)
				}
			}
		}
	case "activity_post_edit", "group_post_edit":
		err = s.Social.EditPost(ctx, a, id(r, "post_id"), r.PostForm.Get("body"))
	case "activity_post_delete", "group_post_delete":
		var moderationHidden bool
		moderationHidden, err = s.Social.DeletePost(ctx, a, id(r, "post_id"))
		if errors.Is(err, social.ErrPendingPostAppeal) {
			s.redirectPostNotice(w, r, a, target, "p")
			return true
		}
		if err == nil && moderationHidden {
			s.redirectPostNotice(w, r, a, target, "d")
			return true
		}
	case "activity_post_react", "group_post_react", "activity_post_dissent", "group_post_dissent", "activity_post_concern", "group_post_concern":
		suffix := name[strings.LastIndex(name, "_")+1:]
		if suffix == "react" {
			suffix = "reaction"
		}
		mine, err = s.Social.ToggleSentiment(ctx, a, id(r, "post_id"), suffix, r.PostForm.Get("emoji"))
		target += "#post-" + r.PathValue("post_id")
	case "share_to_thread":
		kind := r.PostForm.Get("kind")
		n, e := strconv.ParseInt(r.PostForm.Get("obj_id"), 10, 64)
		dest, e2 := strconv.ParseInt(r.PostForm.Get("target"), 10, 64)
		if e != nil || e2 != nil || n < 1 || dest < 1 || kind != "activity" && kind != "place" && kind != "event" {
			platform.Fail(w, platform.ErrNotFound)
			return true
		}
		if _, e = s.Social.Activity(ctx, a, dest); e != nil {
			platform.Fail(w, e)
			return true
		}
		note := []rune(strings.TrimSpace(r.PostForm.Get("note")))
		if len(note) > 280 {
			note = note[:280]
		}
		in := social.PostInput{Body: string(note)}
		switch kind {
		case "activity":
			in.ShareActivity = &n
		case "place":
			in.SharePlace = &n
		case "event":
			in.ShareEvent = &n
		}
		postID, err = s.Social.WritePost(ctx, a, "activity", dest, in, false)
		target = fmt.Sprintf("/activities/%d/#post-%d", dest, postID)
		if err != nil {
			target = socialSafeNext(r, "/")
			returnName = "home"
		}
	case "connection_request":
		_, err = s.Social.RequestConnection(ctx, a, r.PostForm.Get("public_id"))
		target = socialSafeNext(r, "/connections/")
	case "connection_respond":
		action := "decline"
		if r.PostForm.Get("accept") == "1" {
			action = "accept"
		}
		err = s.Social.RespondConnection(ctx, a, pk, action)
		target = "/connections/"
	case "connection_withdraw":
		err = s.Social.RespondConnection(ctx, a, pk, "withdraw")
		target = "/connections/"
	case "connection_remove":
		err = s.Social.RemoveConnection(ctx, a, r.PostForm.Get("public_id"))
		target = "/connections/"
	case "connection_message":
		var username string
		err = s.DB.QueryRow(ctx, `SELECT u.username FROM accounts_user u WHERE u.public_id::text=$2 AND u.cohort=$3 AND u.is_active AND EXISTS(SELECT 1 FROM connections_connection c WHERE c.status='accepted' AND ((c.requester_id=$1 AND c.addressee_id=u.id) OR (c.addressee_id=$1 AND c.requester_id=u.id))) AND `+socialBlockUser, a.ID, r.PostForm.Get("public_id"), a.Cohort).Scan(&username)
		if err == nil {
			if s.Messaging == nil {
				err = errors.New("messaging unavailable")
			} else {
				_, err = s.Messaging.Start(ctx, a, "direct", []string{username}, "")
			}
		}
		target = "/messages/"
	default:
		return false
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, platform.ErrNotFound) {
			platform.Fail(w, platform.ErrNotFound)
			return true
		}
		if r.Header.Get("X-Requested-With") == "fetch" && strings.Contains(name, "_post_") {
			platform.JSON(w, 400, map[string]any{"ok": false, "detail": socialActionError(err)})
			return true
		}
		s.socialRenderError(w, r, a, returnName, socialActionError(err))
		return true
	}
	if r.Header.Get("X-Requested-With") == "fetch" && (strings.HasSuffix(name, "_react") || strings.HasSuffix(name, "_dissent") || strings.HasSuffix(name, "_concern")) {
		var value any = mine
		if strings.HasSuffix(name, "_react") {
			rows, e := s.DB.Query(ctx, `SELECT emoji FROM social_postreaction WHERE post_id=$1 AND user_id=$2 ORDER BY emoji`, id(r, "post_id"), a.ID)
			if e != nil {
				platform.Fail(w, e)
				return true
			}
			facets := []string{}
			for rows.Next() {
				var facet string
				if e = rows.Scan(&facet); e != nil {
					rows.Close()
					platform.Fail(w, e)
					return true
				}
				facets = append(facets, facet)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				platform.Fail(w, e)
				return true
			}
			sort.Strings(facets)
			value = facets
		}
		platform.JSON(w, 200, map[string]any{"ok": true, "mine": value})
		return true
	}
	_ = postID
	http.Redirect(w, r, target, 302)
	return true
}

// Read a legacy upload without ParseMultipartForm's process-default temporary
// directory. All bytes are bounded and go only to the configured private media
// scratch; no request filename ever contributes to a server filesystem path.
func (s *Server) socialReadUpload(w http.ResponseWriter, r *http.Request, field string, cap int64) (path, filename string, cleanup func(), err error) {
	cleanup = func() {
		if path != "" {
			_ = os.Remove(path)
		}
	}
	if s.Config.UploadScratch == "" {
		return "", "", cleanup, media.ErrProcessing
	}
	r.Body = http.MaxBytesReader(w, r.Body, cap+(1<<20))
	reader, e := r.MultipartReader()
	if e != nil {
		return "", "", cleanup, platform.ErrInvalid
	}
	values := url.Values{}
	formBudget := platform.NewUploadBudget(r.Context())
	defer func() {
		if err != nil {
			cleanup()
		}
	}()
	for count := 0; ; count++ {
		if count >= 16 {
			return path, filename, cleanup, platform.ErrInvalid
		}
		part, e := reader.NextPart()
		if e == io.EOF {
			break
		}
		if e != nil {
			return path, filename, cleanup, platform.ErrInvalid
		}
		name := part.FormName()
		if name == "" || len(name) > 40 {
			part.Close()
			return path, filename, cleanup, platform.ErrInvalid
		}
		if part.FileName() != "" {
			if name != field || path != "" {
				part.Close()
				return path, filename, cleanup, platform.ErrInvalid
			}
			file, e := os.CreateTemp(s.Config.UploadScratch, "legacy-upload-")
			if e != nil {
				part.Close()
				return path, filename, cleanup, media.ErrProcessing
			}
			path = file.Name()
			filename = filepath.Base(strings.ReplaceAll(part.FileName(), "\\", "/"))
			n, e := io.Copy(file, io.LimitReader(part, cap+1))
			closeErr := file.Close()
			part.Close()
			if e != nil || closeErr != nil || n < 1 || n > cap {
				return path, filename, cleanup, media.ErrRejected
			}
		} else {
			if values.Has(name) {
				part.Close()
				return path, filename, cleanup, platform.ErrInvalid
			}
			raw, e := formBudget.ReadField(part, 16384)
			part.Close()
			if e != nil || len(raw) > 16384 {
				return path, filename, cleanup, platform.ErrInvalid
			}
			if name != field {
				values.Set(name, string(raw))
			}
		}
	}
	r.PostForm = values
	r.Form = values
	return path, filename, cleanup, nil
}
