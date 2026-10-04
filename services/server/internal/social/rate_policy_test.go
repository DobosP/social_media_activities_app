package social

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestPostgresSocialRatePoliciesShareIdempotentConnectionAndJoinPaths(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	s.AllowUserGroups = true
	s.RatePolicies = map[string]budgets.Policy{
		"connection_request": {Limit: 2, Window: 3 * time.Minute},
		"group_join":         {Limit: 1, Window: 2 * time.Minute},
	}
	replica := New(s.DB, platform.RecordAudit)
	replica.AllowUserGroups = true
	replica.RatePolicies = s.RatePolicies
	owner := fixtureUser(t, s, "rate-owner", "adult")
	peers := []Actor{
		fixtureUser(t, s, "rate-peer-one", "adult"),
		fixtureUser(t, s, "rate-peer-two", "adult"),
		fixtureUser(t, s, "rate-peer-three", "adult"),
	}
	activity := fixtureActivity(t, s, owner, p(5))
	for _, peer := range peers {
		membership, err := s.Join(ctx, peer, activity)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Vote(ctx, owner, membership, true, true); err != nil {
			t.Fatal(err)
		}
	}
	connection, err := s.RequestConnection(ctx, owner, peers[0].PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if repeated, err := replica.RequestConnection(ctx, owner, peers[0].PublicID); err != nil || repeated != connection {
		t.Fatalf("pending connection replay spent a debit: id=%d err=%v", repeated, err)
	}
	if _, err := replica.RequestConnection(ctx, owner, peers[1].PublicID); err != nil {
		t.Fatalf("idempotent request exhausted custom budget: %v", err)
	}
	if _, err := s.RequestConnection(ctx, owner, peers[2].PublicID); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("replicas did not share connection budget: %v", err)
	}
	assertSocialRatePolicy(t, s, owner.ID, "social.connection_request", 2, 3*time.Minute, 2)
	var typ int64
	if err := s.DB.QueryRow(ctx, `SELECT activity_type_id FROM social_activity WHERE id=$1`, activity).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	groups := make([]int64, 2)
	for i := range groups {
		groups[i], err = s.CreateGroup(ctx, owner, GroupInput{City: []string{"Cluj-Napoca", "Synthetic rate city"}[i], ActivityType: &typ, Title: "Rate policy group"})
		if err != nil {
			t.Fatal(err)
		}
	}
	// Omitted group_create retains the reviewed default while custom join and
	// connection policies use independent scopes for the same actor.
	assertSocialRatePolicy(t, s, owner.ID, "social.group_create", 5, time.Hour, 2)
	if err := s.JoinGroup(ctx, peers[0], groups[0]); err != nil {
		t.Fatal(err)
	}
	if err := replica.JoinGroup(ctx, peers[0], groups[0]); err != nil {
		t.Fatalf("existing group membership spent a debit: %v", err)
	}
	if err := replica.JoinGroup(ctx, peers[0], groups[1]); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("replicas did not share group join budget: %v", err)
	}
	assertSocialRatePolicy(t, s, peers[0].ID, "social.group_join", 1, 2*time.Minute, 1)

	s.RatePolicies["connection_request"] = budgets.Policy{}
	if _, err := s.RequestConnection(ctx, peers[1], peers[2].PublicID); err == nil || errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("explicit invalid policy silently used the default: %v", err)
	}
	var invalidDebits int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM go_rate_budget WHERE user_id=$1 AND scope='social.connection_request'`, peers[1].ID).Scan(&invalidDebits); err != nil || invalidDebits != 0 {
		t.Fatalf("invalid connection policy created budget state: count=%d err=%v", invalidDebits, err)
	}
}

func assertSocialRatePolicy(t *testing.T, s *Service, actor int64, scope string, limit int, window time.Duration, events int) {
	t.Helper()
	var actualLimit, actualEvents int
	var actualWindow int64
	if err := s.DB.QueryRow(context.Background(), `SELECT policy_limit,window_us,cardinality(events) FROM go_rate_budget WHERE user_id=$1 AND scope=$2`, actor, scope).Scan(&actualLimit, &actualWindow, &actualEvents); err != nil {
		t.Fatal(err)
	}
	if actualLimit != limit || actualWindow != window.Microseconds() || actualEvents != events {
		t.Fatalf("%s retained limit=%d window_us=%d events=%d", scope, actualLimit, actualWindow, actualEvents)
	}
}
