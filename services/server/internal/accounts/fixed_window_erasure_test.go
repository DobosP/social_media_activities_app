package accounts

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type expiryTraceKey struct{}
type expiryQueryKey struct{}
type expiryErasureTrace struct {
	pruned, userLocked, resume chan struct{}
	pruneOnce, userOnce        sync.Once
}

func TestAccountFixedWindowUnavailableActorFailsClosedBeforeDatabase(t *testing.T) {
	s := New(nil, nil, "", Config{})
	for _, id := range []int64{0, 1} {
		if allowed, err := s.allowAction(context.Background(), id, "guardian_invite", 20, time.Hour); allowed || err == nil {
			t.Fatal("missing database/actor was admitted")
		}
	}
}

func (t *expiryErasureTrace) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, expiryQueryKey{}, data.SQL)
}
func (t *expiryErasureTrace) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if data.Err != nil {
		return
	}
	query, _ := ctx.Value(expiryQueryKey{}).(string)
	if ctx.Value(expiryTraceKey{}) == "admission" && strings.Contains(query, "DELETE FROM accounts_go_action_budget") {
		t.pruneOnce.Do(func() {
			close(t.pruned)
			select {
			case <-t.resume:
			case <-ctx.Done():
			}
		})
	}
	if ctx.Value(expiryTraceKey{}) == "erasure" && strings.Contains(query, "FROM accounts_user") && strings.Contains(query, "FOR UPDATE") {
		t.userOnce.Do(func() { close(t.userLocked) })
	}
}
func awaitExpirySignal(t *testing.T, ctx context.Context, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("synthetic lock-order barrier timed out")
	}
}

// The tracer pauses the real expiry DELETE while the actual Erase service locks
// the account. Before the fix this forced budget->account versus account->budget
// and PostgreSQL aborted one transaction. The separate sweep releases the budget
// locks before admission requests its FK lock, even with only two connections.
func TestPostgresFixedWindowExpiryAndActualErasureNoDeadlock(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "synthetic-expiry-erasure", "adult", "adult")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.Config.Now = func() time.Time { return now }
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_action_budget(user_id,action,count,until) VALUES($1,'guardian_invite',1,$2)`, a.ID, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	trace := &expiryErasureTrace{pruned: make(chan struct{}), userLocked: make(chan struct{}), resume: make(chan struct{})}
	cfg := s.DB.Config()
	cfg.MaxConns = 2
	cfg.MinConns = 0
	cfg.ConnConfig.Tracer = trace
	small, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	s.DB = small
	type result struct {
		allowed bool
		err     error
	}
	admitted := make(chan result, 1)
	erased := make(chan error, 1)
	admissionCtx := context.WithValue(ctx, expiryTraceKey{}, "admission")
	erasureCtx := context.WithValue(ctx, expiryTraceKey{}, "erasure")
	go func() {
		yes, err := s.allowAction(admissionCtx, a.ID, "guardian_invite", 20, time.Hour)
		admitted <- result{yes, err}
	}()
	awaitExpirySignal(t, ctx, trace.pruned)
	go func() { erased <- s.Erase(erasureCtx, a, a) }()
	awaitExpirySignal(t, ctx, trace.userLocked)
	close(trace.resume)
	select {
	case err := <-erased:
		if err != nil {
			t.Fatal("expiry admission aborted actual erasure", err)
		}
	case <-ctx.Done():
		t.Fatal("erasure blocked behind admission")
	}
	select {
	case result := <-admitted:
		if result.allowed || !errors.Is(result.err, pgx.ErrNoRows) {
			var failure *pgconn.PgError
			state := "not_pg"
			if errors.As(result.err, &failure) {
				state = failure.Code
			}
			t.Fatalf("admission did not fail only for the erased actor: allowed=%t sqlstate=%s", result.allowed, state)
		}
	case <-ctx.Done():
		t.Fatal("admission retained pool/lock after erasure")
	}
	var remaining int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_action_budget WHERE user_id=$1`, a.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatal("erasure retained fixed-window budget", remaining, err)
	}
}

