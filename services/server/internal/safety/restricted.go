package safety

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const restrictedSchema = `CREATE TABLE IF NOT EXISTS safety_go_restrictioncapability(token_hash char(64) PRIMARY KEY,user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,action_id bigint NOT NULL REFERENCES safety_moderationaction(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL);CREATE INDEX IF NOT EXISTS safety_go_restrictionexpiry ON safety_go_restrictioncapability(expires_at)`

func capHash(raw string) string { sum := sha256.Sum256([]byte(raw)); return hex.EncodeToString(sum[:]) }

var (
	errRestrictionAccountActive = errors.New("verified account is active")
	errRestrictionNoDecision    = errors.New("verified account has no current moderation restriction")
)

func restrictedDate(value any) string {
	text, _ := value.(string)
	date, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return ""
	}
	if location, err := time.LoadLocation("Europe/Bucharest"); err == nil {
		date = date.In(location)
	}
	return date.Format("2 Jan 2006, 15:04")
}

var restrictedPage = template.Must(template.New("restricted").Funcs(template.FuncMap{"date": restrictedDate}).Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Account restriction</title><main><h1>Account restriction</h1>{{if .Error}}<p role="alert">{{.Error}}</p>{{end}}{{if .Filed}}<p>Your appeal was received.</p>{{else if .Active}}<p>Your account is active. You can log in normally.</p>{{else if .InactiveNoModeration}}<p>Your account is not currently active, and there is no moderation decision to show.</p>{{else if .Statement}}<p>{{index .Statement "action_label"}}. Reason: {{index .Statement "reason_label"}}.</p><p>{{date (index .Statement "created_at")}}</p>{{if index .Statement "is_lifetime"}}<p>This is a permanent restriction.</p>{{else if index .Statement "lifts_at"}}<p>This restriction is due to lift on {{date (index .Statement "lifts_at")}}.</p>{{else}}<p>This restriction does not have an automatic end date.</p>{{end}}{{if .Token}}<form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="appeal_token" value="{{.Token}}"><label>Why do you think this decision is wrong?<textarea name="statement" required maxlength="2000"></textarea></label><button>Send appeal</button></form>{{else if index .Statement "appeal_status_label"}}<p>You have already contested this decision. Status: {{index .Statement "appeal_status_label"}}.</p>{{end}}{{else}}<p>Prove your own account credentials to read its statement of reasons. This grants no participation session.</p><form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><label>Username<input name="username" autocomplete="username" required></label><label>Password<input name="password" type="password" autocomplete="current-password" required maxlength="1024"></label><button>Read my statement</button></form>{{end}}</main></html>`))

type restrictedContext struct {
	CSRF, Token, Error           string
	Statement                    map[string]any
	Filed                        bool
	Active, InactiveNoModeration bool
}

func (s *Service) Restricted(w http.ResponseWriter, r *http.Request) {
	if s.Config.Accounts == nil || s.Config.Accounts.Auth == nil {
		platform.Error(w, 503, "Account recovery unavailable.")
		return
	}
	view := restrictedContext{CSRF: s.Config.Accounts.Auth.EnsureCSRF(w, r)}
	if r.Method == http.MethodPost {
		r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
		if r.ParseForm() != nil {
			platform.Error(w, 400, "Invalid request.")
			return
		}
		r.Header.Set("X-CSRFToken", r.PostForm.Get("csrf_token"))
		if s.Config.Accounts.Auth.CheckCSRF(r) != nil {
			platform.Error(w, 403, "CSRF verification failed.")
			return
		}
		if token := r.PostForm.Get("appeal_token"); token != "" {
			var user, action int64
			err := s.DB.QueryRow(r.Context(), `SELECT user_id,action_id FROM safety_go_restrictioncapability WHERE token_hash=$1 AND expires_at>$2`, capHash(token), s.Config.Now()).Scan(&user, &action)
			if err != nil {
				view.Error = "Your verification expired. Please verify your credentials again."
			} else {
				statement, current, scopeErr := s.restrictedStatement(r.Context(), user)
				text := strings.TrimSpace(r.PostForm.Get("statement"))
				switch {
				case scopeErr != nil || current != action:
					view.Error = "Your verification expired. Please verify your credentials again."
				case text == "" || utf8.RuneCountInString(text) > 2000:
					view.Statement = statement
					if can, _ := statement["can_appeal"].(bool); can {
						view.Token = token
					}
					view.Error = "Please tell us why you think this decision is wrong, in at most 2000 characters."
				default:
					err = s.DB.QueryRow(r.Context(), `DELETE FROM safety_go_restrictioncapability WHERE token_hash=$1 AND user_id=$2 AND action_id=$3 AND expires_at>$4 RETURNING user_id,action_id`, capHash(token), user, action, s.Config.Now()).Scan(&user, &action)
					if err == nil {
						_, err = s.FileAppeal(r.Context(), platform.Actor{ID: user}, action, text)
					}
					if err != nil {
						view.Error = "This decision cannot be contested again, or your verification expired."
					} else {
						view.Filed = true
					}
				}
			}
		} else {
			statement, token, err := s.RestrictionAccess(r.Context(), r.PostForm.Get("username"), r.PostForm.Get("password"), r.RemoteAddr)
			switch {
			case errors.Is(err, ErrRate):
				view.Error = "Too many attempts. Please wait a few minutes and try again."
			case errors.Is(err, errRestrictionAccountActive):
				view.Active = true
			case errors.Is(err, errRestrictionNoDecision):
				view.InactiveNoModeration = true
			case err != nil:
				view.Error = "We couldn't verify those details."
			default:
				view.Token = token
				view.Statement = statement
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = restrictedPage.Execute(w, view)
}

// RestrictionAccess validates credentials even for a disabled account, then
// grants only a short-lived, one-decision appeal capability, never a session.
func (s *Service) RestrictionAccess(ctx context.Context, username, password, address string) (map[string]any, string, error) {
	username = accounts.NormalizeLoginUsername(username)
	if len(username) > 150 || len(password) > 1024 {
		return nil, "", platform.ErrInvalid
	}
	if s.Config.Accounts == nil {
		return nil, "", platform.ErrForbidden
	}
	// Its own per-prefix scope: a login spray cannot block this DSA remedy, and
	// it still caps total proofs per network before any password work.
	if err := s.Config.Accounts.AdmitLoginPeer(ctx, accounts.AuthScopeRestricted, address); err != nil {
		if errors.Is(err, accounts.ErrLoginPeerLimit) {
			return nil, "", ErrRate
		}
		return nil, "", err
	}
	var id int64
	var active bool
	valid, err := s.Config.Accounts.LoginFailures(ctx, username, address, func(attempt context.Context) (bool, error) {
		var hash string
		err := s.DB.QueryRow(attempt, `SELECT id,password,is_active FROM accounts_user WHERE username=$1`, username).Scan(&id, &hash, &active)
		found := err == nil
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return false, err
		}
		if !found {
			hash = "pbkdf2_sha256$1000000$dummy$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		}
		valid, err := authcore.VerifyPassword(attempt, password, hash)
		return found && valid, err
	})
	if errors.Is(err, accounts.ErrLoginFailureLimit) {
		return nil, "", ErrRate
	}
	if err != nil {
		return nil, "", err
	}
	if !valid {
		return nil, "", platform.ErrForbidden
	}
	if active {
		return nil, "", errRestrictionAccountActive
	}
	statement, action, err := s.restrictedStatement(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", errRestrictionNoDecision
	}
	if err != nil {
		return nil, "", err
	}
	token := ""
	if can, ok := statement["can_appeal"].(bool); ok && can {
		token = rand.Text() + rand.Text()
		if _, err = s.DB.Exec(ctx, `DELETE FROM safety_go_restrictioncapability WHERE expires_at<=$1`, s.Config.Now()); err != nil {
			return nil, "", err
		}
		if _, err = s.DB.Exec(ctx, `INSERT INTO safety_go_restrictioncapability(token_hash,user_id,action_id,expires_at) VALUES($1,$2,$3,$4)`, capHash(token), id, action, s.Config.Now().Add(30*time.Minute)); err != nil {
			return nil, "", err
		}
	}
	return statement, token, nil
}

func (s *Service) restrictedStatement(ctx context.Context, user int64) (map[string]any, int64, error) {
	raw, err := s.RestrictionStatement(ctx, user)
	if err != nil {
		return nil, 0, err
	}
	var statement map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&statement); err != nil {
		return nil, 0, err
	}
	number, ok := statement["action_id"].(json.Number)
	if !ok {
		return nil, 0, platform.ErrInvalid
	}
	action, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil || action < 1 {
		return nil, 0, platform.ErrInvalid
	}
	return statement, action, nil
}
