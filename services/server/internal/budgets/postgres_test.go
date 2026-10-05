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

// capacity checks every family counter against a recount and returns totals.
func capacity(t *testing.T, db *pgxpool.Pool) (int, int) {
	t.Helper()
	var keys, events, families, drift, actualKeys, actualEvents int
	if err := db.QueryRow(context.Background(), `SELECT coalesce(sum(c.keys),0)::bigint,coalesce(sum(c.events),0)::bigint,count(*),count(*) FILTER(WHERE c.keys<>coalesce(n.keys,0) OR c.events<>coalesce(n.events,0))
 FROM go_rate_budget_family_capacity c LEFT JOIN (SELECT go_rate_budget_family(scope,user_id) AS family,count(*) AS keys,sum(cardinality(events)) AS events FROM go_rate_budget GROUP BY 1) n ON n.family=c.family`).Scan(&keys, &events, &families, &drift); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(context.Background(), `SELECT count(*),coalesce(sum(cardinality(events)),0) FROM go_rate_budget`).Scan(&actualKeys, &actualEvents); err != nil {
		t.Fatal(err)
	}
	if families != 4 || drift != 0 || keys != actualKeys || events != actualEvents {
		t.Fatal("capacity drift", families, drift, keys, events, actualKeys, actualEvents)
	}
	return keys, events
}

