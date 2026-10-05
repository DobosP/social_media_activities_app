package accounts

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

// AuthenticateToken preserves the mobile Authorization: Token <40hex> contract.
// Token rows are revocable and checked for age and current active user status.
func (s *Service) AuthenticateToken(r *http.Request) (platform.Actor, error) {
	raw := r.Header.Get("Authorization")
	if len(raw) < len("Token ") || !strings.EqualFold(raw[:len("Token ")], "Token ") {
		return platform.Actor{}, authcore.ErrNotFound
	}
	key := raw[len("Token "):]
	if len(key) != 40 {
		return platform.Actor{}, authcore.ErrNotFound
	}
	if _, err := hex.DecodeString(key); err != nil {
		return platform.Actor{}, authcore.ErrNotFound
	}
	var id int64
	err := s.DB.QueryRow(r.Context(), `SELECT t.user_id FROM authtoken_token t JOIN accounts_user u ON u.id=t.user_id WHERE t.key=$1 AND t.created>$2 AND u.is_active`, key, s.Config.Now().Add(-s.Config.APITokenTTL)).Scan(&id)
	if err != nil {
		return platform.Actor{}, storeError(err)
	}
	return s.actor(r.Context(), s.DB, id)
}

func (s *Service) ObtainToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if platform.Decode(w, r, &body) != nil {
		platform.Fail(w, platform.ErrInvalid)
		return
	}
	// Hash-free rejections come before any failure row exists.
	if len(body.Username) > 150 || len(body.Password) > 1024 {
		platform.Error(w, 400, "Invalid credentials.")
		return
	}
	// The per-prefix cap is the app's api.token budget on this exact route; the
	// failed-login counter is the same username+peer pair as browser/JSON login.
	var user authcore.User
	valid, err := s.LoginFailures(r.Context(), body.Username, r.RemoteAddr, func(ctx context.Context) (bool, error) {
		found, hash, err := s.Store.FindByUsername(ctx, body.Username)
		if err != nil && !errors.Is(err, authcore.ErrNotFound) {
			return false, err
		}
		exists := err == nil
		if !exists {
			hash = "pbkdf2_sha256$1000000$dummy$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
		}
		verified, err := authcore.VerifyPassword(ctx, body.Password, hash)
		if err != nil {
			return false, err
		}
		user = found
		return exists && verified, nil
	})
	if errors.Is(err, ErrLoginFailureLimit) {
		platform.Error(w, 429, "Try again later.")
		return
	}
	if err != nil {
		platform.Fail(w, err)
		return
	}
	if !valid {
		platform.Error(w, 400, "Invalid credentials.")
		return
	}
	var token string
	id, err := strconv.ParseInt(user.ID, 10, 64)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	err = platform.Transaction(r.Context(), s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, id); err != nil {
			return err
		}
		err := tx.QueryRow(r.Context(), `SELECT key FROM authtoken_token WHERE user_id=$1 AND created>$2`, id, s.Config.Now().Add(-s.Config.APITokenTTL)).Scan(&token)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if _, err = tx.Exec(r.Context(), `DELETE FROM authtoken_token WHERE user_id=$1`, id); err != nil {
			return err
		}
		raw := make([]byte, 20)
		if _, err = rand.Read(raw); err != nil {
			return err
		}
		token = hex.EncodeToString(raw)
		_, err = tx.Exec(r.Context(), `INSERT INTO authtoken_token(key,user_id,created) VALUES($1,$2,$3)`, token, id, s.Config.Now())
		return err
	})
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, map[string]string{"token": token})
}
func (s *Service) RevokeToken(w http.ResponseWriter, r *http.Request) {
	a, err := s.AuthenticateToken(r)
	if err != nil {
		platform.Error(w, 401, "Authentication required.")
		return
	}
	if err = s.RevokeAPIAccess(r.Context(), a.ID); err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 204, nil)
}
func (s *Service) RevokeAPIAccess(ctx context.Context, user int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM authtoken_token WHERE user_id=$1`, user)
	return err
}
