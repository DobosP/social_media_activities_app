package social

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
)

func TestRootAuditCorrectionBlankDisplayKeepsWholeStrangerHandlePrivate(t *testing.T) {
	s, _ := testStore(t)
	s.Avatar = accounts.Avatar
	ctx := context.Background()
	a := fixtureUser(t, s, "root-audit-neutral-viewer", "adult")
	b := fixtureUser(t, s, "root-audit-private-target-handle", "adult")
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET display_name='' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	card, err := s.Profile(ctx, a, b.PublicID)
	if err != nil || card["tier"] != "stranger" || card["display"] != "A member" {
		t.Fatal("blank stranger display was not neutral", err)
	}
	raw, err := json.Marshal(card)
	if err != nil || strings.Contains(string(raw), b.Username) {
		t.Fatal("whole stranger payload leaked target handle", err)
	}
	activity := fixtureActivity(t, s, a, nil)
	retirementSeat(t, s, activity, b.ID, "member")
	card, err = s.Profile(ctx, a, b.PublicID)
	if err != nil || card["tier"] != "shared" || card["display"] != b.Username {
		t.Fatal("shared peer context did not permit handle fallback", err)
	}
}