func TestPostgresFixedWindowExpirySweepBoundedSkipLockedAndOwnReset(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "synthetic-expiry-owner", "adult", "adult")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.Config.Now = func() time.Time { return now }
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_action_budget(user_id,action,count,until) SELECT $1,'fixture_'||i::text,2147483647,$2 FROM generate_series(1,300) i;`, a.ID, now.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_action_budget(user_id,action,count,until) VALUES($1,'guardian_invite',2147483647,$2),($1,'live_fixture',1,$3)`, a.ID, now.Add(-time.Second), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	locked, err := s.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = locked.Exec(ctx, `SELECT user_id FROM accounts_go_action_budget WHERE user_id=$1 AND action='fixture_1' FOR UPDATE`, a.ID); err != nil {
		locked.Rollback(ctx)
		t.Fatal(err)
	}
	if err = s.pruneExpiredActionBudgets(ctx, now); err != nil {
		locked.Rollback(ctx)
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_action_budget`).Scan(&count); err != nil || count != 46 {
		locked.Rollback(ctx)
		t.Fatal("expiry sweep unbounded or blocked", count, err)
	}
	locked.Rollback(ctx)
	// Repopulate earlier victims so the admission's next bounded sweep cannot
	// reach the target's later expiry. Its own row must reset without overflow.
	if _, err = s.DB.Exec(ctx, `INSERT INTO accounts_go_action_budget(user_id,action,count,until) SELECT $1,'refill_'||i::text,1,$2 FROM generate_series(1,300) i`, a.ID, now.Add(-2*time.Second)); err != nil {
		t.Fatal(err)
	}
	s.RatePolicies = map[string]budgets.Policy{"guardian_invite": {Limit: 2, Window: 90 * time.Second}}
	for i := 0; i < 3; i++ {
		yes, err := s.allowAction(ctx, a.ID, "guardian_invite", 20, time.Hour)
		if err != nil || yes != (i < 2) {
			t.Fatal("expired own bucket reset/live cap changed", i, yes, err)
		}
	}
	var until time.Time
	if err = s.DB.QueryRow(ctx, `SELECT count,until FROM accounts_go_action_budget WHERE user_id=$1 AND action='guardian_invite'`, a.ID).Scan(&count, &until); err != nil || count != 2 || !until.Equal(now.Add(90*time.Second)) {
		t.Fatal("fixed expiry/debit changed", count, until, err)
	}
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_action_budget WHERE action='live_fixture'`).Scan(&count); err != nil || count != 1 {
		t.Fatal("live budget removed", count, err)
	}
}

func TestPostgresFixedWindowConcurrentExpiryWithTwoConnections(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "synthetic-two-connection-expiry", "adult", "adult")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.Config.Now = func() time.Time { return now }
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_action_budget(user_id,action,count,until) VALUES($1,'guardian_invite',2147483647,$2)`, a.ID, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	cfg := s.DB.Config()
	cfg.MaxConns = 2
	cfg.MinConns = 0
	small, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	s.DB = small
	s.RatePolicies = map[string]budgets.Policy{"guardian_invite": {Limit: 3, Window: 2 * time.Minute}}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			yes, err := s.allowAction(ctx, a.ID, "guardian_invite", 20, time.Hour)
			if err != nil {
				t.Error(err)
			}
			if yes {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()
	if admitted.Load() != 3 {
		t.Fatal("concurrent expiry multiplied cap", admitted.Load())
	}
	var count int
	var until time.Time
	if err = s.DB.QueryRow(ctx, `SELECT count,until FROM accounts_go_action_budget WHERE user_id=$1 AND action='guardian_invite'`, a.ID).Scan(&count, &until); err != nil || count != 3 || !until.Equal(now.Add(2*time.Minute)) {
		t.Fatal("bounded denial/fixed expiry changed", count, until, err)
	}
}
