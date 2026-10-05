package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrLoginPeerLimit means one trusted network prefix spent its total
// credential-attempt cap. It never depends on how many other peers are active.
var ErrLoginPeerLimit = errors.New("too many login attempts from this network")

// Per-prefix total-attempt scopes (ADR-0039). Successes count too; the
// failed-only username+IP counter is separate and unchanged.
const (
	AuthScopeLogin      = "auth.login"
	AuthScopeRestricted = "auth.restricted"
	AuthScopeSignup     = "auth.signup"
	AuthScopeOAuthStart = "auth.oauth_start"
	authScopeLegacy     = "auth.legacy"
	authScopeOAuthFlow  = "auth.oauth_flow"
)

// Reviewed defaults per IPv4 address or IPv6 /64, overridable only through
// Service.RatePolicies. The owner confirmed them on 2026-10-05 (ADR-0039).
var (
	authLoginPolicy      = budgets.Policy{Limit: 100, Window: 15 * time.Minute}
	authRestrictedPolicy = budgets.Policy{Limit: 30, Window: 15 * time.Minute}
	authSignupPolicy     = budgets.Policy{Limit: 30, Window: time.Hour}
	authOAuthStartPolicy = budgets.Policy{Limit: 30, Window: 15 * time.Minute}
	authLegacyPolicy     = budgets.Policy{Limit: 10, Window: time.Minute}
)

const opportunisticSweepBatch = 256

type peerAdmissionContext struct{}

// peerAdmission lives only in request memory. It carries the normalized peer
// prefix to the pinned library's attempt and OAuth-flow stores, whose own key
// is an unrecoverable digest of the raw host.
type peerAdmission struct {
	peer, scope string
	policy      budgets.Policy
	exempt      bool
	err         error
}

func (s *Service) peerPolicy(scope string) (budgets.Policy, error) {
	var fallback budgets.Policy
	switch scope {
	case AuthScopeLogin:
		fallback = authLoginPolicy
	case AuthScopeRestricted:
		fallback = authRestrictedPolicy
	case AuthScopeSignup:
		fallback = authSignupPolicy
	case AuthScopeOAuthStart:
		fallback = authOAuthStartPolicy
	default:
		return budgets.Policy{}, errors.New("unknown peer admission scope")
	}
	return budgets.Resolve(s.RatePolicies, scope, fallback)
}

// WithPeerAdmission marks a request bound for the pinned authentication
// library with its trusted peer prefix and admission scope. An invalid policy
// override is carried as an error and refuses admission; it never disables it.
func (s *Service) WithPeerAdmission(r *http.Request, scope string) *http.Request {
	policy, err := s.peerPolicy(scope)
	value := &peerAdmission{peer: platform.PeerKey(r.RemoteAddr), scope: scope, policy: policy, err: err}
	return r.WithContext(context.WithValue(r.Context(), peerAdmissionContext{}, value))
}

// WithPeerAdmissionExempt marks a request whose pinned handler must never be
// throttled (API logout). The attempt store then admits it without a write.
func (s *Service) WithPeerAdmissionExempt(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), peerAdmissionContext{}, &peerAdmission{exempt: true}))
}

// AdmitLoginPeer charges one credential attempt to the trusted peer prefix
// before any failure reservation or password work.
func (s *Service) AdmitLoginPeer(ctx context.Context, scope, address string) error {
	_, err := s.admitLoginPeer(ctx, scope, address)
	return err
}

func (s *Service) admitLoginPeer(ctx context.Context, scope, address string) (time.Duration, error) {
	if s.Store == nil || s.Config.Now == nil {
		return 0, platform.ErrInvalid
	}
	policy, err := s.peerPolicy(scope)
	if err != nil {
		return 0, err
	}
	allowed, retry, err := s.Store.admitPeer(ctx, scope, platform.PeerKey(address), policy, s.Config.Now())
	if err != nil {
		return 0, err
	}
	if !allowed {
		return retry, ErrLoginPeerLimit
	}
	return 0, nil
}

// peerKey never stores a raw address: HMAC under the deployment's binding
// secret, or a plain digest for bare fixtures without one.
func (s *Store) peerKey(scope, peer string) string {
	message := []byte("accounts.peer-admission.v1\x00" + scope + "\x00" + peer)
	if len(s.PeerSecret) == 0 {
		digest := sha256.Sum256(message)
		return hex.EncodeToString(digest[:])
	}
	mac := hmac.New(sha256.New, s.PeerSecret)
	_, _ = mac.Write(message)
	return hex.EncodeToString(mac.Sum(nil))
}

