package accounts

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	DB *pgxpool.Pool
	// PeerSecret keys per-prefix admission rows (HMAC); every replica and both
	// application stores share the deployment's identity binding secret.
	PeerSecret []byte
	// RequirePeerMarker fails closed on routing drift: the assembled application
	// marks, exempts or reserves every request that reaches the pinned library's
	// attempt and OAuth-flow stores, so an unmarked call is refused (no row)
	// instead of using the legacy per-host key. Bare fixtures leave it false.
	RequirePeerMarker bool
	sweepDue          func() bool
}

func NewStore(db *pgxpool.Pool) *Store { return &Store{DB: db} }

const NativeSchema = `
CREATE TABLE IF NOT EXISTS accounts_go_identity (
 provider text NOT NULL, subject text NOT NULL, user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now(), PRIMARY KEY(provider,subject));
CREATE TABLE IF NOT EXISTS accounts_go_session (
 token_hash text PRIMARY KEY CHECK(length(token_hash)=64), user_id bigint NOT NULL REFERENCES accounts_user(id) ON DELETE CASCADE,
 expires_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS accounts_go_session_expiry ON accounts_go_session(expires_at);
CREATE TABLE IF NOT EXISTS accounts_go_oauth_flow (state_hash text PRIMARY KEY CHECK(length(state_hash)=64), data jsonb NOT NULL, expires_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS accounts_go_oauth_expiry ON accounts_go_oauth_flow(expires_at);
CREATE TABLE IF NOT EXISTS accounts_go_auth_attempt (key_hash text PRIMARY KEY CHECK(length(key_hash)=64), count integer NOT NULL, expires_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS accounts_go_attempt_expiry ON accounts_go_auth_attempt(expires_at);
ALTER TABLE accounts_go_oauth_flow ADD COLUMN IF NOT EXISTS peer_hash char(64);
CREATE INDEX IF NOT EXISTS accounts_go_oauth_peer ON accounts_go_oauth_flow(peer_hash,expires_at);
`

