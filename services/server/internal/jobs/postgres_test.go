package jobs

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var jobsDSN = flag.String("jobs-test-dsn", "", "disposable native maintenance database")

func jobFixture(t *testing.T) *Runner {
	t.Helper()
	if *jobsDSN == "" {
		t.Skip("explicit disposable jobs-test-dsn not supplied")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, *jobsDSN)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("jobs_native_test_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	rows, err := admin.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	for _, table := range tables {
		if _, err = admin.Exec(ctx, "CREATE TABLE "+quoted+"."+pgx.Identifier{table}.Sanitize()+" (LIKE public."+pgx.Identifier{table}.Sanitize()+" INCLUDING ALL)"); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"django_content_type", "taxonomy_activitycategory", "taxonomy_activitytype"} {
		if _, err = admin.Exec(ctx, "INSERT INTO "+quoted+"."+table+" SELECT * FROM public."+table); err != nil {
			t.Fatal(err)
		}
	}
	config, err := pgxpool.ParseConfig(*jobsDSN)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = name + ",public"
	config.MaxConns = 4
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+quoted+" CASCADE")
		admin.Close()
	})
	store := accounts.NewStore(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	auth, err := authcore.New(authcore.Config{PublicURL: "https://app.example"}, store)
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.New(db, auth, "generated-test-binding-secret-32-bytes", accounts.Config{})
	if err = acc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	safe := safety.New(db, safety.Config{Accounts: acc})
	if err = safe.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Accounts = acc
	cfg.Safety = safe
	cfg.Social = social.New(db, platform.RecordAudit)
	if err = media.EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	cfg.Media = media.NewService(db, nil, nil, media.TokenCodec{}, nil)
	return New(db, cfg)
}

