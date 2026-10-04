package messaging

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestMessagingRatePolicyInvalidRefusesBeforeDatabase(t *testing.T) {
	for _, action := range []string{"messaging_start", "messaging_send"} {
		for _, policy := range []budgets.Policy{{Window: time.Minute}, {Limit: -1, Window: time.Minute}, {Limit: 1}, {Limit: 1, Window: -time.Second}} {
			s := New(nil, platform.CursorCodec{})
			s.RatePolicies = map[string]budgets.Policy{action: policy}
			if err := s.budget(context.Background(), nil, 1, action, 20); err == nil {
				t.Fatalf("invalid %s policy admitted", action)
			}
		}
	}
}

func TestPostgresMessagingRatePoliciesPreserveReuseAndFailedAttempts(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	s.RatePolicies = map[string]budgets.Policy{
		"messaging_start": {Limit: 1, Window: 3 * time.Minute},
		"messaging_send":  {Limit: 2, Window: 2 * time.Minute},
	}
	replica := New(s.DB, s.Cursor)
	replica.RatePolicies = s.RatePolicies
	a := fixtureUser(t, s, "rate-policy-actor", "adult")
	b := fixtureUser(t, s, "rate-policy-peer", "adult")
	c := fixtureUser(t, s, "rate-policy-other", "adult")
	direct, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if reused, err := replica.Start(ctx, a, "direct", []string{b.Username}, ""); err != nil || reused != direct {
		t.Fatalf("idempotent reuse consumed rate budget: id=%d err=%v", reused, err)
	}
	if _, err := replica.Start(ctx, a, "direct", []string{c.Username}, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("custom start limit ignored: %v", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE messaging_go_ratebudget SET window_start=now()-interval '90 seconds' WHERE user_id=$1 AND action='messaging_start'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.Start(ctx, a, "direct", []string{c.Username}, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("start policy used default minute window: %v", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE messaging_go_ratebudget SET window_start=now()-interval '3 minutes' WHERE user_id=$1 AND action='messaging_start'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.Start(ctx, a, "direct", []string{c.Username}, ""); err != nil {
		t.Fatalf("custom start window failed to expire: %v", err)
	}
	// The invited peer is not an active recipient yet. This fails after the
	// separately committed send admission and must still consume a token.
	if _, err := s.Post(ctx, a, direct, packet(a, b)); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("invalid recipient set was accepted: %v", err)
	}
	if _, err := replica.Post(ctx, a, direct, packet(a)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Post(ctx, a, direct, packet(a)); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("failed recipient attempt refunded custom send budget: %v", err)
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count FROM messaging_go_ratebudget WHERE user_id=$1 AND action='messaging_send'`, a.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("send denial changed bounded count: count=%d err=%v", count, err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE messaging_go_ratebudget SET count=2147483647,window_start=now()-interval '90 seconds' WHERE user_id=$1 AND action='messaging_send'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.Post(ctx, a, direct, packet(a)); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("send policy overflowed count or used default minute window: %v", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE messaging_go_ratebudget SET window_start=now()-interval '2 minutes' WHERE user_id=$1 AND action='messaging_send'`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.Post(ctx, a, direct, packet(a)); err != nil {
		t.Fatalf("custom send window did not reopen: %v", err)
	}
}