func (s *Store) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, NativeSchema)
	return err
}
func storeError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return authcore.ErrNotFound
	}
	var pgerr *pgconn.PgError
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return authcore.ErrConflict
	}
	return err
}
func uuid() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system randomness unavailable")
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}
func scanUser(row pgx.Row) (authcore.User, string, error) {
	var user authcore.User
	var id int64
	var password string
	err := row.Scan(&id, &user.Username, &user.Name, &password)
	user.ID = strconv.FormatInt(id, 10)
	return user, password, storeError(err)
}
func (s *Store) FindByUsername(ctx context.Context, name string) (authcore.User, string, error) {
	return scanUser(s.DB.QueryRow(ctx, `SELECT id,username,display_name,password FROM accounts_user WHERE username=$1 AND is_active`, name))
}
func insertPending(ctx context.Context, tx pgx.Tx, user authcore.User, password, provider string) (authcore.User, error) {
	if utf8.RuneCountInString(user.Username) > 150 || utf8.RuneCountInString(user.Name) > 120 {
		return authcore.User{}, authcore.ErrConflict
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined)
 VALUES($1,NULL,false,$2,$3,$4,'unknown','unassigned',false,NULL,'user',true,false,now()) RETURNING id`, password, uuid(), user.Username, user.Name).Scan(&id)
	if err != nil {
		return authcore.User{}, storeError(err)
	}
	user.ID = strconv.FormatInt(id, 10)
	user.Email = ""
	user.Avatar = ""
	actor := platform.Actor{ID: id, IsActive: true, Cohort: "unassigned", Role: "user"}
	if err = platform.RecordAudit(ctx, tx, actor, "accounts.registered_pending", "accounts.user:"+user.ID, map[string]any{"provider": provider}); err != nil {
		return authcore.User{}, err
	}
	return user, nil
}
func (s *Store) CreatePasswordUser(ctx context.Context, user authcore.User, password string) (authcore.User, error) {
	var result authcore.User
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var err error
		result, err = insertPending(ctx, tx, user, password, "password")
		return err
	})
	return result, err
}
func (s *Store) FindOrCreateExternal(ctx context.Context, provider, subject string, profile authcore.User) (authcore.User, error) {
	if (provider != "google" && provider != "facebook") || subject == "" || len(subject) > 255 {
		return authcore.User{}, authcore.ErrNotFound
	}
	var result authcore.User
	err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		// Account linking is solely provider+subject, serialized even for first login.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, provider+":"+subject); err != nil {
			return err
		}
		user, _, err := scanUser(tx.QueryRow(ctx, `SELECT u.id,u.username,u.display_name,u.password FROM accounts_go_identity i JOIN accounts_user u ON u.id=i.user_id WHERE i.provider=$1 AND i.subject=$2 AND u.is_active`, provider, subject))
		if err == nil {
			result = user
			return nil
		}
		if !errors.Is(err, authcore.ErrNotFound) {
			return err
		}
		var existing bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_go_identity WHERE provider=$1 AND subject=$2)`, provider, subject).Scan(&existing); err != nil {
			return err
		}
		if existing {
			return authcore.ErrNotFound
		}
		// Provider real names, email and avatars never become a public identity.
		pending := authcore.User{Username: "member_" + rand.Text(), Name: ""}
		result, err = insertPending(ctx, tx, pending, "!", provider)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO accounts_go_identity(provider,subject,user_id) VALUES($1,$2,$3)`, provider, subject, result.ID)
		return err
	})
	return result, err
}
func (s *Store) CreateSession(ctx context.Context, session authcore.Session) error {
	id, err := strconv.ParseInt(session.UserID, 10, 64)
	if err != nil || id <= 0 {
		return authcore.ErrNotFound
	}
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		var active bool
		if err := tx.QueryRow(ctx, `SELECT is_active FROM accounts_user WHERE id=$1 FOR UPDATE`, id).Scan(&active); err != nil {
			return storeError(err)
		}
		if !active {
			return authcore.ErrNotFound
		}
		if _, err := tx.Exec(ctx, `DELETE FROM accounts_go_session WHERE expires_at<=now()`); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `INSERT INTO accounts_go_session(token_hash,user_id,expires_at) VALUES($1,$2,$3)`, session.TokenHash, id, session.ExpiresAt)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return authcore.ErrNotFound
		}
		_, err = tx.Exec(ctx, `DELETE FROM accounts_go_session WHERE user_id=$1 AND token_hash NOT IN(SELECT token_hash FROM accounts_go_session WHERE user_id=$1 ORDER BY expires_at DESC,token_hash DESC LIMIT 10)`, id)
		return err
	})
}
func (s *Store) GetSession(ctx context.Context, hash string, now time.Time) (authcore.User, error) {
	u, _, err := scanUser(s.DB.QueryRow(ctx, `SELECT u.id,u.username,u.display_name,u.password FROM accounts_go_session s JOIN accounts_user u ON u.id=s.user_id WHERE s.token_hash=$1 AND s.expires_at>$2 AND u.is_active`, hash, now))
	return u, err
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM accounts_go_session WHERE token_hash=$1`, hash)
	return err
}
func (s *Store) CreateOAuthFlow(ctx context.Context, hash string, flow authcore.OAuthFlow) error {
	data, err := json.Marshal(flow)
	if err != nil {
		return err
	}
	marker, ok := ctx.Value(peerAdmissionContext{}).(*peerAdmission)
	if !ok || marker.peer == "" {
		if s.RequirePeerMarker {
			return authcore.ErrConflict
		}
		// Direct library callers keep the legacy per-hash attempt cap as bound.
		_, err = s.DB.Exec(ctx, `INSERT INTO accounts_go_oauth_flow(state_hash,data,expires_at) VALUES($1,$2,$3)`, hash, data, flow.ExpiresAt)
		return err
	}
	// Pending flows are capped per network prefix, never globally, at the
	// prefix's resolved OAuth start limit: every live flow needed an admitted start.
	limit := marker.policy.Limit
	if marker.err != nil || limit < 1 {
		return authcore.ErrConflict
	}
	peerHash := s.peerKey(authScopeOAuthFlow, marker.peer)
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,683475951211))`, peerHash); err != nil {
			return err
		}
		var live int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM (SELECT 1 FROM accounts_go_oauth_flow WHERE peer_hash=$1 AND expires_at>now() LIMIT $2) f`, peerHash, limit).Scan(&live); err != nil {
			return err
		}
		if live >= limit {
			return authcore.ErrConflict
		}
		_, err := tx.Exec(ctx, `INSERT INTO accounts_go_oauth_flow(state_hash,data,expires_at,peer_hash) VALUES($1,$2,$3,$4)`, hash, data, flow.ExpiresAt, peerHash)
		return err
	})
}
func (s *Store) ConsumeOAuthFlow(ctx context.Context, hash string, now time.Time) (authcore.OAuthFlow, error) {
	var data []byte
	var result authcore.OAuthFlow
	err := s.DB.QueryRow(ctx, `DELETE FROM accounts_go_oauth_flow WHERE state_hash=$1 AND expires_at>$2 RETURNING data`, hash, now).Scan(&data)
	if err != nil {
		return result, storeError(err)
	}
	err = json.Unmarshal(data, &result)
	return result, err
}
func (s *Store) AllowAuthAttempt(ctx context.Context, key string, now time.Time) (bool, error) {
	if allowed, reserved, err := s.reservedLoginAllowed(ctx, key); reserved {
		return allowed, err
	}
	if marker, ok := ctx.Value(peerAdmissionContext{}).(*peerAdmission); ok {
		if marker.exempt {
			return true, nil
		}
		if marker.err != nil {
			return false, marker.err
		}
		allowed, _, err := s.admitPeer(ctx, marker.scope, marker.peer, marker.policy, now)
		return allowed, err
	}
	if s.RequirePeerMarker {
		// The assembled application never calls here unmarked; refuse drift.
		return false, nil
	}
	// No marker: direct library callers keep ten attempts per minute per the
	// library's own host digest, now without any table-wide refusal.
	allowed, _, err := s.admitPeer(ctx, authScopeLegacy, key, authLegacyPolicy, now)
	return allowed, err
}

func (s *Store) Actor(ctx context.Context, userID string) (platform.Actor, error) {
	var a platform.Actor
	err := s.DB.QueryRow(ctx, `SELECT id,public_id::text,username,display_name,age_band,cohort,role,is_identity_verified,is_active,is_staff,is_superuser FROM accounts_user WHERE id=$1 AND is_active`, userID).Scan(&a.ID, &a.PublicID, &a.Username, &a.DisplayName, &a.AgeBand, &a.Cohort, &a.Role, &a.IdentityVerified, &a.IsActive, &a.IsStaff, &a.IsSuperuser)
	return a, err
}
