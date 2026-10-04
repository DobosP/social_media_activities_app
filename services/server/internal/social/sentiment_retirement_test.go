package social

import (
	"context"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
)

func TestRetirementSentimentToggleAndSharedWriteVetoMatrix(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-sentiment-owner", "adult")
	peer := fixtureUser(t, s, "retirement-sentiment-peer", "adult")
	outsider := fixtureUser(t, s, "retirement-sentiment-outsider", "adult")
	guardian := fixtureUser(t, s, "retirement-sentiment-guardian", "adult")
	activity := fixtureActivity(t, s, owner, nil)
	retirementSeat(t, s, activity, peer.ID, "member")
	retirementSeat(t, s, activity, guardian.ID, "guardian")
	post, err := s.WritePost(ctx, owner, "activity", activity, PostInput{Body: "Synthetic sentiment target"}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"dissent", "concern"} {
		if added, err := s.ToggleSentiment(ctx, peer, post, kind, ""); err != nil || !added {
			t.Fatal("eligible sentiment did not add", kind, err)
		}
		if added, err := s.ToggleSentiment(ctx, peer, post, kind, ""); err != nil || added {
			t.Fatal("second sentiment toggle did not remove", kind, err)
		}
	}
	for _, scenario := range []struct {
		name                  string
		actor                 Actor
		hidden, frozen, block bool
	}{
		{name: "non-member", actor: outsider},
		{name: "guardian", actor: guardian},
		{name: "hidden-post", actor: peer, hidden: true},
		{name: "frozen-thread", actor: peer, frozen: true},
		{name: "blocked-owner", actor: peer, block: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := s.DB.Exec(ctx, `UPDATE social_post SET is_hidden=$2 WHERE id=$1`, post, scenario.hidden); err != nil {
				t.Fatal(err)
			}
			status := "open"
			if scenario.frozen {
				status = "cancelled"
			}
			if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET status=$2 WHERE id=$1`, activity, status); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(ctx, `DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, owner.ID, peer.ID); err != nil {
				t.Fatal(err)
			}
			if scenario.block {
				if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, peer.ID); err != nil {
					t.Fatal(err)
				}
			}
			for _, kind := range []string{"reaction", "dissent", "concern"} {
				if _, err := s.ToggleSentiment(ctx, scenario.actor, post, kind, "helped_me"); err == nil {
					t.Fatal("shared write veto failed", kind)
				}
			}
		})
	}
	var rows int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM social_postreaction WHERE post_id=$1)+(SELECT count(*) FROM social_postdissent WHERE post_id=$1)+(SELECT count(*) FROM social_postconcern WHERE post_id=$1)`, post).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("refused sentiment wrote private rows")
	}
}

func TestRetirementChildSentimentFloorAndCurrentConsent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-child-sentiment-owner", "child")
	peer := fixtureUser(t, s, "retirement-child-sentiment-peer", "child")
	activity := fixtureActivity(t, s, owner, nil)
	retirementSeat(t, s, activity, peer.ID, "member")
	post, err := s.WritePost(ctx, owner, "activity", activity, PostInput{Body: "Synthetic child thread"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if added, err := s.ToggleSentiment(ctx, peer, post, "reaction", "helped_me"); err != nil || !added {
		t.Fatal("child lost permitted appreciation reaction", err)
	}
	for _, kind := range []string{"dissent", "concern"} {
		if _, err := s.ToggleSentiment(ctx, peer, post, kind, ""); err == nil {
			t.Fatal("child conduct/dissent rung became available")
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked' WHERE minor_id=$1`, peer.ID); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"reaction", "dissent", "concern"} {
		if _, err := s.ToggleSentiment(ctx, peer, post, kind, "helped_me"); err == nil {
			t.Fatal("revoked current consent retained write authority", kind)
		}
	}
}

func TestRetirementSentimentKindsShareOneAdmissionBudget(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-sentiment-budget-owner", "adult")
	peer := fixtureUser(t, s, "retirement-sentiment-budget-peer", "adult")
	activity := fixtureActivity(t, s, owner, nil)
	retirementSeat(t, s, activity, peer.ID, "member")
	post, err := s.WritePost(ctx, owner, "activity", activity, PostInput{Body: "Synthetic budget target"}, false)
	if err != nil {
		t.Fatal(err)
	}
	s.RatePolicies = map[string]budgets.Policy{"thread_react": {Limit: 1, Window: time.Minute}}
	if _, err := s.ToggleSentiment(ctx, peer, post, "reaction", "helped_me"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"dissent", "concern"} {
		if _, err := s.ToggleSentiment(ctx, peer, post, kind, ""); err == nil {
			t.Fatal("new sentiment kind escaped existing reaction budget", kind)
		}
	}
}
