package safety

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSafetyFixedWindowUnavailableActorFailsClosedBeforeDatabase(t *testing.T) {
	for _, tc := range []struct {
		db    *pgxpool.Pool
		actor int64
	}{{nil, 1}, {&pgxpool.Pool{}, 0}, {&pgxpool.Pool{}, -1}} {
		s := New(tc.db, Config{})
		if allowed, err := s.allow(context.Background(), platform.Actor{ID: tc.actor}, "report", 20, time.Hour); allowed || err == nil {
			t.Fatal("unavailable budget actor admitted", allowed, err)
		}
		if _, err := s.UnsafeReport(context.Background(), platform.Actor{ID: tc.actor}, 1); err == nil {
			t.Fatal("unavailable unsafe-report actor admitted")
		}
	}
}

func fixedWindowErasureFixture(t *testing.T) (*Service, *accounts.Service, platform.Actor) {
	t.Helper()
	db := testdb.New(t, *safetyTestDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	a := testdb.Actor(t, db, "fixed-window-erasure-owner", "adult")
	cfg := db.Config()
	cfg.MaxConns = 2
	cfg.ConnConfig.RuntimeParams["application_name"] = fmt.Sprintf("safety-fixed-erasure-%d", time.Now().UnixNano())
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	acc := accounts.New(pool, nil, "synthetic-fixed-window-binding-32", accounts.Config{})
	if err = acc.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := New(pool, Config{Accounts: acc})
	if err = s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	var cascade bool
	if err = pool.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid=to_regclass('safety_go_actionbudget') AND confrelid=to_regclass('accounts_user') AND confdeltype='c')`).Scan(&cascade); err != nil || !cascade {
		t.Fatal("real budget erasure FK absent", err)
	}
	return s, acc, a
}

func TestPostgresSafetyFixedWindowSweepBoundSkipsLockedRows(t *testing.T) {
	s, _, a := fixedWindowErasureFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) SELECT $1,'expired'||lpad(i::text,3,'0'),1,$2::timestamptz-interval '1 hour' FROM generate_series(0,259) i`, a.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) VALUES($1,'live',1,$2)`, a.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	controller, err := pgx.ConnectConfig(ctx, s.DB.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close(ctx)
	tx, err := controller.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT user_id FROM safety_go_actionbudget WHERE user_id=$1 AND action='expired000' FOR UPDATE`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.pruneExpiredActionBudgets(ctx, now); err != nil {
		t.Fatal(err)
	}
	var expired, live, locked int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE until<=$1),count(*) FILTER(WHERE action='live'),count(*) FILTER(WHERE action='expired000') FROM safety_go_actionbudget`, now).Scan(&expired, &live, &locked); err != nil || expired != 4 || live != 1 || locked != 1 {
		t.Fatal("sweep exceeded bound or waited for locked victim", expired, live, locked, err)
	}
}

func TestPostgresSafetyFixedWindowLeftoverExpiryResetAndConcurrentCap(t *testing.T) {
	s, _, a := fixedWindowErasureFixture(t)
	ctx := context.Background()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Config.Now = func() time.Time { return now }
	s.RatePolicies = map[string]budgets.Policy{"report": {Limit: 3, Window: 90 * time.Second}}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) SELECT $1,'earlier'||i::text,1,$2::timestamptz-interval '2 hours' FROM generate_series(1,256) i`, a.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) VALUES($1,'report',2147483647,$2::timestamptz-interval '1 hour')`, a.ID, now); err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.allow(ctx, a, "report", 20, time.Hour); err != nil || !allowed {
		t.Fatal("expired target not reset after bounded sweep", allowed, err)
	}
	replica := New(s.DB, s.Config)
	replica.RatePolicies = s.RatePolicies
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := s
			if i%2 != 0 {
				svc = replica
			}
			ok, err := svc.allow(ctx, a, "report", 20, time.Hour)
			if err != nil {
				t.Error(err)
			}
			if ok {
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	var count int
	var until time.Time
	if err := s.DB.QueryRow(ctx, `SELECT count,until FROM safety_go_actionbudget WHERE user_id=$1 AND action='report'`, a.ID).Scan(&count, &until); err != nil || count != 3 || admitted.Load() != 2 || !until.Equal(now.Add(90*time.Second)) {
		t.Fatal("fixed-window cap/expiry changed", count, admitted.Load(), until, err)
	}
	now = now.Add(90 * time.Second)
	if allowed, err := replica.allow(ctx, a, "report", 20, time.Hour); err != nil || !allowed {
		t.Fatal("fixed window did not reopen", allowed, err)
	}
}

func TestPostgresSafetyFixedWindowAdmissionDoesNotDeadlockActualErasure(t *testing.T) {
	s, acc, a := fixedWindowErasureFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.Config.Now = func() time.Time { return now }
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) VALUES($1,'report',1,$2::timestamptz-interval '1 hour')`, a.ID, now); err != nil {
		t.Fatal(err)
	}
	controller, err := pgx.ConnectConfig(ctx, s.DB.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close(context.Background())
	hold, err := controller.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback(context.Background())
	if _, err = hold.Exec(ctx, `SELECT user_id FROM safety_go_actionbudget WHERE user_id=$1 AND action='report' FOR UPDATE`, a.ID); err != nil {
		t.Fatal(err)
	}
	erased := make(chan error, 1)
	go func() { erased <- acc.Erase(ctx, a, a) }()
	// The real eraser now owns accounts_user while the controller pins its child.
	deadline := time.Now().Add(3 * time.Second)
	owned := false
	for time.Now().Before(deadline) {
		if _, err = hold.Exec(ctx, `SAVEPOINT probe_account_lock`); err != nil {
			t.Fatal(err)
		}
		_, probeErr := hold.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR KEY SHARE NOWAIT`, a.ID)
		if _, err = hold.Exec(ctx, `ROLLBACK TO SAVEPOINT probe_account_lock`); err != nil {
			t.Fatal(err)
		}
		var pgerr *pgconn.PgError
		if errors.As(probeErr, &pgerr) && pgerr.Code == "55P03" {
			owned = true
			break
		}
		if probeErr != nil {
			t.Fatal(probeErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !owned {
		t.Fatal("real eraser never obtained parent row lock")
	}
	type admission struct {
		allowed bool
		err     error
	}
	admitted := make(chan admission, 1)
	go func() { ok, err := s.allow(ctx, a, "report", 20, time.Hour); admitted <- admission{ok, err} }()
	// A new limiter skips the pinned expired row, commits its sweep, then waits
	// on the account. The inherited limiter would instead block on DELETE here.
	appName := s.DB.Config().ConnConfig.RuntimeParams["application_name"]
	deadline = time.Now().Add(time.Second)
	parentWait := false
	for time.Now().Before(deadline) {
		if err = hold.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%FOR KEY SHARE%')`, appName).Scan(&parentWait); err != nil {
			t.Fatal(err)
		}
		if parentWait {
			break
		}
		if _, err = hold.Exec(ctx, `SELECT pg_stat_clear_snapshot()`); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !parentWait {
		t.Fatal("admission retained expired child lock instead of waiting on account")
	}
	if err = hold.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = <-erased; err != nil {
		t.Fatal("actual account erasure failed", err)
	}
	result := <-admitted
	if result.allowed || result.err == nil {
		t.Fatal("erased actor admitted", result.allowed, result.err)
	}
	var actors, rows int
	if err = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_user WHERE id=$1),(SELECT count(*) FROM safety_go_actionbudget WHERE user_id=$1)`, a.ID).Scan(&actors, &rows); err != nil || actors != 0 || rows != 0 {
		t.Fatal("erasure retained account/budget", actors, rows, err)
	}
}

func TestPostgresUnsafeFixedWindowLeftoverExpiryAndFreeRepeat(t *testing.T) {
	s, _, a := fixedWindowErasureFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.Config.Now = func() time.Time { return now }
	soc := social.New(s.DB, platform.RecordAudit)
	s.Config.CanSeeActivity = soc.CanSeeActivity
	place := testdb.Place(t, s.DB, "Synthetic unsafe fixture venue", "osm")
	var typ int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	aid, err := soc.CreateActivity(ctx, a, social.ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic unsafe budget activity", StartsAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	s.RatePolicies = map[string]budgets.Policy{"unsafe_report": {Limit: 1, Window: 90 * time.Second}}
	if _, err = s.DB.Exec(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) SELECT $1,'earlier'||i::text,1,$2::timestamptz-interval '2 hours' FROM generate_series(1,256) i`, a.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO safety_go_actionbudget(user_id,action,count,until) VALUES($1,'unsafe_report',2147483647,$2::timestamptz-interval '1 hour')`, a.ID, now); err != nil {
		t.Fatal(err)
	}
	first, err := s.UnsafeReport(ctx, a, aid)
	if err != nil || first.Repeat {
		t.Fatal(first, err)
	}
	second, err := s.UnsafeReport(ctx, a, aid)
	if err != nil || !second.Repeat || first.ReportID != second.ReportID {
		t.Fatal("repeat spent an unsafe debit", second, err)
	}
	var count int
	var until time.Time
	if err = s.DB.QueryRow(ctx, `SELECT count,until FROM safety_go_actionbudget WHERE user_id=$1 AND action='unsafe_report'`, a.ID).Scan(&count, &until); err != nil || count != 1 || !until.Equal(now.Add(90*time.Second)) {
		t.Fatal("unsafe fixed-window reset/expiry changed", count, until, err)
	}
}
