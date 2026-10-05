package web

import (
	"encoding/json"
	"errors"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/flosch/pongo2/v6"
	"github.com/jackc/pgx/v5"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
)

var accountActionNames = map[string]bool{"avatar_upload": true, "avatar_style": true, "api_token_revoke": true, "guardian_guardrail_set": true, "guardian_revoke": true, "guardian_invite_create": true, "guardian_invite_accept": true, "guardian_invite_decline": true, "ward_topics_set": true, "verify_age": true, "notification_preferences": true, "access_preferences": true, "display_preferences": true, "account_delete": true, "safety_record_appeal": true}

func init() {
	for name := range accountActionNames {
		if _, ok := actions[name]; !ok {
			actions[name] = actionSpec{}
		}
	}
}
func (s *Server) accountRender(w http.ResponseWriter, r *http.Request, a platform.Actor, name, message string, extra pongo2.Context) {
	data, template, _, err := s.AccountView(r, a, name)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	data["messages"] = []string{message}
	for key, value := range extra {
		data[key] = value
	}
	if s.Auth != nil {
		data["csrf"] = s.Auth.EnsureCSRF(w, r)
	}
	if err = s.Renderer.Render(w, r, template, data); err != nil {
		platform.Error(w, 500, "Page unavailable.")
	}
}
func optionalFormInt(raw string) (*int, error) {
	if raw == "" {
		return nil, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil, platform.ErrInvalid
	}
	return &value, nil
}

