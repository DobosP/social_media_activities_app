package social

import (
	"context"
	"errors"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRetirementProfileLiveJoinContextGuardianSeatAndConnectionReplay(t *testing.T) {
	s := profileAuthorityStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-profile-owner", "adult")
	requester := fixtureUser(t, s, "retirement-profile-requester", "adult")
	guardian := fixtureUser(t, s, "retirement-profile-guardian", "adult")
	outsider := fixtureUser(t, s, "retirement-profile-outsider", "adult")
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET display_name='' WHERE id=$1`, requester.ID); err != nil {
		t.Fatal(err)
	}
	card, err := s.Profile(ctx, owner, requester.PublicID)
	if err != nil || card["tier"] != "stranger" || card["display"] != "A member" || len(card) != 5 {
		t.Fatal("stranger blank display leaked handle or exceeded minimal cap")
	}
	activity := fixtureActivity(t, s, owner, nil)
	retirementSeat(t, s, activity, requester.ID, "member")
	if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET state='requested' WHERE activity_id=$1 AND user_id=$2`, activity, requester.ID); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct{ viewer, target Actor }{{owner, requester}, {requester, owner}} {
		card, err := s.Profile(ctx, pair.viewer, pair.target.PublicID)
		if err != nil || card["tier"] != "shared" || card["shared"].(map[string]any)["join_request"] != true || card["can_connect"] != false {
			t.Fatal("pending organizer/requester context did not grant reciprocal reviewed shape", err)
		}
	}
	retirementSeat(t, s, activity, guardian.ID, "guardian")
	card, err = s.Profile(ctx, owner, guardian.PublicID)
	if err != nil || card["tier"] != "stranger" {
		t.Fatal("supervisory seat created peer/shared profile context", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET state='member' WHERE activity_id=$1 AND user_id=$2`, activity, requester.ID); err != nil {
		t.Fatal(err)
	}
	card, err = s.Profile(ctx, owner, requester.PublicID)
	if err != nil || card["tier"] != "shared" || card["can_connect"] != true {
		t.Fatal("live peer activity did not grant shared tier and connection eligibility")
	}
	connection, err := s.RequestConnection(ctx, owner, requester.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.RequestConnection(ctx, owner, requester.PublicID); err != nil || again != connection {
		t.Fatal("pending request replay changed identity", err)
	}
	var notices int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='connection_request'`, requester.ID).Scan(&notices); err != nil || notices != 1 {
		t.Fatal("pending request replay renotified addressee")
	}
	if err := s.RespondConnection(ctx, outsider, connection, "accept"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("outsider answered another pair's request", err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, owner.ID, requester.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RespondConnection(ctx, requester, connection, "accept"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("accept bypassed block created after request", err)
	}
	if _, err := s.DB.Exec(ctx, `DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, owner.ID, requester.ID); err != nil {
		t.Fatal(err)
	}
	if reciprocal, err := s.RequestConnection(ctx, requester, owner.PublicID); err != nil || reciprocal != connection {
		t.Fatal("reciprocal request did not reuse and accept pending pair", err)
	}
	card, err = s.Profile(ctx, owner, requester.PublicID)
	if err != nil || card["tier"] != "connected" || card["can_message"] != true || card["can_connect"] != false {
		t.Fatal("accepted relationship did not produce connected tier")
	}
}

func TestRetirementProfileSharedTitlesOverflowCountsOnlyLiveIntersections(t *testing.T) {
	s := profileAuthorityStore(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "retirement-overflow-a", "adult")
	b := fixtureUser(t, s, "retirement-overflow-b", "adult")
	for i := 0; i < 5; i++ {
		activity := fixtureActivity(t, s, a, nil)
		retirementSeat(t, s, activity, b.ID, "member")
	}
	card, err := s.Profile(ctx, a, b.PublicID)
	if err != nil {
		t.Fatal(err)
	}
	shared := card["shared"].(map[string]any)
	if card["tier"] != "shared" || len(shared["activities"].([]string)) != 3 || shared["activity_count"] != 5 || shared["activity_overflow"] != 2 {
		t.Fatal("shared context overflow was not derived from bounded live intersections")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET state='removed' WHERE user_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	card, err = s.Profile(ctx, a, b.PublicID)
	if err != nil || card["tier"] != "stranger" || len(card) != 5 {
		t.Fatal("departed peer retained relationship history in profile")
	}
}