func readPackFixture(t *testing.T) PackRead {
	t.Helper()
	raw, err := os.ReadFile("testdata/roedu-page.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var page map[string]any
	if err = decoder.Decode(&page); err != nil {
		t.Fatal(err)
	}
	items := []map[string]any{}
	for _, item := range page["items"].([]any) {
		items = append(items, item.(map[string]any))
	}
	return PackRead{Items: items, Pack: SocialPack, Snapshot: stringValue(page, "snapshot_id"), Release: stringValue(page, "release_id"), Generated: stringValue(page, "snapshot_generated_at"), Mode: "full", Complete: true}
}
func TestNativeFeedNamespacingSavedSearchLedgerAndPublicIndexNow(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	pack := readPackFixture(t)
	if _, err := r.ApplyRoedu(ctx, pack, "Cluj-Napoca"); err != nil {
		t.Fatal(err)
	}
	var placeID, typeID int64
	if err := r.DB.QueryRow(ctx, `SELECT id FROM places_place ORDER BY id LIMIT 1`).Scan(&placeID); err != nil {
		t.Fatal(err)
	}
	if err := r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='reading'`).Scan(&typeID); err != nil {
		t.Fatal(err)
	}
	owner := jobUser(t, r, "generated-owner", "adult", "adult")
	saver := jobUser(t, r, "generated-saver", "adult", "adult")
	actor, err := accounts.NewStore(r.DB).Actor(ctx, strconv.FormatInt(owner, 10))
	if err != nil {
		t.Fatal(err)
	}
	activityID, err := r.Config.Social.CreateActivity(ctx, actor, social.ActivityInput{Place: placeID, ActivityType: typeID, Title: "Generated private meetup", StartsAt: r.Config.Now().Add(24 * time.Hour), MeetingPoint: "Private meetup logistics"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.DB.Exec(ctx, `INSERT INTO saved_searches_savedsearch(user_id,cohort,activity_type_id,beginners,cost_band,coarse_window,created_at) VALUES($1,'adult',$2,false,'','',now())`, saver, typeID); err != nil {
		t.Fatal(err)
	}
	summary, err := r.MatchSavedSearches(ctx)
	if err != nil || summary.Scanned != 1 || summary.Notified != 1 || summary.Skipped != 0 {
		t.Fatal("saved-search matching SQL or notices", summary, err)
	}
	summary, err = r.MatchSavedSearches(ctx)
	if err != nil || summary.Scanned != 0 || summary.Notified != 0 {
		t.Fatal("saved-search notice replay", summary, err)
	}
	for _, name := range []string{"GeneratedFeedA", "GeneratedFeedB"} {
		if _, err = r.DB.Exec(ctx, `INSERT INTO events_eventfeed(name,url,place_id,activity_type_id,is_active,last_status,created_at) VALUES($1,'https://generated.example/feed.ics',$2,$3,true,'',now())`, name, placeID, typeID); err != nil {
			t.Fatal(err)
		}
	}
	feedDate := r.Config.Now().Add(48 * time.Hour).UTC().Format("20060102T150405Z")
	r.Config.FetchFeed = func(context.Context, string) ([]byte, error) {
		return []byte("BEGIN:VEVENT\nUID:generated-shared\nSUMMARY:Generated reading event\nDTSTART:" + feedDate + "\nEND:VEVENT"), nil
	}
	feeds, err := r.SyncFeeds(ctx)
	if err != nil || feeds["events"] != 2 || feeds["failed_feeds"] != 0 {
		t.Fatal("feed namespacing import", feeds, err)
	}
	if _, err = r.SyncFeeds(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM events_event WHERE source='ical'`).Scan(&count); err != nil || count != 2 {
		t.Fatal("feed UID cross-source collision/replay", count, err)
	}
	r.Config.IndexNowEnabled = true
	r.Config.IndexNowKey = "generated-indexnow-key"
	r.Config.SiteBaseURL = "https://public-generated.example"
	var sent map[string]any
	r.Config.IndexNowHTTP = &http.Client{Transport: transportFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Hostname() != "api.indexnow.org" {
			t.Fatal("unexpected outbound host")
		}
		raw, _ := io.ReadAll(req.Body)
		if json.Unmarshal(raw, &sent) != nil {
			t.Fatal("invalid IndexNow payload")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if _, err = r.IndexNow(ctx); err != nil {
		t.Fatal(err)
	}
	if sent == nil {
		t.Fatal("public URLs not submitted")
	}
	for _, raw := range sent["urlList"].([]any) {
		url := raw.(string)
		if strings.Contains(url, "/activities/") || strings.Contains(url, strconv.FormatInt(activityID, 10)+"/generated-private-meetup") || strings.Count(strings.TrimPrefix(url, "https://public-generated.example"), "/") != 4 {
			t.Fatal("private or noncanonical URL indexed", url)
		}
	}
	changed := pack
	changed.Snapshot = "sha256-" + strings.Repeat("f", 64)
	changed.Release = changed.Snapshot
	changed.Items = []map[string]any{}
	if _, err = r.ApplyRoedu(ctx, changed, "Cluj-Napoca"); err == nil {
		t.Fatal("different immutable snapshot with same timestamp accepted")
	}
}

func TestNativeAllDueSQLSmokeOnIsolatedEmptyFixture(t *testing.T) {
	r := jobFixture(t)
	results, err := r.RunDue(context.Background())
	if err != nil {
		for _, result := range results {
			if result.Status != "ok" {
				_, detail := r.Run(context.Background(), result.Name, nil)
				t.Errorf("due SQL failed: %s: %v", result.Name, detail)
			}
		}
		t.Fatal(err)
	}
	if len(results) != 27 {
		t.Fatal("native due inventory drift")
	}
}

func jobUser(t *testing.T, r *Runner, name, band, cohort string) int64 {
	t.Helper()
	var id int64
	err := r.DB.QueryRow(context.Background(), `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,$2,$3,true,now(),'user',true,false,now()) RETURNING id`, name, band, cohort).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func TestNativeAssuranceAndConsentSweepNoticeMarkersAndCap(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	guardian := jobUser(t, r, "generated-guardian", "adult", "adult")
	child := jobUser(t, r, "generated-child", "under_16", "child")
	teen := jobUser(t, r, "generated-teen", "16_17", "teen")
	soon := jobUser(t, r, "generated-soon", "16_17", "teen")
	for _, id := range []int64{child, teen, soon} {
		expires := now.Add(-time.Hour)
		if id == soon {
			expires = now.Add(time.Hour)
		}
		if _, err := r.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'generated','eudi',$2,$3,$4,'{}','')`, id, map[bool]string{true: "under_16", false: "16_17"}[id == child], now.Add(-time.Hour), expires); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,created_at,updated_at) VALUES($1,$2,'parent','active',now(),now())`, guardian, soon); err != nil {
		t.Fatal(err)
	}
	summary, err := r.Config.Accounts.RunReverifySweep(ctx, now, 14, 1)
	if err != nil || summary.Paused != 1 || summary.NewlyExpired != 2 || summary.Nudged != 1 || summary.Failed != 0 {
		t.Fatal("reverify safety cap or nudge failed", summary, err)
	}
	summary, err = r.Config.Accounts.RunReverifySweep(ctx, now, 14, 1)
	if err != nil || summary.Paused != 1 || summary.NewlyExpired != 1 || summary.Nudged != 0 {
		t.Fatal("proof notices repeated or backlog unprocessed", summary, err)
	}
	summary, err = r.Config.Accounts.RunReverifySweep(ctx, now, 14, 1)
	if err != nil || summary.Paused != 0 || summary.NewlyExpired != 0 || summary.Nudged != 0 {
		t.Fatal("standing proof backlog miscounted", summary, err)
	}
	for _, expires := range []time.Time{now.Add(-time.Hour), now.Add(time.Hour)} {
		if _, err = r.DB.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,renewal_notice,created_at,updated_at) VALUES($1,'generated-guardian','active','participation',$2,$3,'',now(),now())`, child, now.Add(-time.Hour), expires); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = r.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,created_at,updated_at) VALUES($1,$2,'parent','active',now(),now())`, guardian, child); err != nil {
		t.Fatal(err)
	}
	summary, err = r.Config.Accounts.RunConsentRenewalSweep(ctx, now, 14, 10)
	if err != nil || summary.Paused != 0 || summary.Nudged != 1 {
		t.Fatal("last-valid consent precedence lost", summary, err)
	}
	summary, err = r.Config.Accounts.RunConsentRenewalSweep(ctx, now.Add(2*time.Hour), 14, 10)
	if err != nil || summary.Paused != 1 || summary.NewlyExpired != 1 {
		t.Fatal("consent lapse not enforced", summary, err)
	}
	summary, err = r.Config.Accounts.RunConsentRenewalSweep(ctx, now.Add(2*time.Hour), 14, 10)
	if err != nil || summary.Paused != 0 || summary.NewlyExpired != 0 {
		t.Fatal("consent lapse repeated", summary, err)
	}
}

func TestDeferredTransactionalDedupRetryAndConcurrentClaims(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	var calls atomic.Int32
	if err := r.Queue.Register("test.once", func(context.Context, pgx.Tx, map[string]json.RawMessage) error { calls.Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	var first, second int64
	err := platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
		var err error
		first, err = r.Queue.Enqueue(ctx, tx, "test.once", map[string]any{"item_id": 1}, ops.EnqueueOptions{DedupKey: "same"})
		if err != nil {
			return err
		}
		second, err = r.Queue.Enqueue(ctx, tx, "test.once", nil, ops.EnqueueOptions{DedupKey: "same"})
		return err
	})
	if err != nil || first != second {
		t.Fatal("transactional dedup failed", err)
	}
	var wait sync.WaitGroup
	for i := 0; i < 4; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := r.Queue.RunPending(ctx, 10); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatal("duplicate task claim", calls.Load())
	}
	if err = r.Queue.Register("test.retry", func(ctx context.Context, tx pgx.Tx, _ map[string]json.RawMessage) error {
		_, err := tx.Exec(ctx, `SELECT 1/0`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err = platform.Transaction(ctx, r.DB, func(tx pgx.Tx) error {
		_, err := r.Queue.Enqueue(ctx, tx, "test.retry", nil, ops.EnqueueOptions{MaxAttempts: 2})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := r.Queue.RunPending(ctx, 1)
	if err != nil || summary.Retried != 1 {
		t.Fatal("handler savepoint/retry failed", summary, err)
	}
	if _, err = r.DB.Exec(ctx, `UPDATE ops_deferredtask SET available_at=now() WHERE kind='test.retry'`); err != nil {
		t.Fatal(err)
	}
	summary, err = r.Queue.RunPending(ctx, 1)
	if err != nil || summary.Failed != 1 {
		t.Fatal("poison task never exhausted", summary, err)
	}
}

func TestNativeMaintenanceRetentionAndSnapshotPrivacy(t *testing.T) {
	r := jobFixture(t)
	ctx := context.Background()
	var userID int64
	err := r.DB.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),'generated-member','Generated','adult','adult',true,now(),'user',true,false,now()) RETURNING id`).Scan(&userID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"arrival", "moderation", "system"} {
		if _, err = r.DB.Exec(ctx, `INSERT INTO notifications_notification(recipient_id,kind,title,body,url,read_at,created_at) VALUES($1,$2,'generated','','',now(),now()-interval '200 days')`, userID, kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = r.Run(ctx, "purge_read_notifications", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Run(ctx, "process_deferred_tasks", nil); err != nil {
		t.Fatal(err)
	}
	var remaining int
	_ = r.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification`).Scan(&remaining)
	if remaining != 2 {
		t.Fatal("DSA notice retention violated", remaining)
	}
	fixture, err := os.ReadFile("testdata/roedu-page.json")
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(fixture)))
	decoder.UseNumber()
	var page map[string]any
	if err = decoder.Decode(&page); err != nil {
		t.Fatal(err)
	}
	items := []map[string]any{}
	for _, item := range page["items"].([]any) {
		items = append(items, item.(map[string]any))
	}
	pack := PackRead{Items: items, Pack: SocialPack, Snapshot: page["snapshot_id"].(string), Release: page["release_id"].(string), Generated: page["snapshot_generated_at"].(string), Mode: "full", Complete: true}
	if _, err = r.ApplyRoedu(ctx, pack, "Cluj-Napoca"); err != nil {
		t.Fatal("native fact import", err)
	}
	dir := t.TempDir()
	r.Config.AgentSnapshotDir = dir
	if _, err = r.ExportAgentSnapshot(ctx, nil); err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if json.Unmarshal(manifest, &meta) != nil || meta["schema_version"] != float64(2) {
		t.Fatal("snapshot metadata invalid")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "activities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "owner") || strings.Contains(string(raw), "meeting_point") || strings.Contains(string(raw), "generated-member") {
		t.Fatal("private activity metadata exported")
	}
}

func TestNativeDueTickContinuesAndOnlyGreenHeartbeats(t *testing.T) {
	r := New(nil, DefaultConfig())
	var calls int
	for _, name := range DueNames {
		n := name
		r.handlers[name] = func(context.Context, map[string]json.RawMessage) (any, error) {
			calls++
			if n == "purge_messaging" {
				return nil, fmt.Errorf("generated failure")
			}
			return map[string]int{"processed": 1}, nil
		}
	}
	heartbeats := 0
	r.Config.Heartbeat = func(context.Context) bool { heartbeats++; return true }
	out, err := r.RunDue(context.Background())
	if err == nil || len(out) != 27 || calls != 27 || heartbeats != 0 {
		t.Fatal("failed tick skipped duty or falsely heartbeated")
	}
	r.handlers["purge_messaging"] = func(context.Context, map[string]json.RawMessage) (any, error) { return 1, nil }
	if _, err = r.RunDue(context.Background()); err != nil || heartbeats != 1 {
		t.Fatal("green heartbeat missing", err)
	}
}
