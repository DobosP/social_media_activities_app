// Package budgets provides bounded, PostgreSQL-backed sliding admission. It has
// no local history or outage fallback; every replica uses the database clock.
package budgets

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Policy struct {
	Limit  int
	Window time.Duration
}

func (p Policy) Valid() error {
	if p.Limit < 1 || p.Limit > 10000 || p.Window < time.Second || p.Window > 24*time.Hour || p.Window%time.Microsecond != 0 {
		return errors.New("invalid rate policy")
	}
	return nil
}

// Resolve keeps reviewed defaults for omitted actions. An explicit invalid
// override is an error, never an instruction to disable admission.
func Resolve(policies map[string]Policy, action string, fallback Policy) (Policy, error) {
	if p, ok := policies[action]; ok {
		return p, p.Valid()
	}
	return fallback, fallback.Valid()
}

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type Store struct{ DB *pgxpool.Pool }

func New(db *pgxpool.Pool) *Store { return &Store{DB: db} }

// Check rejects an unmigrated shared-admission database before serving starts.
func (s *Store) Check(ctx context.Context) error {
	if s == nil || s.DB == nil {
		return errors.New("shared budget unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var ready bool
	err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM go_rate_budget_capacity WHERE singleton) AND to_regprocedure('go_admit_rate_budget(text,bytea,bigint,integer,bigint)') IS NOT NULL AND to_regprocedure('go_prune_rate_budgets(integer)') IS NOT NULL`).Scan(&ready)
	if err != nil || !ready {
		return errors.New("shared budget schema unavailable")
	}
	return nil
}

// Prune supports explicit, off-request privacy maintenance without starting a
// scheduler. It also runs in a separate transaction before each admission.
func (s *Store) Prune(ctx context.Context, batch int) (int64, error) {
	if s == nil || s.DB == nil || batch < 1 || batch > 1000 {
		return 0, errors.New("invalid shared budget maintenance")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var removed int64
	if err := s.DB.QueryRow(ctx, `SELECT go_prune_rate_budgets($1)`, batch).Scan(&removed); err != nil {
		return 0, errors.New("shared budget maintenance unavailable")
	}
	return removed, nil
}

func (s *Store) Actor(ctx context.Context, actor int64, scope string, policy Policy) (Decision, error) {
	if actor < 1 {
		return Decision{}, errors.New("invalid budget actor")
	}
	key := sha256.Sum256([]byte("actor:" + scope + ":" + strconv.FormatInt(actor, 10)))
	return s.admit(ctx, actor, scope, key[:], policy)
}

// Peer never sends a raw IP/peer to PostgreSQL. The deployment's existing shared
// secret must match across replicas; scopes have distinct HMAC namespaces.
func (s *Store) Peer(ctx context.Context, secret []byte, peer, scope string, policy Policy) (Decision, error) {
	if len(secret) < 32 || len(peer) == 0 || len(peer) > 512 {
		return Decision{}, errors.New("invalid anonymous budget identity")
	}
	h := hmac.New(sha256.New, secret)
	h.Write([]byte("peer:" + scope + ":" + peer))
	return s.admit(ctx, nil, scope, h.Sum(nil), policy)
}

// Global is for a shared ingress ceiling with no personal identity (CSP reports).
func (s *Store) Global(ctx context.Context, scope string, policy Policy) (Decision, error) {
	key := sha256.Sum256([]byte("global:" + scope))
	return s.admit(ctx, nil, scope, key[:], policy)
}

func (s *Store) admit(ctx context.Context, actor any, scope string, key []byte, policy Policy) (Decision, error) {
	if err := policy.Valid(); err != nil {
		return Decision{}, err
	}
	if s == nil || s.DB == nil || len(scope) == 0 || len(scope) > 64 {
		return Decision{}, errors.New("shared budget unavailable")
	}
	for _, c := range scope {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '.') {
			return Decision{}, errors.New("invalid budget scope")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Independent bounded expiry sweep: no request history grows in Go memory.
	if _, err := s.Prune(ctx, 256); err != nil {
		return Decision{}, errors.New("shared budget unavailable")
	}
	var result Decision
	var retryUS int64
	err := s.DB.QueryRow(ctx, `SELECT allowed,retry_us FROM go_admit_rate_budget($1,$2,$3,$4,$5)`, scope, key, actor, policy.Limit, policy.Window.Microseconds()).Scan(&result.Allowed, &retryUS)
	if err != nil {
		return Decision{}, errors.New("shared budget unavailable")
	}
	result.RetryAfter = time.Duration(retryUS) * time.Microsecond
	return result, nil
}

var ErrDenied = errors.New("shared rate budget exhausted")
var needAdmission = errors.New("shared rate admission required")

// Reserve runs domain preflight to its admission point, releases its transaction,
// commits admission independently, then reruns all current domain gates. This
// avoids nested pool acquisition and preserves debits on later domain rollback.
// run must propagate the reservation error unchanged and keep effects inside
// its transaction. Early idempotent returns consume no budget, as before.
func Reserve(run func(reserve func() error) error, admit func() (Decision, error)) error {
	err := run(func() error { return needAdmission })
	if !errors.Is(err, needAdmission) {
		return err
	}
	decision, err := admit()
	if err != nil {
		return err
	}
	if !decision.Allowed {
		return ErrDenied
	}
	return run(func() error { return nil })
}
