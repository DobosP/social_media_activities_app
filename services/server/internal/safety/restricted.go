package safety

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"html/template"
	"net/http"
	"time"
)

const restrictedSchema = `CREATE TABLE IF NOT EXISTS safety_go_restrictioncapability(token_hash char(64) PRIMARY KEY,user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,action_id bigint NOT NULL REFERENCES safety_moderationaction(id) ON DELETE CASCADE,expires_at timestamptz NOT NULL);CREATE INDEX IF NOT EXISTS safety_go_restrictionexpiry ON safety_go_restrictioncapability(expires_at)`

func capHash(raw string) string { sum := sha256.Sum256([]byte(raw)); return hex.EncodeToString(sum[:]) }

var restrictedPage = template.Must(template.New("restricted").Parse(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>Account restriction</title><main><h1>Account restriction</h1>{{if .Error}}<p role="alert">{{.Error}}</p>{{end}}{{if .Filed}}<p>Your appeal was received.</p>{{else if .Statement}}<p>{{index .Statement "action_label"}}. Reason: {{index .Statement "reason_label"}}.</p>{{if .Token}}<form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="appeal_token" value="{{.Token}}"><label>Why do you think this decision is wrong?<textarea name="statement" required maxlength="2000"></textarea></label><button>Send appeal</button></form>{{else}}<p>Your contest has been received and its status appears above.</p>{{end}}{{else}}<p>Prove your own account credentials to read its statement of reasons. This grants no participation session.</p><form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><label>Username<input name="username" autocomplete="username" required></label><label>Password<input name="password" type="password" autocomplete="current-password" required maxlength="1024"></label><button>Read my statement</button></form>{{end}}</main></html>`))

type restrictedContext struct {
	CSRF, Token, Error string
	Statement          map[string]any
	Filed              bool
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
			err := s.DB.QueryRow(r.Context(), `DELETE FROM safety_go_restrictioncapability WHERE token_hash=$1 AND expires_at>$2 RETURNING user_id,action_id`, capHash(token), s.Config.Now()).Scan(&user, &action)
			if err != nil {
				view.Error = "Your verification expired. Please verify your credentials again."
			} else {
				_, err = s.FileAppeal(r.Context(), platform.Actor{ID: user}, action, r.PostForm.Get("statement"))
				if err != nil {
					view.Error = "This decision cannot be contested again, or the statement is invalid."
				} else {
					view.Filed = true
				}
			}
		} else {
			statement, token, err := s.RestrictionAccess(r.Context(), r.PostForm.Get("username"), r.PostForm.Get("password"), r.RemoteAddr)
			if err != nil {
				view.Error = "We couldn't verify those details, or no current restriction applies."
			} else {
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
	if len(username) > 150 || len(password) > 1024 {
		return nil, "", platform.ErrInvalid
	}
	if s.Config.Accounts == nil {
		return nil, "", platform.ErrForbidden
	}
	allowed, err := s.Config.Accounts.Store.AllowAuthAttempt(ctx, capHash("restricted:"+address), s.Config.Now())
	if err != nil {
		return nil, "", err
	}
	if !allowed {
		return nil, "", ErrRate
	}
	var id int64
	var active bool
	var hash string
	err = s.DB.QueryRow(ctx, `SELECT id,password,is_active FROM accounts_user WHERE username=$1`, username).Scan(&id, &hash, &active)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, "", err
	}
	if !found {
		hash = "pbkdf2_sha256$1000000$dummy$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	}
	valid, err := authcore.VerifyPassword(ctx, password, hash)
	if err != nil {
		return nil, "", err
	}
	if !found || !valid || active {
		return nil, "", platform.ErrForbidden
	}
	raw, err := s.RestrictionStatement(ctx, id)
	if err != nil {
		return nil, "", err
	}
	var statement map[string]any
	if json.Unmarshal(raw, &statement) != nil {
		return nil, "", platform.ErrInvalid
	}
	token := ""
	if can, ok := statement["can_appeal"].(bool); ok && can {
		action := int64(statement["action_id"].(float64))
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
