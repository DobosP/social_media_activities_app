package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/ops"
	"github.com/jackc/pgx/v5"
	"testing"
	"time"
)

func TestJobPolicyValidation(t *testing.T) {
	c := DefaultConfig()
	if err := c.ValidatePolicy(); err != nil {
		t.Fatal(err)
	}
	c.DeferredBatch = 0
	if c.ValidatePolicy() == nil {
		t.Fatal("zero batch allowed")
	}
	c = DefaultConfig()
	c.SavedSearchMatchBatch = 10001
	if c.ValidatePolicy() == nil {
		t.Fatal("unbounded search batch allowed")
	}
}
func TestPostgresSavedSearchMatchBatchActuallyBoundsLedger(t *testing.T) {
	r := jobFixture(t)
	r.Config.SavedSearchMatchBatch = 1
	ctx := context.Background()
	pack := readPackFixture(t)
	if _, err := r.ApplyRoedu(ctx, pack, "Cluj-Napoca"); err != nil {
		t.Fatal(err)
	}
	var owner, user, place, typ int64
	for i, dst := range []*int64{&owner, &user} {
		if err := r.DB.QueryRow(ctx, `INSERT INTO accounts_user(password,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',false,gen_random_uuid(),$1,$1,'adult','adult',true,now(),'user',true,false,now()) RETURNING id`, fmt.Sprintf("match-policy-%d", i)).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.DB.QueryRow(ctx, `SELECT id FROM places_place ORDER BY id LIMIT 1`).Scan(&place); err != nil {
		t.Fatal(err)
	}
	if err := r.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := r.DB.Exec(ctx, `INSERT INTO social_activity(owner_id,place_id,activity_type_id,title,description,starts_at,cohort,join_threshold,guardian_accompanied,supervised,meeting_point,what_to_bring,organizer_note,first_time_note,cost_band,cost_note,difficulty,accessibility_notes,beginners_welcome,status,is_hidden,is_publicly_listed,owner_can_override,created_at,updated_at) VALUES($1,$2,$3,'Synthetic match','',$4,'adult',0.6666666666666666,false,false,'','','','','free','','','',false,'open',false,false,true,now(),now())`, owner, place, typ, time.Now().Add(time.Duration(i+1)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.DB.Exec(ctx, `INSERT INTO saved_searches_savedsearch(user_id,cohort,activity_type_id,beginners,cost_band,coarse_window,created_at) VALUES($1,'adult',$2,false,'','',now())`, user, typ); err != nil {
		t.Fatal(err)
	}
	result, err := r.MatchSavedSearches(ctx)
	if err != nil || result.Scanned != 1 {
		t.Fatal("search batch ignored", result, err)
	}
	var n int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM saved_searches_savedsearchmatch WHERE user_id=$1`, user).Scan(&n); err != nil || n != 1 {
		t.Fatal("ledger batch ignored", n, err)
	}
}

func TestPostgresDeferredPolicyBoundsOneDrainPass(t *testing.T) {
	r := jobFixture(t)
	r.Config.DeferredBatch = 1
	ctx := context.Background()
	handled := 0
	if err := r.Queue.Register("test.policy_batch", func(context.Context, pgx.Tx, map[string]json.RawMessage) error { handled++; return nil }); err != nil {
		t.Fatal(err)
	}
	tx, err := r.DB.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for i := 0; i < 2; i++ {
		if _, err = r.Queue.Enqueue(ctx, tx, "test.policy_batch", nil, ops.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Run(ctx, "process_deferred_tasks", nil); err != nil {
		t.Fatal(err)
	}
	if handled != 1 {
		t.Fatal("deferred batch ignored", handled)
	}
	var pending int
	if err = r.DB.QueryRow(ctx, `SELECT count(*) FROM ops_deferredtask WHERE kind='test.policy_batch' AND status='PENDING'`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("deferred queue wasn't left for next pass", pending, err)
	}
}
