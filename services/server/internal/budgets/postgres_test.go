package budgets_test

import (
	"context"
	"flag"
	"fmt"
	"net/http/httptest"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var budgetDSN = flag.String("budgets-test-dsn", "", "explicit disposable shared-budget fixture")
var secret = []byte("synthetic-stable-replica-budget-key-32-bytes")

func fixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if *budgetDSN == "" {
		t.Skip("explicit disposable test DSN not supplied")
	}
	return testdb.New(t, *budgetDSN, nil)
}

func capacity(t *testing.T, db *pgxpool.Pool) (int, int) {
	t.Helper()
	var keys, events, actualKeys, actualEvents int
	if err := db.QueryRow(context.Background(), `SELECT keys,events FROM go_rate_budget_capacity WHERE singleton`).Scan(&keys, &events); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `SELECT count(*),coalesce(sum(cardinality(events)),0) FROM go_rate_budget`).Scan(&actualKeys, &actualEvents); err != nil {
		t.Fatal(err)
	}
	if keys != actualKeys || events != actualEvents {
		t.Fatal("capacity drift", keys, events, actualKeys, actualEvents)
	}
	return keys, events
}

func TestPostgresBootstrapAndAdoption(t *testing.T) {
	if *budgetDSN == "" {
		t.Skip("explicit disposable test DSN not supplied")
	}
	ctx := context.Background()
	db, err := pgxpool.New(ctx, *budgetDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for i := 0; i < 2; i++ {
		if err := schema.Migrate(ctx, db); err != nil {
			t.Fatal("bootstrap/adoption", err)
		}
	}
}

func TestPostgresMultiReplicaSlidingAndPrivacy(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	actor := testdb.Actor(t, db, "synthetic-budget-owner", "adult")
	stores := []*budgets.Store{budgets.New(db), budgets.New(db)}
	p := budgets.Policy{Limit: 3, Window: time.Minute}
	var allowed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			d, err := stores[i%2].Actor(ctx, actor.ID, "test.action", p)
			if err != nil {
				t.Error(err)
			}
			if d.Allowed {
				allowed.Add(1)
			}
		}(i)
	}
	wg.Wait()
	if allowed.Load() != 3 {
		t.Fatal("replicas multiplied budget", allowed.Load())
	}
	d, err := budgets.New(db).Actor(ctx, actor.ID, "test.action", p)
	if err != nil || d.Allowed || d.RetryAfter <= 0 || d.RetryAfter > time.Minute {
		t.Fatal("restart/retry", d, err)
	}
	// Controlled DB timestamps establish a rolling boundary without wall sleeps.
	if _, err := db.Exec(ctx, `UPDATE go_rate_budget SET events=ARRAY[clock_timestamp()-interval '61 seconds',clock_timestamp()-interval '30 seconds',clock_timestamp()-interval '20 seconds'],expires_at=clock_timestamp()+interval '40 seconds' WHERE scope='test.action'`); err != nil {
		t.Fatal(err)
	}
	d, err = stores[1].Actor(ctx, actor.ID, "test.action", p)
	if err != nil || !d.Allowed {
		t.Fatal("old event did not slide out", d, err)
	}
	d, err = stores[0].Actor(ctx, actor.ID, "test.action", p)
	if err != nil || d.Allowed || d.RetryAfter > 31*time.Second {
		t.Fatal("window reset allowed boundary burst", d, err)
	}
	if d, err = stores[0].Actor(ctx, actor.ID, "test.other", p); err != nil || !d.Allowed {
		t.Fatal("scopes not independent", d, err)
	}
	if d, err = stores[0].Actor(ctx, actor.ID, "test.action", budgets.Policy{Limit: 4, Window: time.Second}); err != nil || d.Allowed {
		t.Fatal("mismatched replica reset live policy", d, err)
	}
	peer := "192.0.2.55"
	p = budgets.Policy{Limit: 1, Window: time.Minute}
	for i := 0; i < 2; i++ {
		d, err = stores[i].Peer(ctx, secret, peer, "api.anonymous", p)
		if err != nil || d.Allowed != (i == 0) {
			t.Fatal("peer replica budget", d, err)
		}
	}
	if d, err = stores[1].Peer(ctx, secret, peer, "api.token", p); err != nil || !d.Allowed {
		t.Fatal("token scope", d, err)
	}
	var raw string
	if err := db.QueryRow(ctx, `SELECT json_agg(r)::text FROM go_rate_budget r`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, peer) || strings.Contains(raw, "synthetic-budget-owner") || strings.Contains(raw, string(secret)) {
		t.Fatal("personal peer/secret stored in rate history")
	}
	keys, _ := capacity(t, db)
	if keys != 4 {
		t.Fatal(keys)
	}
	if _, err := db.Exec(ctx, `DELETE FROM accounts_user WHERE id=$1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	keys, _ = capacity(t, db)
	if keys != 2 {
		t.Fatal("erasure retained actor budgets", keys)
	}
}

func TestPostgresCapacityExpiryAndConstantMemory(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	// Fixture-only bulk fill; serving never allocates all histories in memory.
	_, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) SELECT 'test.fill',decode(md5(i::text)||md5('subject'||i::text),'hex'),1,60000000,ARRAY[clock_timestamp()],clock_timestamp()+interval '1 minute' FROM generate_series(1,10000) i`)
	if err != nil {
		t.Fatal(err)
	}
	if keys, events := capacity(t, db); keys != 10000 || events != 10000 {
		t.Fatal(keys, events)
	}
	if d, err := store.Global(ctx, "test.new", budgets.Policy{Limit: 1, Window: time.Minute}); err != nil || d.Allowed {
		t.Fatal("identity capacity failed open", d, err)
	}
	if _, err := db.Exec(ctx, `UPDATE go_rate_budget SET events=ARRAY[clock_timestamp()-interval '2 minutes'],expires_at=clock_timestamp()-interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if d, err := store.Global(ctx, "test.new", budgets.Policy{Limit: 1, Window: time.Minute}); err != nil || !d.Allowed {
		t.Fatal("expiry sweep did not reclaim capacity", d, err)
	}
	if keys, _ := capacity(t, db); keys != 9745 {
		t.Fatal("sweep not bounded", keys)
	}
	if _, err := db.Exec(ctx, `DELETE FROM go_rate_budget`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) SELECT 'test.events',decode(md5(i::text)||md5('subject'||i::text),'hex'),10000,60000000,array_fill(clock_timestamp(),ARRAY[10000]),clock_timestamp()+interval '1 minute' FROM generate_series(1,100) i`)
	if err != nil {
		t.Fatal(err)
	}
	if _, events := capacity(t, db); events != 1000000 {
		t.Fatal(events)
	}
	if d, err := store.Global(ctx, "test.more", budgets.Policy{Limit: 1, Window: time.Minute}); err != nil || d.Allowed {
		t.Fatal("total event capacity failed open", d, err)
	}
	capacity(t, db)
	// Store has one pool pointer; identities/events are never resident Go maps.
	if fmt.Sprintf("%T", store.DB) != "*pgxpool.Pool" {
		t.Fatal("unexpected history storage")
	}
}

