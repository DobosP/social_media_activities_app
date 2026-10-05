package safety

import (
	"context"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestCasePort2BlockSymmetricReversibleUniqueAndSelfRefusal(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	a := user(t, s, "case2-blocker", false)
	b := user(t, s, "case2-blocked", false)
	body := fmt.Sprintf(`{"user_id":%d}`, b.ID)
	for i := 0; i < 2; i++ {
		if out := request(s, a, "POST", "/api/safety/blocks/", body); out.Code != 204 {
			t.Fatal("block handler failed", out.Code)
		}
	}
	var rows, audits int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2),(SELECT count(*) FROM safety_auditlog WHERE actor_id=$1 AND event='user.blocked')`, a.ID, b.ID).Scan(&rows, &audits); err != nil || rows != 1 || audits != 1 {
		t.Fatal("repeated block duplicated row or audit", err)
	}
	for _, pair := range [][2]int64{{a.ID, b.ID}, {b.ID, a.ID}} {
		if blocked, err := platform.Blocked(ctx, s.DB, pair[0], pair[1]); err != nil || !blocked {
			t.Fatal("block was not symmetric", err)
		}
	}
	if out := request(s, a, "DELETE", "/api/safety/blocks/", body); out.Code != 204 {
		t.Fatal("unblock handler failed", out.Code)
	}
	if blocked, err := platform.Blocked(ctx, s.DB, a.ID, b.ID); err != nil || blocked {
		t.Fatal("unblock did not reverse suppression", err)
	}
	if out := request(s, a, "POST", "/api/safety/blocks/", fmt.Sprintf(`{"user_id":%d}`, a.ID)); out.Code != 400 {
		t.Fatal("self block was not refused", out.Code)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_block WHERE blocker_id=$1 AND blocked_id=$1`, a.ID).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("self block persisted", err)
	}
}