// AccountAction is called after the common same-origin CSRF admission. Browser
// forms share the native domain transactions and never trust a posted age band.
func (s *Server) AccountAction(w http.ResponseWriter, r *http.Request, a platform.Actor, name string) bool {
	if !accountActionNames[name] {
		return false
	}
	if r.Method != "POST" {
		w.Header().Set("Allow", "POST")
		platform.Error(w, 405, "Submit this form with POST.")
		return true
	}
	if name != "display_preferences" && (a.ID < 1 || !a.IsActive) {
		platform.Fail(w, platform.ErrForbidden)
		return true
	}
	if r.ParseForm() != nil {
		platform.Fail(w, platform.ErrInvalid)
		return true
	}
	target, view := "/profile/", "profile"
	var err error
	ctx := r.Context()
	switch name {
	case "display_preferences":
		for cookie, allowed := range map[string][]string{"display_theme": {"auto", "light", "dark", "contrast"}, "display_text": {"normal", "large", "larger"}, "display_motion": {"auto", "reduce", "full"}} {
			value := r.PostForm.Get(cookie)
			for _, choice := range allowed {
				if value == choice {
					http.SetCookie(w, &http.Cookie{Name: cookie, Value: value, Path: "/", MaxAge: 365 * 24 * 60 * 60, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || strings.HasPrefix(s.Config.PublicURL, "https://")})
					break
				}
			}
		}
		http.Redirect(w, r, "/display/", 302)
		return true
	case "avatar_upload":
		if s.Media == nil {
			err = errors.New("media unavailable")
		} else {
			var path string
			var cleanup func()
			path, _, cleanup, err = s.socialReadUpload(w, r, "image", 5<<20)
			if cleanup != nil {
				defer cleanup()
			}
			if err == nil && path != "" {
				_, err = s.Media.UploadPhoto(ctx, a, "profile", 0, path)
			}
		}
	case "avatar_style":
		generation, parseErr := strconv.Atoi(r.PostForm.Get("generation"))
		if parseErr != nil {
			err = platform.ErrInvalid
		} else {
			_, _, err = s.call(r, "POST", "/api/accounts/me/avatar-style/", map[string]int{"generation": generation})
		}
	case "api_token_revoke":
		target, view = "/settings/", "settings"
		err = s.Accounts.RevokeAPIAccess(ctx, a.ID)
	case "notification_preferences":
		target, view = "/notifications/preferences/", "notification_preferences"
		muted := r.PostForm["muted"]
		if muted == nil {
			muted = []string{}
		}
		_, _, err = s.call(r, "PUT", "/api/accounts/me/settings/", map[string]any{"muted_kinds": muted})
	case "access_preferences":
		target, view = "/access/", "access_preferences"
		access := map[string]bool{}
		for _, field := range []string{"needs_step_free", "needs_accessible_toilet", "needs_hearing_loop", "prefers_quiet"} {
			value := r.PostForm.Get(field)
			access[field] = value == "on" || value == "true" || value == "1"
		}
		_, _, err = s.call(r, "PUT", "/api/accounts/me/settings/", map[string]any{"access": access})
	case "guardian_invite_create":
		target, view = "/wards/", "wards"
		public := "00000000-0000-0000-0000-000000000000"
		if s.Accounts.Config.AllowMinorOnboarding && a.Cohort == "adult" {
			lookupErr := s.DB.QueryRow(ctx, `SELECT public_id::text FROM accounts_user WHERE username=$1`, strings.TrimSpace(r.PostForm.Get("ward_username"))).Scan(&public)
			if lookupErr != nil && !errors.Is(lookupErr, pgx.ErrNoRows) {
				err = lookupErr
			}
		}
		if err == nil {
			_, response, callErr := s.call(r, "POST", "/api/accounts/guardian-links/", map[string]string{"ward": public, "relationship": r.PostForm.Get("relationship")})
			if callErr != nil && response != nil && response.Code >= 500 {
				err = callErr
			}
		} // Every eligibility outcome has the same browser response.
	case "guardian_invite_accept", "guardian_invite_decline":
		target, view = "/", "you"
		verb := "accept"
		if name == "guardian_invite_decline" {
			verb = "decline"
		}
		_, _, err = s.call(r, "POST", "/api/accounts/guardian-links/"+r.PathValue("token")+"/"+verb+"/", nil)
	case "guardian_guardrail_set", "guardian_revoke", "ward_topics_set":
		target, view = "/wards/", "wards"
		ward := id(r, "ward_pk")
		if ward < 1 {
			err = platform.ErrInvalid
			break
		}
		if name == "guardian_revoke" {
			err = s.Accounts.RevokeGuardian(ctx, a, ward, s.Messaging)
			break
		}
		if name == "ward_topics_set" {
			allowed, limitErr := s.Accounts.AllowAction(ctx, a, "guardian_ward_topics", 30, time.Hour)
			if limitErr != nil {
				err = limitErr
			} else if !allowed {
				err = platform.ErrForbidden
			} else if s.Recommendations == nil {
				err = errors.New("recommendations unavailable")
			} else {
				_, err = s.Recommendations.SetWardTopics(ctx, a, ward, r.PostForm["topics"])
			}
			break
		}
		earliest, e1 := optionalFormInt(r.PostForm.Get("earliest_start_hour"))
		latest, e2 := optionalFormInt(r.PostForm.Get("latest_start_hour"))
		cap, e3 := optionalFormInt(r.PostForm.Get("max_open_joins"))
		if e1 != nil || e2 != nil || e3 != nil {
			err = platform.ErrInvalid
		} else {
			err = s.Accounts.SetGuardianGuardrail(ctx, a, ward, accounts.GuardrailInput{SupervisedOnly: r.PostForm.Get("supervised_only") == "on", Earliest: earliest, Latest: latest, MaxOpen: cap, Weekdays: r.PostForm["allowed_weekdays"], Categories: r.PostForm["allowed_categories"]})
		}
	case "verify_age":
		target, view = "/profile/", "verify_age"
		if r.PostForm.Get("action") == "start" {
			challenge, _, startErr := s.call(r, "POST", "/api/accounts/verify-age/start/", nil)
			if startErr != nil {
				s.accountRender(w, r, a, "verify_age", "Age verification is unavailable. No age band was changed.", nil)
			} else {
				s.accountRender(w, r, a, "verify_age", "", pongo2.Context{"native_challenge": object(challenge)})
			}
			return true
		}
		if r.PostForm.Get("action") != "verify" {
			err = platform.ErrInvalid
			break
		}
		_, _, err = s.call(r, "POST", "/api/accounts/verify-age/", map[string]string{"state": r.PostForm.Get("state"), "vp_token": r.PostForm.Get("vp_token"), "holder_binding_proof": r.PostForm.Get("holder_binding_proof")})
	case "safety_record_appeal":
		target, view = "/my-safety-record/", "safety_record"
		actionID, parseErr := strconv.ParseInt(r.PostForm.Get("action_id"), 10, 64)
		if parseErr != nil {
			err = platform.ErrNotFound
		} else {
			_, err = s.Safety.FileAppeal(ctx, a, actionID, r.PostForm.Get("statement"))
		}
	case "account_delete":
		target, view = "/", "account_delete"
		err = s.Accounts.Erase(ctx, a, a)
		if err == nil && s.Auth != nil {
			s.Auth.ClearCookies(w)
		}
	}
	if err != nil {
		// These source controls always return to the guardian's own panel on an
		// eligibility refusal. A missing relationship is not a disclosure surface.
		wardControl := name == "guardian_guardrail_set" || name == "guardian_revoke" || name == "ward_topics_set"
		if wardControl && (errors.Is(err, pgx.ErrNoRows) || errors.Is(err, platform.ErrNotFound) || errors.Is(err, platform.ErrForbidden)) {
			http.Redirect(w, r, "/wards/", http.StatusFound)
			return true
		}
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, platform.ErrNotFound) {
			platform.Fail(w, platform.ErrNotFound)
		} else if errors.Is(err, platform.ErrBusy) {
			platform.Fail(w, err)
		} else {
			s.accountRender(w, r, a, view, socialActionError(err), nil)
		}
		return true
	}
	http.Redirect(w, r, target, 302)
	return true
}
func (s *Server) AccountDownload(w http.ResponseWriter, r *http.Request, a platform.Actor, name string) bool {
	if name != "account_export" {
		return false
	}
	if r.Method != "GET" || a.ID < 1 || !a.IsActive {
		platform.Fail(w, platform.ErrForbidden)
		return true
	}
	payload, err := s.Accounts.Export(r.Context(), a, true)
	if err != nil {
		platform.Fail(w, err)
		return true
	}
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "my-data-" + a.PublicID + ".json"}))
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(payload)
	return true
}