// admitPeer is one fixed-window statement per (scope, prefix): no advisory lock,
// no table count and no capacity refusal. An expired key resets inline, so
// correctness never depends on the sweep.
func (s *Store) admitPeer(ctx context.Context, scope, peer string, p budgets.Policy, now time.Time) (bool, time.Duration, error) {
	if err := p.Valid(); err != nil {
		return false, 0, err
	}
	if s == nil || s.DB == nil || scope == "" || peer == "" {
		return false, 0, errors.New("peer admission unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var count int
	var expires time.Time
	err := s.DB.QueryRow(ctx, `INSERT INTO accounts_go_auth_attempt(key_hash,count,expires_at) VALUES($1,1,$2)
 ON CONFLICT(key_hash) DO UPDATE SET
 count=CASE WHEN accounts_go_auth_attempt.expires_at<=$3 THEN 1 ELSE LEAST(accounts_go_auth_attempt.count+1,1000000) END,
 expires_at=CASE WHEN accounts_go_auth_attempt.expires_at<=$3 THEN $2 ELSE accounts_go_auth_attempt.expires_at END
 RETURNING count,expires_at`, s.peerKey(scope, peer), now.Add(p.Window), now).Scan(&count, &expires)
	if err != nil {
		return false, 0, err
	}
	if count <= p.Limit {
		// Only admitted attempts may trigger hygiene, so a throttled flood cannot.
		s.maybeSweep(now)
		return true, 0, nil
	}
	return false, max(time.Second, expires.Sub(now)), nil
}

// maybeSweep runs best-effort storage hygiene after a completed admission or
// reservation, in its own context and outside every admission transaction.
// sweepDue is injectable for tests; nil means a one-in-sixteen chance.
func (s *Store) maybeSweep(now time.Time) {
	if s == nil || s.DB == nil {
		return
	}
	due := s.sweepDue
	if due == nil {
		due = oneInSixteen
	}
	if !due() {
		return
	}
	_ = sweepAuthState(context.Background(), s.DB, now, opportunisticSweepBatch)
}

func oneInSixteen() bool {
	var b [1]byte
	_, _ = rand.Read(b[:])
	return b[0]&15 == 0
}

// SweepAuthState removes expired authentication admission state in bounded,
// separately committed statements that skip rows other transactions hold.
func (s *Service) SweepAuthState(ctx context.Context, batch int) error {
	if s.Config.Now == nil {
		return platform.ErrInvalid
	}
	return sweepAuthState(ctx, s.DB, s.Config.Now(), batch)
}

func sweepAuthState(ctx context.Context, db *pgxpool.Pool, now time.Time, batch int) error {
	if db == nil || batch < 1 || batch > 1000 {
		return platform.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for _, statement := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM accounts_go_login_reservation WHERE token_hash IN(SELECT token_hash FROM accounts_go_login_reservation WHERE expires_at<=$1 ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED)`, []any{now, batch}},
		// A concurrent reservation updates touched_at in its own transaction, so
		// the locked-row recheck excludes a pair that just became active.
		{`DELETE FROM accounts_go_login_failure WHERE key_hash IN(SELECT b.key_hash FROM accounts_go_login_failure b WHERE b.touched_at<=$1 AND (b.failure_until IS NULL OR b.failure_until<=$2) AND NOT EXISTS(SELECT 1 FROM accounts_go_login_reservation r WHERE r.key_hash=b.key_hash) ORDER BY b.touched_at LIMIT $3 FOR UPDATE SKIP LOCKED)`, []any{now.Add(-loginReservationLease), now, batch}},
		{`DELETE FROM accounts_go_auth_attempt WHERE key_hash IN(SELECT key_hash FROM accounts_go_auth_attempt WHERE expires_at<=$1 ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED)`, []any{now, batch}},
		{`DELETE FROM accounts_go_oauth_flow WHERE state_hash IN(SELECT state_hash FROM accounts_go_oauth_flow WHERE expires_at<=$1 ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED)`, []any{now, batch}},
	} {
		if _, err := db.Exec(ctx, statement.sql, statement.args...); err != nil {
			return err
		}
	}
	return nil
}