// family returns one family's counters after checking all of them.
func family(t *testing.T, db *pgxpool.Pool, name string) (int, int) {
	t.Helper()
	capacity(t, db)
	var keys, events int
	if err := db.QueryRow(context.Background(), `SELECT keys,events FROM go_rate_budget_family_capacity WHERE family=$1`, name).Scan(&keys, &events); err != nil {
		t.Fatal(err)
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

func TestPostgresCapacityAndExpiry(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	p := budgets.Policy{Limit: 1, Window: time.Minute}
	// Fixture-only bulk fill of the 'other' family (1,000 keys); serving never
	// allocates all histories in memory.
	_, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) SELECT 'test.fill',decode(md5(i::text)||md5('subject'||i::text),'hex'),1,60000000,ARRAY[clock_timestamp()],clock_timestamp()+interval '1 minute' FROM generate_series(1,1000) i`)
	if err != nil {
		t.Fatal(err)
	}
	if keys, events := family(t, db, "other"); keys != 1000 || events != 1000 {
		t.Fatal(keys, events)
	}
	// While concurrent admissions hold every other key nothing is evictable,
	// so the full family refuses rather than exceeding its capacity.
	held, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Rollback(ctx)
	if _, err := held.Exec(ctx, `SELECT 1 FROM go_rate_budget WHERE scope='test.fill' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	if d, err := store.Global(ctx, "test.new", p); err != nil || d.Allowed || d.RetryAfter != time.Second {
		t.Fatal("identity capacity failed open", d, err)
	}
	if err := held.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// Released, the family evicts its 64 soonest-expiring keys and admits.
	if d, err := store.Global(ctx, "test.new", p); err != nil || !d.Allowed {
		t.Fatal("full family refused a new key", d, err)
	}
	if keys, _ := family(t, db, "other"); keys != 937 {
		t.Fatal("eviction not bounded", keys)
	}
	// Expired rows wait for explicit off-request maintenance.
	if _, err := db.Exec(ctx, `UPDATE go_rate_budget SET events=ARRAY[clock_timestamp()-interval '2 minutes'],expires_at=clock_timestamp()-interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.Prune(ctx, 256); err != nil || removed != 256 {
		t.Fatal("explicit sweep", removed, err)
	}
	if keys, _ := capacity(t, db); keys != 681 {
		t.Fatal("sweep not bounded", keys)
	}
	if d, err := store.Global(ctx, "test.new", p); err != nil || !d.Allowed {
		t.Fatal("expired bucket not reusable", d, err)
	}
	if _, err := db.Exec(ctx, `DELETE FROM go_rate_budget`); err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) SELECT 'test.events',decode(md5(i::text)||md5('subject'||i::text),'hex'),10000,60000000,array_fill(clock_timestamp(),ARRAY[10000]),clock_timestamp()+interval '1 minute' FROM generate_series(1,10) i`)
	if err != nil {
		t.Fatal(err)
	}
	if _, events := family(t, db, "other"); events != 100000 {
		t.Fatal(events)
	}
	// Event saturation evicts too: all ten heavy keys fit in one batch.
	if d, err := store.Global(ctx, "test.more", p); err != nil || !d.Allowed {
		t.Fatal("event-saturated family refused a new key", d, err)
	}
	if keys, events := family(t, db, "other"); keys != 1 || events != 1 {
		t.Fatal("event eviction", keys, events)
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
	// The 120/min ceiling is per process again (ADR-0037 2026-10-05).
	if len(replicas[0].RecentCSP())+len(replicas[1].RecentCSP()) != 121 {
		t.Fatal("CSP reports dropped below the per-process ceiling")
	}
	for i := 0; i < 120; i++ {
		replicas[0].CSPReport(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/ops/csp-report/", strings.NewReader(`{"csp-report":{"effective-directive":"img-src"}}`)))
	}
	if len(replicas[0].RecentCSP()) != 120 {
		t.Fatal("per-process CSP ceiling", len(replicas[0].RecentCSP()))
	}
	// An unauthenticated flood never charges the shared pool, so no CSP
	// content or ingress state can be persisted in a budget.
	if keys, events := capacity(t, db); keys != 0 || events != 0 {
		t.Fatal("CSP ingress reached the database", keys, events)
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
		// Existing buckets sit in the anonymous family (10,000 keys), so the
		// measured 'other' key never pays for eviction.
		if _, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) SELECT 'api.anonymous',decode(md5(i::text)||md5('subject'||i::text),'hex'),1,3600000000,ARRAY[clock_timestamp()],clock_timestamp()+interval '1 hour' FROM generate_series(1,$1::int) i`, cardinality); err != nil {
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

// Owner rule (2026-10-05): never refuse new users because a table is full.
// Minted anonymous peers fill only their own family at its real seeded limit.
func TestAnonymousSaturationCannotStarveActorOrNewKeys(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	actor := testdb.Actor(t, db, "synthetic-saturation-actor", "adult")
	// 10,000 live anonymous keys; flood key i expires i milliseconds after key 1.
	if _, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) SELECT 'api.anonymous',decode(md5(i::text)||md5('flood'||i::text),'hex'),60,60000000,ARRAY[clock_timestamp()],now()+interval '30 seconds'+i*interval '1 millisecond' FROM generate_series(1,10000) i`); err != nil {
		t.Fatal(err)
	}
	if d, err := store.Actor(ctx, actor.ID, "social.thread_post", budgets.Policy{Limit: 30, Window: time.Minute}); err != nil || !d.Allowed {
		t.Fatal("anonymous saturation starved a fresh actor key", d, err)
	}
	if keys, _ := family(t, db, "anonymous"); keys != 10000 {
		t.Fatal("anonymous family not full", keys)
	}
	if d, err := store.Peer(ctx, secret, "203.0.113.9", "api.anonymous", budgets.Policy{Limit: 60, Window: time.Minute}); err != nil || !d.Allowed {
		t.Fatal("full anonymous family refused a new peer", d, err)
	}
	var evicted, next, actorRows int
	if err := db.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM go_rate_budget WHERE scope='api.anonymous' AND subject IN (SELECT decode(md5(i::text)||md5('flood'||i::text),'hex') FROM generate_series(1,64) i)),
 (SELECT count(*) FROM go_rate_budget WHERE scope='api.anonymous' AND subject=decode(md5('65')||md5('flood65'),'hex')),
 (SELECT count(*) FROM go_rate_budget WHERE user_id=$1)`, actor.ID).Scan(&evicted, &next, &actorRows); err != nil {
		t.Fatal(err)
	}
	if evicted != 0 || next != 1 || actorRows != 1 {
		t.Fatal("eviction did not take only the soonest-expiring anonymous keys", evicted, next, actorRows)
	}
	if keys, _ := family(t, db, "anonymous"); keys != 9937 {
		t.Fatal("eviction not bounded", keys)
	}
}

func TestEvictionOnlyWithinFamilyAndCountersStayExact(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	actor := testdb.Actor(t, db, "synthetic-eviction-actor", "adult")
	// Actor and anonymous keys expire before every 'other' key, so a
	// family-blind eviction would take them first.
	if _, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,user_id,policy_limit,window_us,events,expires_at)
 SELECT 'social.thread_post',decode(md5(i::text)||md5('actor'||i::text),'hex'),$1::bigint,1,60000000,ARRAY[clock_timestamp()],now()+interval '10 seconds' FROM generate_series(1,10) i
 UNION ALL SELECT 'api.anonymous',decode(md5(i::text)||md5('peer'||i::text),'hex'),NULL,1,60000000,ARRAY[clock_timestamp()],now()+interval '10 seconds' FROM generate_series(1,10) i
 UNION ALL SELECT 'test.fill',decode(md5(i::text)||md5('other'||i::text),'hex'),NULL,1,60000000,ARRAY[clock_timestamp()],now()+interval '1 minute'+i*interval '1 millisecond' FROM generate_series(1,1000) i`, actor.ID); err != nil {
		t.Fatal(err)
	}
	if keys, _ := family(t, db, "other"); keys != 1000 {
		t.Fatal("other family not full", keys)
	}
	p := budgets.Policy{Limit: 1, Window: time.Minute}
	if d, err := store.Global(ctx, "test.evict", p); err != nil || !d.Allowed {
		t.Fatal("full family refused a new key", d, err)
	}
	var actorRows, peerRows, evicted, next int
	if err := db.QueryRow(ctx, `SELECT
 (SELECT count(*) FROM go_rate_budget WHERE scope='social.thread_post' AND user_id=$1),
 (SELECT count(*) FROM go_rate_budget WHERE scope='api.anonymous'),
 (SELECT count(*) FROM go_rate_budget WHERE scope='test.fill' AND subject IN (SELECT decode(md5(i::text)||md5('other'||i::text),'hex') FROM generate_series(1,64) i)),
 (SELECT count(*) FROM go_rate_budget WHERE scope='test.fill' AND subject=decode(md5('65')||md5('other65'),'hex'))`, actor.ID).Scan(&actorRows, &peerRows, &evicted, &next); err != nil {
		t.Fatal(err)
	}
	if actorRows != 10 || peerRows != 10 || evicted != 0 || next != 1 {
		t.Fatal("eviction crossed families or skipped the soonest keys", actorRows, peerRows, evicted, next)
	}
	// Event saturation in the ops family evicts only its own heavy key.
	if _, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) VALUES('ops.flood',decode(md5('ops')||md5('flood'),'hex'),10000,60000000,array_fill(clock_timestamp(),ARRAY[10000]),now()+interval '1 minute')`); err != nil {
		t.Fatal(err)
	}
	if d, err := store.Global(ctx, "ops.new", p); err != nil || !d.Allowed {
		t.Fatal("event-saturated family refused a new key", d, err)
	}
	if keys, events := family(t, db, "ops"); keys != 1 || events != 1 {
		t.Fatal("ops event eviction", keys, events)
	}
	if keys, _ := family(t, db, "other"); keys != 937 {
		t.Fatal("ops eviction touched another family", keys)
	}
	if keys, events := family(t, db, "actor"); keys != 10 || events != 10 {
		t.Fatal("actor family changed", keys, events)
	}
	// Migration re-runs on every start: the census over a populated table is
	// exact and deletes nothing within the caps.
	before, _ := capacity(t, db)
	if err := schema.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if after, _ := capacity(t, db); after != before {
		t.Fatal("re-run migration changed histories", before, after)
	}
}

func TestTruncateResetsFamilyCounters(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	actor := testdb.Actor(t, db, "synthetic-truncate-actor", "adult")
	p := budgets.Policy{Limit: 2, Window: time.Minute}
	if d, err := store.Actor(ctx, actor.ID, "social.thread_post", p); err != nil || !d.Allowed {
		t.Fatal(d, err)
	}
	if d, err := store.Peer(ctx, secret, "192.0.2.77", "api.anonymous", p); err != nil || !d.Allowed {
		t.Fatal(d, err)
	}
	if d, err := store.Global(ctx, "ops.truncate", p); err != nil || !d.Allowed {
		t.Fatal(d, err)
	}
	if keys, _ := capacity(t, db); keys != 3 {
		t.Fatal(keys)
	}
	if _, err := db.Exec(ctx, `TRUNCATE go_rate_budget`); err != nil {
		t.Fatal(err)
	}
	var stale int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM go_rate_budget_family_capacity WHERE keys<>0 OR events<>0`).Scan(&stale); err != nil || stale != 0 {
		t.Fatal("TRUNCATE left stale family counters", stale, err)
	}
	if d, err := store.Peer(ctx, secret, "192.0.2.77", "api.anonymous", p); err != nil || !d.Allowed {
		t.Fatal(d, err)
	}
	if keys, events := capacity(t, db); keys != 1 || events != 1 {
		t.Fatal(keys, events)
	}
}

