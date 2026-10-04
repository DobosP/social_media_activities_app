package social

import (
	"context"
	"errors"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func retirementSeat(t *testing.T, s *Service, activity, user int64, role string) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO social_membership(activity_id,user_id,role,state,created_at,updated_at,decided_at,attendance_intent,transit_status,brings_support_person) VALUES($1,$2,$3,'member',now(),now(),now(),'unknown','none',false)`, activity, user, role); err != nil {
		t.Fatal(err)
	}
}

func TestRetirementOrganizerGrantReplayTransferAndOwnershipWalls(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-owner", "adult")
	peer := fixtureUser(t, s, "retirement-co-organizer", "adult")
	other := fixtureUser(t, s, "retirement-outsider", "adult")
	child := fixtureUser(t, s, "retirement-minor-target", "child")
	activity := fixtureActivity(t, s, owner, nil)
	retirementSeat(t, s, activity, peer.ID, "member")
	if _, err := s.ChangeOrganizer(ctx, owner, activity, peer.ID, "grant_organizer"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeOrganizer(ctx, owner, activity, peer.ID, "grant_organizer"); err != nil {
		t.Fatal(err)
	}
	var notices, grants int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='organizer_role'),(SELECT count(*) FROM safety_auditlog WHERE event='activity.co_organizer_granted')`, peer.ID).Scan(&notices, &grants); err != nil || notices != 1 || grants != 1 {
		t.Fatal("replayed grant was not a strict audited/notification no-op")
	}
	for _, scenario := range []struct {
		name   string
		actor  Actor
		target int64
		action string
	}{
		{"co-organizer-cannot-grant", peer, other.ID, "grant_organizer"},
		{"co-organizer-cannot-transfer", peer, other.ID, "transfer"},
		{"outsider-cannot-grant", other, peer.ID, "grant_organizer"},
		{"minor-target-cannot-receive-role", owner, child.ID, "grant_organizer"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := s.ChangeOrganizer(ctx, scenario.actor, activity, scenario.target, scenario.action); !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("organizer authority wall failed", err)
			}
		})
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET age_band='16_17',cohort='teen' WHERE id=$1`, peer.ID); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"grant_organizer", "transfer"} {
		if _, err := s.ChangeOrganizer(ctx, owner, activity, peer.ID, action); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("existing adult peer downgraded to minor retained organizer eligibility", err)
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET age_band='adult',cohort='adult' WHERE id=$1`, peer.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeOrganizer(ctx, owner, activity, peer.ID, "revoke_organizer"); err != nil {
		t.Fatal(err)
	}
	var role string
	if err := s.DB.QueryRow(ctx, `SELECT role FROM social_membership WHERE activity_id=$1 AND user_id=$2`, activity, peer.ID).Scan(&role); err != nil || role != "member" {
		t.Fatal("revocation did not restore ordinary peer role")
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND kind='organizer_role'`, peer.ID).Scan(&notices); err != nil || notices != 2 {
		t.Fatal("organizer revocation omitted its own notice")
	}
	if _, err := s.ChangeOrganizer(ctx, owner, activity, peer.ID, "revoke_organizer"); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("revoke of ordinary member accepted", err)
	}
	if _, err := s.ChangeOrganizer(ctx, owner, activity, peer.ID, "transfer"); err != nil {
		t.Fatal(err)
	}
	var owners int
	var ownerID int64
	if err := s.DB.QueryRow(ctx, `SELECT owner_id,(SELECT count(*) FROM social_membership WHERE activity_id=$1 AND state='member' AND role='owner') FROM social_activity WHERE id=$1`, activity).Scan(&ownerID, &owners); err != nil || ownerID != peer.ID || owners != 1 {
		t.Fatal("ownership transfer did not preserve exactly one owner")
	}
	if _, err := s.ChangeOrganizer(ctx, owner, activity, other.ID, "transfer"); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("stale former owner transferred again", err)
	}
}

func TestRetirementQuorumWobbleDoesNotReplayNotice(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	owner := fixtureUser(t, s, "retirement-quorum-owner", "adult")
	peer := fixtureUser(t, s, "retirement-quorum-peer", "adult")
	late := fixtureUser(t, s, "retirement-quorum-late", "adult")
	activity := fixtureActivity(t, s, owner, nil)
	retirementSeat(t, s, activity, peer.ID, "member")
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET min_to_go=2 WHERE id=$1`, activity); err != nil {
		t.Fatal(err)
	}
	for _, user := range []Actor{owner, peer} {
		if err := s.RSVP(ctx, user, activity, "going"); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		var n int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE kind='meetup_confirmed'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count() != 2 {
		t.Fatal("first threshold crossing did not notify current peers once")
	}
	var latched bool
	if err := s.DB.QueryRow(ctx, `SELECT go_confirmed_at IS NOT NULL FROM social_activity WHERE id=$1`, activity).Scan(&latched); err != nil || !latched {
		t.Fatal("first quorum crossing did not latch confirmation")
	}
	if err := s.RSVP(ctx, peer, activity, "not_going"); err != nil {
		t.Fatal(err)
	}
	attendance, err := s.Attendance(ctx, owner, activity)
	if err != nil {
		t.Fatal(err)
	}
	state := decodeObject(t, attendance)
	if state["met_minimum"] != false || state["remaining_needed"] != float64(1) {
		t.Fatal("quorum chip did not follow current going count")
	}
	if err := s.RSVP(ctx, peer, activity, "going"); err != nil {
		t.Fatal(err)
	}
	retirementSeat(t, s, activity, late.ID, "member")
	if err := s.RSVP(ctx, late, activity, "going"); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Fatal("re-crossing or late join replayed historical quorum notice")
	}
	if err := s.CancelActivity(ctx, owner, activity, "Synthetic cancelled meetup"); err != nil {
		t.Fatal(err)
	}
	attendance, err = s.Attendance(ctx, owner, activity)
	if err != nil {
		t.Fatal(err)
	}
	if decodeObject(t, attendance)["met_minimum"] != nil || count() != 2 {
		t.Fatal("cancelled activity retained misleading quorum chip or confirmation")
	}
}
