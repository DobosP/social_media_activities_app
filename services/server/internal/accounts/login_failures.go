package accounts

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrLoginFailureLimit = errors.New("too many failed login attempts")
	errLoginReservation  = errors.New("login reservation unavailable")
)

const loginReservationLease = time.Minute
const loginFailureMaxPairs = 10000

const LoginFailuresSchema = `
CREATE TABLE IF NOT EXISTS accounts_go_login_failure (
 key_hash char(64) PRIMARY KEY, epoch char(64) NOT NULL, failures integer NOT NULL CHECK(failures BETWEEN 0 AND 1000),
 failure_until timestamptz, touched_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS accounts_go_login_failure_expiry ON accounts_go_login_failure(touched_at);
CREATE TABLE IF NOT EXISTS accounts_go_login_reservation (
 token_hash char(64) PRIMARY KEY, key_hash char(64) NOT NULL REFERENCES accounts_go_login_failure(key_hash) ON DELETE CASCADE,
 epoch char(64) NOT NULL, expires_at timestamptz NOT NULL);
CREATE INDEX IF NOT EXISTS accounts_go_login_reservation_pair ON accounts_go_login_reservation(key_hash);
CREATE INDEX IF NOT EXISTS accounts_go_login_reservation_expiry ON accounts_go_login_reservation(expires_at);`

func (s *Service) MigrateLoginFailures(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, LoginFailuresSchema)
	return err
}

// NormalizeLoginPeer accepts only the server's already trusted peer identity.
// Forwarded headers are interpreted at the app boundary, never here. Source
// ports and equivalent IPv4-mapped addresses cannot mint independent counters.
func NormalizeLoginPeer(address string) string {
	peer, _, err := net.SplitHostPort(address)
	if err != nil {
		peer = address
	}
	peer = strings.TrimSpace(peer)
	if ip, err := netip.ParseAddr(peer); err == nil {
		return ip.Unmap().String()
	}
	return "unknown"
}

func loginCorePeerHash(address string) string {
	peer, _, err := net.SplitHostPort(address)
	if err != nil {
		peer = address
	}
	digest := sha256.Sum256([]byte(peer))
	return hex.EncodeToString(digest[:])
}

// NormalizeLoginUsername preserves credential case while matching the source
// form's whitespace cleaning. Admission and verification must use this value.
func NormalizeLoginUsername(username string) string { return strings.TrimSpace(username) }