func TestAdmissionDoesNotPruneOnRequestPath(t *testing.T) {
	db := fixture(t)
	ctx := context.Background()
	store := budgets.New(db)
	expired := func(scope string) {
		t.Helper()
		if _, err := db.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,policy_limit,window_us,events,expires_at) VALUES($1::text,decode(md5($1::text)||md5('expired'),'hex'),1,1000000,ARRAY[clock_timestamp()-interval '2 seconds'],clock_timestamp()-interval '1 second')`, scope); err != nil {
			t.Fatal(err)
		}
	}
	rows := func(scope string) int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM go_rate_budget WHERE scope=$1`, scope).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	expired("test.stale")
	if d, err := store.Global(ctx, "test.live", budgets.Policy{Limit: 1, Window: time.Minute}); err != nil || !d.Allowed {
		t.Fatal(d, err)
	}
	if rows("test.stale") != 1 {
		t.Fatal("admission swept expired rows on the request path")
	}
	if removed, err := store.Prune(ctx, 1000); err != nil || removed != 1 || rows("test.stale") != 0 {
		t.Fatal("explicit prune", removed, err)
	}
	expired("test.swept")
	sweep, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		store.RunSweeper(sweep, 10*time.Millisecond, 1000)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for rows("test.swept") != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	<-done
	if rows("test.swept") != 0 {
		t.Fatal("sweeper did not prune within its ticks")
	}
	capacity(t, db)
}
