// Package platform owns the common HTTP, authorization and transaction seams.
// Product code never grants a cohort or participation through authentication.
package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Actor struct {
	ID                                                     int64
	PublicID, Username, DisplayName, AgeBand, Cohort, Role string
	IdentityVerified, IsActive, IsStaff, IsSuperuser       bool
}

func (a Actor) Moderator() bool {
	return a.IsActive && (a.IsStaff || a.IsSuperuser || a.Role == "moderator" || a.Role == "admin")
}
func (a Actor) Admin() bool { return a.IsActive && (a.IsSuperuser || a.Role == "admin") }

type actorKey struct{}

func WithActor(r *http.Request, actor Actor) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), actorKey{}, actor))
}
func ActorFrom(r *http.Request) (Actor, bool) {
	a, ok := r.Context().Value(actorKey{}).(Actor)
	return a, ok && a.ID > 0 && a.IsActive
}

type Querier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

var ErrForbidden = errors.New("permission denied")
var ErrNotFound = errors.New("not found")
var ErrInvalid = errors.New("invalid request")

func JSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if status != http.StatusNoContent {
		_ = json.NewEncoder(w).Encode(body)
	}
}
func Error(w http.ResponseWriter, status int, detail string) {
	JSON(w, status, map[string]string{"detail": detail})
}
func Fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		Error(w, 403, "Permission denied.")
	case errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows):
		Error(w, 404, "Not found.")
	case errors.Is(err, ErrInvalid):
		Error(w, 400, "Invalid request.")
	default:
		Error(w, 503, "Service unavailable.")
	}
}
func Decode(w http.ResponseWriter, r *http.Request, body any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(body) != nil || d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}
func RequireActor(w http.ResponseWriter, r *http.Request) (Actor, bool) {
	a, ok := ActorFrom(r)
	if !ok {
		Error(w, 401, "Authentication required.")
	}
	return a, ok
}
func Blocked(ctx context.Context, q Querier, a, b int64) (bool, error) {
	var blocked bool
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_block WHERE (blocker_id=$1 AND blocked_id=$2) OR (blocker_id=$2 AND blocked_id=$1))`, a, b).Scan(&blocked)
	return blocked, err
}
func Participate(ctx context.Context, q Querier, a Actor) error {
	if !a.IsActive || !a.IdentityVerified || a.Cohort == "unassigned" || a.Cohort == "" {
		return ErrForbidden
	}
	var current bool
	// A request actor is a snapshot, including across a separately committed
	// rate reservation. Refuse stale authority instead of moving the operation
	// into a different cohort or retaining privileges withdrawn meanwhile.
	err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_user u
		WHERE u.id=$1 AND u.is_active AND u.is_identity_verified
		AND u.age_band=$2 AND u.cohort=$3 AND u.role=$4
		AND u.is_staff=$5 AND u.is_superuser=$6
		AND COALESCE((SELECT expires_at IS NULL OR expires_at>now()
			FROM accounts_ageassurance WHERE user_id=u.id
			ORDER BY verified_at DESC,id DESC LIMIT 1),true))`,
		a.ID, a.AgeBand, a.Cohort, a.Role, a.IsStaff, a.IsSuperuser).Scan(&current)
	if err != nil {
		return err
	}
	if !current {
		return ErrForbidden
	}
	if a.AgeBand == "under_16" {
		err = q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM accounts_parentalconsent WHERE minor_id=$1 AND status='active' AND (expires_at IS NULL OR expires_at>now()))`, a.ID).Scan(&current)
		if err != nil {
			return err
		}
		if !current {
			return ErrForbidden
		}
	}
	return nil
}
func Transaction(ctx context.Context, db *pgxpool.Pool, f func(pgx.Tx) error) error {
	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// The pool's configured statement_timeout (DB_STATEMENT_TIMEOUT_MS) governs the
	// transaction. Only a session without one receives this bounded default.
	if _, err = tx.Exec(ctx, `SELECT set_config('statement_timeout','5s',true) WHERE current_setting('statement_timeout')='0'`); err != nil {
		return err
	}
	if err = f(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func Timeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 10*time.Second)
}
