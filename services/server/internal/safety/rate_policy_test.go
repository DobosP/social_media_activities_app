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

func TestPostgresUnsafeRatePolicyKeepsRepeatFreeAndShared(t *testing.T) {
	s := safetyFixture(t)
	a := user(t, s, "synthetic-unsafe-rate-owner", false)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	s.Config.Now = func() time.Time { return now }
	s.RatePolicies = map[string]budgets.Policy{"unsafe_report": {Limit: 1, Window: 2 * time.Minute}}
	replica := New(s.DB, s.Config)
	replica.RatePolicies = s.RatePolicies
	// The service owns the safe-exit seat gate: a is a current member of
	// activities organised by someone else.
	organiser := user(t, s, "synthetic-unsafe-rate-organiser", false)
	ids := make([]int64, 2)
	for i := range ids {
		if err := s.DB.QueryRow(ctx, `INSERT INTO social_activity(title,description,starts_at,ends_at,cohort,join_threshold,owner_can_override,capacity,status,created_at,updated_at,activity_type_id,owner_id,place_id,guardian_accompanied,is_hidden,meeting_point,organizer_note,what_to_bring,accessibility_notes,beginners_welcome,cost_band,difficulty,go_confirmed_at,min_to_go,series_id,supervised,first_time_note,is_publicly_listed,cost_amount,cost_note) VALUES('Synthetic unsafe rate meetup','',now()+interval '1 day',NULL,'adult',0.666666,false,NULL,'open',now(),now(),1,$1,1,false,false,'','','','',false,'unspecified','unspecified',NULL,NULL,NULL,false,'',false,NULL,'') RETURNING id`, organiser.ID).Scan(&ids[i]); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, ids[i], a.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.UnsafeReport(ctx, a, ids[0])
	if err != nil || first.Repeat {
		t.Fatal(first, err)
	}
	repeated, err := replica.UnsafeReport(ctx, a, ids[0])
	if err != nil || !repeated.Repeat || repeated.ReportID != first.ReportID {
		t.Fatal("repeat spent an unsafe rate token", repeated, err)
	}
	if _, err := replica.UnsafeReport(ctx, a, ids[1]); err != ErrRate {
		t.Fatal("unsafe rate policy was not shared", err)
	}
	var count int
	var until time.Time
	if err := s.DB.QueryRow(ctx, `SELECT count,until FROM safety_go_actionbudget WHERE user_id=$1 AND action='unsafe_report'`, a.ID).Scan(&count, &until); err != nil || count != 1 || !until.Equal(now.Add(2*time.Minute)) {
		t.Fatal(count, until, err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := replica.UnsafeReport(ctx, a, ids[1]); err != nil {
		t.Fatal("unsafe policy expiry stayed closed", err)
	}
}
