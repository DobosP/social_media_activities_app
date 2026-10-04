package safety

import (
	"context"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestSafetyRatePolicyInvalidRefusesBeforeDatabase(t *testing.T) {
	for _, policy := range []budgets.Policy{{Window: time.Hour}, {Limit: -1, Window: time.Hour}, {Limit: 1}, {Limit: 1, Window: -time.Second}} {
		s := New(nil, Config{})
		s.RatePolicies = map[string]budgets.Policy{"report": policy}
		if allowed, err := s.allow(context.Background(), platform.Actor{ID: 1}, "report", 20, time.Hour); allowed || err == nil {
			t.Fatalf("invalid policy admitted: allowed=%v err=%v", allowed, err)
		}
	}
}

func TestPostgresSafetyRatePoliciesFixedWindowAndOverflowBound(t *testing.T) {
	s := safetyFixture(t)
	a := user(t, s, "rate-policy-actor", false)
	ctx := context.Background()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	s.Config.Now = func() time.Time { return now }
	s.RatePolicies = map[string]budgets.Policy{"report": {Limit: 1, Window: 2 * time.Minute}, "appeal": {Limit: 2, Window: 3 * time.Minute}}
	replica := New(s.DB, s.Config)
	replica.RatePolicies = s.RatePolicies
	for i, service := range []*Service{s, replica, s} {
		allowed, err := service.allow(ctx, a, "report", 20, time.Hour)
		if err != nil || allowed != (i == 0) {
			t.Fatalf("report policy ignored: attempt=%d allowed=%v err=%v", i, allowed, err)
		}
	}
	var count int
	var until time.Time
	if err := s.DB.QueryRow(ctx, `SELECT count,until FROM safety_go_actionbudget WHERE user_id=$1 AND action='report'`, a.ID).Scan(&count, &until); err != nil {
		t.Fatal(err)
	}
	if count != 1 || !until.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("custom report policy ignored: count=%d until=%s", count, until)
	}
	for i := range 3 {
		allowed, err := replica.allow(ctx, a, "appeal", 5, 24*time.Hour)
		if err != nil || allowed != (i < 2) {
			t.Fatalf("appeal policy leaked another action's count: attempt=%d allowed=%v err=%v", i, allowed, err)
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE safety_go_actionbudget SET count=2147483647 WHERE user_id=$1 AND action='report'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if allowed, err := replica.allow(ctx, a, "report", 20, time.Hour); allowed || err != nil {
		t.Fatalf("denied attempt overflowed persisted count: allowed=%v err=%v", allowed, err)
	}
	now = now.Add(2 * time.Minute)
	if allowed, err := replica.allow(ctx, a, "report", 20, time.Hour); !allowed || err != nil {
		t.Fatalf("fixed expiry did not reopen: allowed=%v err=%v", allowed, err)
	}
	if allowed, err := replica.allow(ctx, a, "appeal", 5, 24*time.Hour); allowed || err != nil {
		t.Fatalf("report expiry reopened appeal too early: allowed=%v err=%v", allowed, err)
	}
}
