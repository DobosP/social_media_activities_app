package messaging

import (
	"context"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRetirementGroupInvitationDeclineAndAdministratorWalls(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	admin := fixtureUser(t, s, "retirement-message-admin", "adult")
	peer := fixtureUser(t, s, "retirement-message-peer", "adult")
	newPeer := fixtureUser(t, s, "retirement-message-invitee", "adult")
	minor := fixtureUser(t, s, "retirement-message-minor", "child")
	group, err := s.Start(ctx, admin, "group", []string{peer.Username}, "Synthetic group")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, peer, group, "decline"); err != nil {
		t.Fatal(err)
	}
	if visible, err := s.CanView(ctx, s.DB, peer, group); err != nil || visible {
		t.Fatal("declined invitation granted history access")
	}
	if err := s.AddParticipant(ctx, admin, group, peer.Username); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, peer, group, "accept"); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name   string
		actor  platform.Actor
		target string
	}{
		{"ordinary-member-cannot-add", peer, newPeer.Username},
		{"cross-cohort-target-refused", admin, minor.Username},
		{"self-add-refused", admin, admin.Username},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if err := s.AddParticipant(ctx, scenario.actor, group, scenario.target); err == nil {
				t.Fatal("participant authority wall failed")
			}
		})
	}
	if err := s.AddParticipant(ctx, admin, group, newPeer.Username); err != nil {
		t.Fatal(err)
	}
	if err := s.AddParticipant(ctx, admin, group, newPeer.Username); err != nil {
		t.Fatal(err)
	}
	var additions int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='messaging.participant_added' AND target_ref='accounts.user:'||$1::bigint::text`, newPeer.ID).Scan(&additions); err != nil || additions != 1 {
		t.Fatalf("already-invited transition audit count=%d error=%v", additions, err)
	}
	if err := s.RemoveParticipant(ctx, peer, group, newPeer.Username); err == nil {
		t.Fatal("ordinary member removed an invitee")
	}
	if err := s.Transition(ctx, newPeer, group, "accept"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveParticipant(ctx, admin, group, newPeer.Username); err != nil {
		t.Fatal(err)
	}
	if visible, err := s.CanView(ctx, s.DB, newPeer, group); err != nil || visible {
		t.Fatal("removed active participant retained history access")
	}
	if err := s.Transition(ctx, newPeer, group, "accept"); err == nil {
		t.Fatal("removed invitation was accepted")
	}
}

func TestRetirementVerificationRefusesWrongMissingAndCrossCohortKeys(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	viewer := fixtureUser(t, s, "retirement-key-viewer", "adult")
	target := fixtureUser(t, s, "retirement-key-target", "adult")
	minor := fixtureUser(t, s, "retirement-key-minor", "child")
	if _, err := s.VerifyKey(ctx, viewer, target.Username, strings.Repeat("a", 32)); err == nil {
		t.Fatal("missing key was marked verified")
	}
	if _, err := s.RegisterKey(ctx, target, jwk("retirement-target"), "", nil); err != nil {
		t.Fatal(err)
	}
	for _, fingerprint := range []string{"short", strings.Repeat("a", 32)} {
		if _, err := s.VerifyKey(ctx, viewer, target.Username, fingerprint); err == nil {
			t.Fatal("incorrect key fingerprint was recorded")
		}
	}
	key, err := s.ContactKey(ctx, viewer, target.Username)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyKey(ctx, minor, target.Username, key["fingerprint"].(string)); err == nil {
		t.Fatal("minor verified another cohort's key")
	}
	var records int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_keyverification`).Scan(&records); err != nil || records != 0 {
		t.Fatal("failed key proofs left verification state")
	}
}

func TestRetirementUntimedCiphertextRetentionBackstopAndWrappedKeyCleanup(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "retirement-retention-a", "adult")
	b := fixtureUser(t, s, "retirement-retention-b", "adult")
	for _, user := range []platform.Actor{a, b} {
		if _, err := s.RegisterKey(ctx, user, jwk(user.Username), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	direct, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, direct, "accept"); err != nil {
		t.Fatal(err)
	}
	message, err := s.Post(ctx, a, direct, packet(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE messaging_message SET created_at=now()-interval '31 days' WHERE id=$1`, message); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.PurgeExpired(ctx, 100); err != nil || removed != 0 {
		t.Fatal("untimed conversation purged with global backstop disabled")
	}
	s.RetentionDays = 30
	if removed, err := s.PurgeExpired(ctx, 100); err != nil || removed != 1 {
		t.Fatal("global retention backstop ignored untimed ciphertext")
	}
	var messages, keys int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM messaging_message WHERE id=$1),(SELECT count(*) FROM messaging_messagekey WHERE message_id=$1)`, message).Scan(&messages, &keys); err != nil || messages != 0 || keys != 0 {
		t.Fatal("retention removed ciphertext without its wrapped keys")
	}
}