func (s *Service) loginFailureKey(username, peer string) (string, error) {
	username = NormalizeLoginUsername(username)
	if len(s.Secret) < 32 || len(username) > 150 || len(peer) > 256 {
		return "", platform.ErrInvalid
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte("accounts.login-failures.v1\x00" + strings.ToLower(username) + "\x00" + NormalizeLoginPeer(peer)))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

type loginReservation struct {
	key, epoch, token string
	db                *pgxpool.Pool
	now               func() time.Time
	corePeer          string
	used              atomic.Bool
	mu                sync.Mutex
	guardErr          error
}

type reservedLoginContext struct{}

func (r *loginReservation) setGuardError(err error) { r.mu.Lock(); r.guardErr = err; r.mu.Unlock() }
func (r *loginReservation) guardError() error       { r.mu.Lock(); defer r.mu.Unlock(); return r.guardErr }

// This private marker exists only inside LoginPOST's pinned Auth.Login call.
// The exported credential callback helper never grants this context capability.
func (s *Store) reservedLoginAllowed(ctx context.Context, key string) (bool, bool, error) {
	r, present := ctx.Value(reservedLoginContext{}).(*loginReservation)
	if !present {
		return false, false, nil
	}
	if r.db != s.DB || key != r.corePeer || !r.used.CompareAndSwap(false, true) {
		r.setGuardError(errLoginReservation)
		return false, true, errLoginReservation
	}
	var allowed bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_go_login_reservation WHERE token_hash=$1 AND key_hash=$2 AND epoch=$3 AND expires_at>$4)`, r.token, r.key, r.epoch, r.now()).Scan(&allowed)
	if err == nil && !allowed {
		err = errLoginReservation
	}
	if err != nil {
		r.setGuardError(err)
	}
	return allowed && err == nil, true, err
}

func (s *Service) reserveLogin(ctx context.Context, username, peer string) (*loginReservation, error) {
	limit, window := s.Config.LoginFailureLimit, s.Config.LoginFailureWindow
	if s.DB == nil || s.Config.Now == nil || limit < 1 || limit > 1000 || window <= 0 || window > 24*time.Hour {
		return nil, platform.ErrInvalid
	}
	key, err := s.loginFailureKey(username, peer)
	if err != nil {
		return nil, err
	}
	now := s.Config.Now()
	result := &loginReservation{key: key, token: platform.Hash([]byte(rand.Text() + rand.Text())), db: s.DB, now: s.Config.Now, corePeer: loginCorePeerHash(peer)}
	err = platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		// Bound total storage and serialize first-use cardinality as well as each
		// pair's reservation. Password work happens outside this short transaction.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951218)`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM accounts_go_login_reservation WHERE token_hash IN(SELECT token_hash FROM accounts_go_login_reservation WHERE expires_at<=$1 ORDER BY expires_at LIMIT 256)`, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM accounts_go_login_reservation WHERE key_hash=$1 AND expires_at<=$2`, key, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM accounts_go_login_failure WHERE key_hash IN(SELECT b.key_hash FROM accounts_go_login_failure b WHERE b.touched_at<=$1 AND (b.failure_until IS NULL OR b.failure_until<=$2) AND NOT EXISTS(SELECT 1 FROM accounts_go_login_reservation r WHERE r.key_hash=b.key_hash) ORDER BY b.touched_at LIMIT 256)`, now.Add(-loginReservationLease), now); err != nil {
			return err
		}
		var failures int
		var expiry *time.Time
		err := tx.QueryRow(ctx, `SELECT epoch,failures,failure_until FROM accounts_go_login_failure WHERE key_hash=$1 FOR UPDATE`, key).Scan(&result.epoch, &failures, &expiry)
		if errors.Is(err, pgx.ErrNoRows) {
			var pairs int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM accounts_go_login_failure`).Scan(&pairs); err != nil {
				return err
			}
			if pairs >= loginFailureMaxPairs {
				return ErrLoginFailureLimit
			}
			result.epoch = platform.Hash([]byte(rand.Text() + rand.Text()))
			if _, err = tx.Exec(ctx, `INSERT INTO accounts_go_login_failure(key_hash,epoch,failures,failure_until,touched_at) VALUES($1,$2,0,NULL,$3)`, key, result.epoch, now); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if expiry != nil && !expiry.After(now) {
			failures = 0
			result.epoch = platform.Hash([]byte(rand.Text() + rand.Text()))
			if _, err := tx.Exec(ctx, `UPDATE accounts_go_login_failure SET epoch=$2,failures=0,failure_until=NULL WHERE key_hash=$1`, key, result.epoch); err != nil {
				return err
			}
		}
		var pending int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM accounts_go_login_reservation WHERE key_hash=$1 AND expires_at>$2`, key, now).Scan(&pending); err != nil {
			return err
		}
		if failures+pending >= limit {
			return ErrLoginFailureLimit
		}
		if _, err := tx.Exec(ctx, `INSERT INTO accounts_go_login_reservation(token_hash,key_hash,epoch,expires_at) VALUES($1,$2,$3,$4)`, result.token, key, result.epoch, now.Add(loginReservationLease)); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE accounts_go_login_failure SET touched_at=$2 WHERE key_hash=$1`, key, now)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) finishLogin(ctx context.Context, r *loginReservation, success, failure bool) error {
	// Client cancellation cannot strand a slot or suppress a known failure.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	now := r.now()
	return platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(683475951218)`); err != nil {
			return err
		}
		var epoch string
		var failures int
		var expiry *time.Time
		if err := tx.QueryRow(ctx, `SELECT epoch,failures,failure_until FROM accounts_go_login_failure WHERE key_hash=$1 FOR UPDATE`, r.key).Scan(&epoch, &failures, &expiry); err != nil {
			return errLoginReservation
		}
		var lease time.Time
		if err := tx.QueryRow(ctx, `DELETE FROM accounts_go_login_reservation WHERE token_hash=$1 AND key_hash=$2 AND epoch=$3 RETURNING expires_at`, r.token, r.key, r.epoch).Scan(&lease); err != nil {
			return errLoginReservation
		}
		if !lease.After(now) {
			return errLoginReservation
		}
		if epoch != r.epoch {
			return nil
		}
		if expiry != nil && !expiry.After(now) {
			// This completion belongs to an expired window, so it cannot initialize
			// or clear the next window's counter. Release only its own live slot.
			_, err := tx.Exec(ctx, `UPDATE accounts_go_login_failure SET epoch=$2,failures=0,failure_until=NULL,touched_at=$3 WHERE key_hash=$1`, r.key, platform.Hash([]byte(rand.Text()+rand.Text())), now)
			return err
		}
		if success && failures > 0 {
			_, err := tx.Exec(ctx, `UPDATE accounts_go_login_failure SET epoch=$2,failures=0,failure_until=NULL,touched_at=$3 WHERE key_hash=$1`, r.key, platform.Hash([]byte(rand.Text()+rand.Text())), now)
			return err
		}
		if failure {
			_, err := tx.Exec(ctx, `UPDATE accounts_go_login_failure SET failures=failures+1,failure_until=COALESCE(failure_until,$2),touched_at=$3 WHERE key_hash=$1`, r.key, now.Add(s.Config.LoginFailureWindow), now)
			return err
		}
		return nil
	})
}

// LoginFailures reserves before expensive credential verification. A valid
// credential proof clears failures, including informational restricted-account
// outcomes. Invalid/unknown credentials count; infrastructure errors do not.
func (s *Service) LoginFailures(ctx context.Context, username, trustedPeer string, attempt func(context.Context) (bool, error)) (bool, error) {
	if attempt == nil {
		return false, platform.ErrInvalid
	}
	r, err := s.reserveLogin(ctx, username, trustedPeer)
	if err != nil {
		return false, err
	}
	return s.runLoginReservation(ctx, r, attempt)
}

func (s *Service) runLoginReservation(ctx context.Context, r *loginReservation, attempt func(context.Context) (bool, error)) (bool, error) {
	attemptCtx, cancel := context.WithTimeout(ctx, loginReservationLease)
	defer cancel()
	completed := false
	defer func() {
		if !completed {
			_ = s.finishLogin(ctx, r, false, false)
		}
	}()
	success, err := attempt(attemptCtx)
	finishErr := s.finishLogin(ctx, r, success && err == nil, !success && err == nil)
	completed = true
	if finishErr != nil {
		return false, finishErr
	}
	return success, err
}
