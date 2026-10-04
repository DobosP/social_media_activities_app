package accounts

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
)

func TestAccountRatePolicyInvalidRefusesBeforeDatabase(t *testing.T) {
	for _, policy := range []budgets.Policy{{Limit: 0, Window: time.Hour}, {Limit: -1, Window: time.Hour}, {Limit: 1}, {Limit: 1, Window: -time.Second}} {
		s := New(nil, nil, "", Config{})
		s.RatePolicies = map[string]budgets.Policy{"guardian_invite": policy}
		if allowed, err := s.allowAction(context.Background(), 1, "guardian_invite", 20, time.Hour); allowed || err == nil {
			t.Fatalf("invalid policy admitted: allowed=%v err=%v", allowed, err)
		}
	}
}

func TestPostgresAccountRatePoliciesSharedBoundedAndFixedExpiry(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "rate-policy-actor", "adult", "adult")
	ctx := context.Background()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Config.Now = func() time.Time { return now }
	s.RatePolicies = map[string]budgets.Policy{}
	for _, action := range []string{"age_start", "avatar_style", "guardian_invite", "guardian_guardrail", "guardian_ward_topics"} {
		s.RatePolicies[action] = budgets.Policy{Limit: 2, Window: 90 * time.Second}
	}
	replica := New(s.DB, s.Auth, "synthetic-rate-policy-binding", s.Config)
	replica.RatePolicies = s.RatePolicies
	for action := range s.RatePolicies {
		var admitted atomic.Int32
		var wg sync.WaitGroup
		for i := range 12 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				service := s
				if i%2 != 0 {
					service = replica
				}
				allowed, err := service.allowAction(ctx, a.ID, action, 30, time.Hour)
				if err != nil {
					t.Error(err)
				}
				if allowed {
					admitted.Add(1)
				}
			}()
		}
		wg.Wait()
		if got := admitted.Load(); got != 2 {
			t.Fatalf("%s admitted %d concurrent attempts, want 2", action, got)
		}
		var count int
		var until time.Time
		if err := s.DB.QueryRow(ctx, `SELECT count,until FROM accounts_go_action_budget WHERE user_id=$1 AND action=$2`, a.ID, action).Scan(&count, &until); err != nil {
			t.Fatal(err)
		}
		if count != 2 || !until.Equal(now.Add(90*time.Second)) {
			t.Fatalf("%s retained count=%d until=%s", action, count, until)
		}
	}
	now = now.Add(time.Minute)
	if allowed, err := replica.allowAction(ctx, a.ID, "guardian_invite", 20, time.Hour); allowed || err != nil {
		t.Fatalf("denial changed fixed expiry: allowed=%v err=%v", allowed, err)
	}
	now = now.Add(30 * time.Second)
	if allowed, err := replica.allowAction(ctx, a.ID, "guardian_invite", 20, time.Hour); !allowed || err != nil {
		t.Fatalf("expired policy did not reset: allowed=%v err=%v", allowed, err)
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM accounts_user WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_action_budget WHERE user_id=$1`, a.ID).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("account deletion retained budget rows: count=%d err=%v", remaining, err)
	}
}
