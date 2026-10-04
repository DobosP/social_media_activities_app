package web

import (
	"encoding/json"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type actionSpec struct{ Method, Path, Return string }

var actions = map[string]actionSpec{
	"activity_create": {"POST", "/api/social/activities/", "activity_detail"}, "activity_edit": {"PATCH", "/api/social/activities/{pk}/", "activity_detail"},
	"activity_cancel": {"POST", "/api/social/activities/{pk}/cancel/", "activity_detail"}, "activity_announce": {"POST", "/api/social/activities/{pk}/announce/", "activity_detail"},
	"activity_add_supervisor": {"POST", "/api/social/activities/{pk}/guardians/", "activity_detail"}, "activity_set_supervision": {"POST", "/api/social/activities/{pk}/supervision/", "activity_detail"},
	"activity_grant_coorg": {"POST", "/api/social/activities/{pk}/grant_organizer/", "activity_detail"}, "activity_revoke_coorg": {"POST", "/api/social/activities/{pk}/revoke_organizer/", "activity_detail"}, "activity_transfer_owner": {"POST", "/api/social/activities/{pk}/transfer/", "activity_detail"},
	"activity_rsvp": {"POST", "/api/social/activities/{pk}/rsvp/", "activity_detail"}, "activity_support_companion": {"POST", "/api/social/activities/{pk}/support_companion/", "activity_detail"}, "activity_met": {"POST", "/api/social/activities/{pk}/met_confirmed/", "activity_detail"},
	"activity_arrived": {"POST", "/api/social/activities/{pk}/arrived/", "activity_detail"}, "activity_transit": {"POST", "/api/social/activities/{pk}/transit/", "activity_detail"}, "activity_departing": {"POST", "/api/social/activities/{pk}/departing/", "activity_detail"}, "activity_join": {"POST", "/api/social/activities/{pk}/join/", "activity_detail"}, "activity_leave": {"POST", "/api/social/activities/{pk}/leave/", "activity_detail"},
	"activity_post": {"POST", "/api/social/activities/{pk}/posts/", "activity_detail"}, "activity_post_edit": {"PATCH", "/api/social/posts/{post_id}/", "activity_detail"}, "activity_post_delete": {"DELETE", "/api/social/posts/{post_id}/", "activity_detail"}, "activity_post_react": {"POST", "/api/social/posts/{post_id}/reaction/", "activity_detail"}, "activity_post_dissent": {"POST", "/api/social/posts/{post_id}/dissent/", "activity_detail"}, "activity_post_concern": {"POST", "/api/social/posts/{post_id}/concern/", "activity_detail"},
	"membership_vote": {"POST", "/api/social/memberships/{membership_id}/vote/", "activity_detail"}, "activity_listing_toggle": {"POST", "/api/social/activities/{pk}/set_public_listing/", "activity_detail"},
	"series_create": {"POST", "/api/social/series/", "series_detail"}, "series_pause": {"POST", "/api/social/series/{pk}/pause/", "series_detail"}, "series_resume": {"POST", "/api/social/series/{pk}/resume/", "series_detail"}, "series_end": {"POST", "/api/social/series/{pk}/end/", "series_detail"}, "series_set_next_note": {"POST", "/api/social/series/{pk}/next_note/", "series_detail"},
	"group_create": {"POST", "/api/social/groups/", "group_detail"}, "group_join": {"POST", "/api/social/groups/{pk}/join/", "group_detail"}, "group_leave": {"POST", "/api/social/groups/{pk}/leave/", "group_detail"}, "group_archive": {"POST", "/api/social/groups/{pk}/archive/", "group_detail"}, "group_listing_toggle": {"POST", "/api/social/groups/{pk}/set_public_listing/", "group_detail"}, "group_ask": {"POST", "/api/social/groups/{pk}/ask/", "group_detail"}, "group_announce": {"POST", "/api/social/groups/{pk}/announce/", "group_detail"}, "group_post": {"POST", "/api/social/groups/{pk}/posts/", "group_detail"},
	"group_post_edit": {"PATCH", "/api/social/posts/{post_id}/", "group_detail"}, "group_post_delete": {"DELETE", "/api/social/posts/{post_id}/", "group_detail"}, "group_post_react": {"POST", "/api/social/posts/{post_id}/reaction/", "group_detail"}, "group_post_dissent": {"POST", "/api/social/posts/{post_id}/dissent/", "group_detail"}, "group_post_concern": {"POST", "/api/social/posts/{post_id}/concern/", "group_detail"},
	"gauge_create": {"POST", "/api/social/gauges/", "gauge_detail"}, "gauge_interested": {"POST", "/api/social/gauges/{pk}/interested/", "gauge_detail"}, "gauge_uninterested": {"POST", "/api/social/gauges/{pk}/uninterested/", "gauge_detail"}, "gauge_convert": {"POST", "/api/social/gauges/{pk}/convert/", "activity_detail"},
	"place_propose": {"POST", "/api/social/place-proposals/", "places_pending"}, "place_confirm": {"POST", "/api/social/place-proposals/{proposal_id}/confirm/", "places_pending"},
	"connection_request": {"POST", "/api/connections/connections/request_to/", "connections"}, "connection_withdraw": {"POST", "/api/connections/connections/{pk}/withdraw/", "connections"}, "connection_remove": {"POST", "/api/connections/connections/remove/", "connections"},
	"guardian_invite_create": {"POST", "/api/accounts/guardian-links/", "wards"}, "guardian_invite_accept": {"POST", "/api/accounts/guardian-links/{token}/accept/", "home"}, "guardian_invite_decline": {"POST", "/api/accounts/guardian-links/{token}/decline/", "home"},
	"notifications_read_all": {"POST", "/api/notifications/read-all/", "notifications"}, "notification_preferences": {"PUT", "/api/accounts/me/settings/", "notification_preferences"}, "access_preferences": {"PUT", "/api/accounts/me/settings/", "access_preferences"},
	"avatar_style": {"POST", "/api/accounts/me/avatar-style/", "profile"}, "api_token_revoke": {"DELETE", "/api/auth/token/", "settings"},
	"report": {"POST", "/api/safety/reports/", "home"}, "block_user": {"POST", "/api/safety/blocks/", "home"}, "unblock_user": {"DELETE", "/api/safety/blocks/", "home"}, "safety_record_appeal": {"POST", "/api/safety/appeals/", "safety_record"},
	"donate": {"POST", "/api/donations/", "donate"}, "account_delete": {"DELETE", "/api/accounts/me/", "home"},
}
var formPages = map[string]bool{"activity_create": true, "activity_edit": true, "series_create": true, "group_create": true, "gauge_create": true, "gauge_convert": true, "place_propose": true, "notification_preferences": true, "access_preferences": true, "avatar_style": true, "report": true, "donate": true, "account_delete": true}

func init() {
	for name := range actions {
		if !formPages[name] {
			actionOnly[name] = true
		}
	}
}
func substitute(path string, r *http.Request) string {
	for _, key := range []string{"pk", "post_id", "membership_id", "proposal_id", "token", "ward_pk", "edge_id", "correction_id"} {
		path = strings.ReplaceAll(path, "{"+key+"}", r.PathValue(key))
	}
	return path
}
func formBody(r *http.Request) map[string]any {
	body := map[string]any{}
	for key, values := range r.PostForm {
		if key == "csrfmiddlewaretoken" || len(values) == 0 {
			continue
		}
		value := values[len(values)-1]
		body[key] = value
		switch key {
		case "place", "activity_type", "capacity", "min_to_go", "max_to_go", "user_id", "target_id", "decision_id", "generation", "amount_cents", "campaign", "proposal_id", "reply_to", "parent", "ward_pk", "required_confirmations", "max_open_joins":
			if value == "" {
				body[key] = nil
			} else if v, err := strconv.ParseInt(value, 10, 64); err == nil {
				body[key] = v
			}
		case "approve", "approved", "brings", "supervised", "confirmed", "listed", "beginners_welcome", "guardian_accompanied", "recurring":
			body[key] = value == "on" || value == "true" || value == "yes" || value == "1"
		case "starts_at", "ends_at", "next_at", "first_starts_at":
			if value == "" {
				body[key] = nil
				continue
			}
			zone, _ := time.LoadLocation("Europe/Bucharest")
			for _, format := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
				if t, err := time.ParseInLocation(format, value, zone); err == nil {
					body[key] = t.Format(time.RFC3339)
					break
				}
			}
		case "secondary_types":
			items := []int64{}
			for _, v := range values {
				if parsed, err := strconv.ParseInt(v, 10, 64); err == nil {
					items = append(items, parsed)
				}
			}
			body[key] = items
		}
	}
	return body
}
func (s *Server) action(w http.ResponseWriter, r *http.Request, name string) {
	actor, logged := platform.ActorFrom(r)
	if !logged && name != "donate" && name != "display_preferences" {
		http.Redirect(w, r, "/login/", 302)
		return
	}
	if r.ParseForm() != nil {
		platform.Error(w, 400, "Invalid form.")
		return
	}
	if s.Auth.CheckCSRF(formCSRF(r)) != nil {
		platform.Error(w, 403, "CSRF verification failed.")
		return
	}
	if name == "donate" {
		s.donateAction(w, r, actor)
		return
	}
	if s.AccountAction(w, r, actor, name) {
		return
	}
	if s.PublicAction(w, r, actor, name) {
		return
	}
	if s.SocialAction(w, r, actor, name) {
		return
	}
	spec, exists := actions[name]
	if !exists {
		platform.Error(w, 404, "Not found.")
		return
	}
	body := formBody(r)
	if strings.Contains(name, "post_") {
		kind := "activity"
		table := "social_activity"
		if strings.HasPrefix(name, "group_") {
			kind = "group"
			table = "social_group"
		}
		var matches bool
		if err := s.DB.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM social_post p JOIN social_thread t ON t.id=p.thread_id JOIN `+table+` x ON x.id=t.`+kind+`_id WHERE p.id=$1 AND x.id=$2)`, id(r, "post_id"), id(r, "pk")).Scan(&matches); err != nil {
			platform.Fail(w, err)
			return
		}
		if !matches {
			platform.Fail(w, platform.ErrNotFound)
			return
		}
	}
	switch name {
	case "block_user", "unblock_user":
		body = map[string]any{"user_id": id(r, "pk")}
	case "activity_support_companion":
		body["brings"] = r.PostForm.Get("brings") == "on"
	case "activity_set_supervision":
		body["supervised"] = r.PostForm.Get("supervised") == "on"
	case "activity_listing_toggle", "group_listing_toggle":
		body = map[string]any{"listed": r.PostForm.Get("listed") == "on"}
	case "group_ask":
		body = map[string]any{"prompt": r.PostForm.Get("prompt")}
	case "guardian_invite_create":
		body = map[string]any{"username": r.PostForm.Get("ward_username"), "relationship": r.PostForm.Get("relationship")}
	case "notification_preferences":
		body = map[string]any{"muted_kinds": r.PostForm["muted"]}
	case "access_preferences":
		access := map[string]bool{}
		for _, key := range []string{"needs_step_free", "needs_accessible_toilet", "needs_hearing_loop", "prefers_quiet"} {
			access[key] = r.PostForm.Get(key) == "on"
		}
		body = map[string]any{"access": access}
	case "account_delete":
		if r.PostForm.Get("confirm") != "DELETE" && r.PostForm.Get("confirm") != "on" {
			platform.Error(w, 400, "Confirm account deletion.")
			return
		}
		body = map[string]any{}
	}
	_ = actor
	value, response, err := s.call(r, spec.Method, substitute(spec.Path, r), body)
	if response != nil {
		for _, cookie := range response.Header().Values("Set-Cookie") {
			w.Header().Add("Set-Cookie", cookie)
		}
	}
	wantsJSON := r.Header.Get("X-Requested-With") == "fetch"
	if err != nil {
		if response != nil {
			for key, values := range response.Header() {
				if key != "Set-Cookie" {
					w.Header()[key] = values
				}
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(response.Body.Bytes())
			return
		}
		platform.Fail(w, err)
		return
	}
	if wantsJSON {
		result := object(value)
		if added, ok := result["added"]; ok {
			result["mine"] = added
		}
		result["ok"] = true
		platform.JSON(w, 200, result)
		return
	}
	if name == "donate" {
		if checkout, ok := object(value)["checkout_url"].(string); ok {
			http.Redirect(w, r, checkout, 303)
			return
		}
	}
	pk := r.PathValue("pk")
	if strings.HasSuffix(name, "_create") || name == "gauge_convert" {
		if created, ok := object(value)["id"]; ok {
			pk = fmtID(created)
		}
	}
	target := routeURL(spec.Return)
	if strings.Contains(routePaths[spec.Return], "<") {
		target = routeURL(spec.Return, pk)
	}
	if target == "#" {
		target = "/"
	}
	http.Redirect(w, r, target, 303)
}
func fmtID(value any) string {
	switch v := value.(type) {
	case json.Number:
		return string(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatInt(int64(v), 10)
	case string:
		return v
	}
	return ""
}