func TestPostgresReplayLowPoolFreshGatesAndFailedAttempts(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	actor := testdb.Actor(t, db, "synthetic-replay-owner", "adult")
	cfg := db.Config()
	cfg.MaxConns = 2
	small, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close()
	if _, err = small.Exec(ctx, `CREATE TABLE budget_effect(id bigserial PRIMARY KEY,allowed boolean);INSERT INTO budget_effect(allowed) VALUES(true)`); err != nil {
		t.Fatal(err)
	}
	store := budgets.New(small)
	var wg sync.WaitGroup
	var attempts atomic.Int32
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := budgets.Reserve(func(reserve func() error) error {
				return platform.Transaction(ctx, small, func(tx pgx.Tx) error {
					if _, err := tx.Exec(ctx, `SELECT id FROM accounts_user WHERE id=$1 FOR UPDATE`, actor.ID); err != nil {
						return err
					}
					if err := reserve(); err != nil {
						return err
					}
					attempts.Add(1)
					return platform.ErrInvalid
				})
			}, func() (budgets.Decision, error) {
				return store.Actor(ctx, actor.ID, "test.rollback", budgets.Policy{Limit: 3, Window: time.Minute})
			})
			if err != platform.ErrInvalid && err != budgets.ErrDenied {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if attempts.Load() != 3 {
		t.Fatal("failed mutation refunded or multiplied attempts", attempts.Load())
	}
	capacity(t, db)
	passes := 0
	err = budgets.Reserve(func(reserve func() error) error {
		return platform.Transaction(ctx, small, func(tx pgx.Tx) error {
			passes++
			var yes bool
			if err := tx.QueryRow(ctx, `SELECT allowed FROM budget_effect WHERE id=1 FOR UPDATE`).Scan(&yes); err != nil {
				return err
			}
			if !yes {
				return platform.ErrForbidden
			}
			if err := reserve(); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO budget_effect(allowed) VALUES(true)`)
			return err
		})
	}, func() (budgets.Decision, error) {
		if _, err := small.Exec(ctx, `UPDATE budget_effect SET allowed=false WHERE id=1`); err != nil {
			return budgets.Decision{}, err
		}
		return store.Actor(ctx, actor.ID, "test.fresh", budgets.Policy{Limit: 1, Window: time.Minute})
	})
	if err != platform.ErrForbidden || passes != 2 {
		t.Fatal("replay trusted stale gate", passes, err)
	}
	var effects int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM budget_effect`).Scan(&effects); err != nil || effects != 1 {
		t.Fatal("preflight leaked effect", effects, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if d, err := store.Actor(cancelled, actor.ID, "test.cancel", budgets.Policy{Limit: 1, Window: time.Minute}); err == nil || d.Allowed {
		t.Fatal("cancelled request admitted", d, err)
	}
	small.Close()
	if d, err := store.Actor(ctx, actor.ID, "test.outage", budgets.Policy{Limit: 1, Window: time.Minute}); err == nil || d.Allowed {
		t.Fatal("closed database pool admitted", d, err)
	}
}

func TestPostgresCSPReplicaIngressKeepsReportsNondurable(t *testing.T) {
	db := fixture(t)
	replicas := []*ops.Service{ops.NewService(db, ops.HTTPConfig{}), ops.NewService(db, ops.HTTPConfig{})}
	for i := 0; i < 121; i++ {
		w := httptest.NewRecorder()
		replicas[i%2].CSPReport(w, httptest.NewRequest("POST", "/api/ops/csp-report/", strings.NewReader(`{"csp-report":{"effective-directive":"script-src self","document-uri":"https://site.example/private/?token=synthetic","blocked-uri":"https://asset.example/file.js?secret=synthetic"}}`)))
		if w.Code != 204 {
			t.Fatal(w.Code)
		}
	}
	if len(replicas[0].RecentCSP())+len(replicas[1].RecentCSP()) != 120 {
		t.Fatal("CSP quota multiplied across replicas")
	}
	keys, events := capacity(t, db)
	if keys != 1 || events != 120 {
		t.Fatal(keys, events)
	}
	var raw string
	if err := db.QueryRow(context.Background(), `SELECT json_agg(r)::text FROM go_rate_budget r`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "site.example") || strings.Contains(raw, "token") || strings.Contains(raw, "file.js") {
		t.Fatal("CSP content persisted in budget")
	}
}

func TestPostgresCardinalityCostEvidence(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	p := budgets.Policy{Limit: 10000, Window: time.Hour}
	for _, cardinality := range []int{1, 1000, 9000} {
		if _, err := db.Exec(ctx, `DELETE FROM go_rate_budget`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) SELECT 'test.cardinality',decode(md5(i::text)||md5('subject'||i::text),'hex'),1,3600000000,ARRAY[clock_timestamp()],clock_timestamp()+interval '1 hour' FROM generate_series(1,$1::int) i`, cardinality); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		samples := make([]time.Duration, 100)
		for i := 0; i < 100; i++ {
			callStart := time.Now()
			if d, err := store.Global(ctx, "test.measure", p); err != nil || !d.Allowed {
				t.Fatal(d, err)
			}
			samples[i] = time.Since(callStart)
		}
		sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
		t.Logf("synthetic buckets=%d: 100 sequential admissions=%s, mean=%s, p95=%s", cardinality, time.Since(start), time.Since(start)/100, samples[94])
		capacity(t, db)
	}
}

func TestPostgresMemoryBoundAndConcurrentExpiryErasure(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	p := budgets.Policy{Limit: 1, Window: time.Minute}
	for i := 0; i < 100; i++ {
		if d, err := store.Peer(ctx, secret, fmt.Sprintf("192.0.2.synthetic.%d", i), "test.memory", p); err != nil || !d.Allowed {
			t.Fatal(d, err)
		}
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 100; i < 2100; i++ {
		if d, err := store.Peer(ctx, secret, fmt.Sprintf("192.0.2.synthetic.%d", i), "test.memory", p); err != nil || !d.Allowed {
			t.Fatal(d, err)
		}
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	growth := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	t.Logf("synthetic 2000 additional identities: retained Go heap delta=%d bytes", growth)
	if growth > 4<<20 {
		t.Fatal("per-identity history retained in process memory", growth)
	}
	actor := testdb.Actor(t, db, "synthetic-concurrent-erasure", "adult")
	for i := 0; i < 8; i++ {
		if d, err := store.Actor(ctx, actor.ID, fmt.Sprintf("test.erase%d", i), p); err != nil || !d.Allowed {
			t.Fatal(d, err)
		}
	}
	if _, err := db.Exec(ctx, `UPDATE go_rate_budget SET expires_at=clock_timestamp()-interval '1 minute' WHERE user_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := db.Exec(ctx, `SELECT go_prune_rate_budgets(256)`); err != nil {
				t.Error(err)
			}
			if d, err := store.Global(ctx, fmt.Sprintf("test.concurrent%d", i), p); err != nil || !d.Allowed {
				t.Error(d, err)
			}
		}(i)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := db.Exec(ctx, `DELETE FROM accounts_user WHERE id=$1`, actor.ID); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	keys, events := capacity(t, db)
	if keys != 16 || events != 16 {
		t.Fatal("concurrent sweep/erasure retained stale history or drifted capacity", keys, events)
	}
}
