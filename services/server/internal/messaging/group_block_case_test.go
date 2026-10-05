package messaging

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
)

func groupBlockGroup(t *testing.T, s *Service, admin platform.Actor, members ...platform.Actor) int64 {
	t.Helper()
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Username)
	}
	id, e := s.Start(context.Background(), admin, "group", names, "Synthetic block group")
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range members {
		if e = s.Transition(context.Background(), m, id, "accept"); e != nil {
			t.Fatal(e)
		}
	}
	return id
}

func groupBlock(t *testing.T, s *Service, blocker, blocked int64) {
	t.Helper()
	if _, e := s.DB.Exec(context.Background(), `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, blocker, blocked); e != nil {
		t.Fatal(e)
	}
}

func groupBlockState(t *testing.T, s *Service, conversation, user int64) string {
	t.Helper()
	var state string
	if e := s.DB.QueryRow(context.Background(), `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, conversation, user).Scan(&state); e != nil {
		t.Fatal(e)
	}
	return state
}

func groupBlockModerator(t *testing.T, s *Service, name string) (platform.Actor, *safety.Service) {
	t.Helper()
	mod := fixtureUser(t, s, name, "adult")
	mod.Role = "moderator"
	if _, e := s.DB.Exec(context.Background(), `UPDATE accounts_user SET role='moderator' WHERE id=$1`, mod.ID); e != nil {
		t.Fatal(e)
	}
	return mod, safety.New(s.DB, safety.Config{Messaging: s})
}

// A block inside a group never locks out the group: reading, sending, keys and
// history keep working for the blocker, the blocked member and bystanders.
func TestPostgresGroupBlockLeavesGroupWorking(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "group-block-admin", "adult")
	b := fixtureUser(t, s, "group-block-bystander", "adult")
	c := fixtureUser(t, s, "group-block-blocker", "adult")
	for _, u := range []platform.Actor{a, b, c} {
		if _, e := s.RegisterKey(ctx, u, jwk(u.Username), "", nil); e != nil {
			t.Fatal(e)
		}
	}
	group := groupBlockGroup(t, s, a, b, c)
	groupBlock(t, s, c.ID, a.ID)
	for _, viewer := range []platform.Actor{a, b, c} {
		if ok, e := s.CanView(ctx, s.DB, viewer, group); e != nil || !ok {
			t.Fatal("group block revoked read access", viewer.Username, ok, e)
		}
	}
	for _, sender := range []platform.Actor{a, c} {
		if _, e := s.Post(ctx, sender, group, packet(a, b, c)); e != nil {
			t.Fatal("group block stopped sending", sender.Username, e)
		}
	}
	if keys, e := s.ParticipantKeys(ctx, a, group); e != nil || len(keys) != 3 {
		t.Fatal("group block hid the key roster", len(keys), e)
	}
	if w := call(s, b, "GET", fmt.Sprintf("/api/messaging/conversations/%d/messages/", group), nil); w.Code != 200 {
		t.Fatal("bystander history refused under a group block", w.Code)
	}
}

// Direct chats keep the both-ways block veto; only an active peer's block counts.
func TestPostgresDirectBlockVetoesOnlyWhileBlockerIsActive(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "direct-block-a", "adult")
	b := fixtureUser(t, s, "direct-block-b", "adult")
	for _, u := range []platform.Actor{a, b} {
		if _, e := s.RegisterKey(ctx, u, jwk(u.Username), "", nil); e != nil {
			t.Fatal(e)
		}
	}
	direct := casePort3ActiveDirect(t, s, a, b)
	groupBlock(t, s, b.ID, a.ID)
	for _, viewer := range []platform.Actor{a, b} {
		if ok, e := s.CanView(ctx, s.DB, viewer, direct); e != nil || ok {
			t.Fatal("direct block no longer vetoes", viewer.Username, ok, e)
		}
	}
	if _, e := s.Post(ctx, a, direct, packet(a, b)); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("blocked direct sender", e)
	}
	if _, e := s.ParticipantKeys(ctx, a, direct); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("blocked direct key roster", e)
	}
	if _, e := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.CanView(ctx, s.DB, a, direct); e != nil || !ok {
		t.Fatal("deactivated blocker still vetoes the remaining peer", ok, e)
	}
	if keys, e := s.ParticipantKeys(ctx, a, direct); e != nil || len(keys) != 1 {
		t.Fatal("key roster kept a deactivated peer", len(keys), e)
	}
	if _, e := s.Post(ctx, a, direct, packet(a)); e != nil {
		t.Fatal("recipient set kept a deactivated peer", e)
	}
}

// Removing a member never depends on blocks; inviting one still does.
func TestPostgresAdminRemovesMemberRegardlessOfBlocks(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	for _, adminBlocks := range []bool{false, true} {
		t.Run(fmt.Sprintf("admin_blocks_%v", adminBlocks), func(t *testing.T) {
			a := fixtureUser(t, s, fmt.Sprintf("remove-block-admin-%v", adminBlocks), "adult")
			b := fixtureUser(t, s, fmt.Sprintf("remove-block-bystander-%v", adminBlocks), "adult")
			c := fixtureUser(t, s, fmt.Sprintf("remove-block-member-%v", adminBlocks), "adult")
			group := groupBlockGroup(t, s, a, b, c)
			if adminBlocks {
				groupBlock(t, s, a.ID, c.ID)
			} else {
				groupBlock(t, s, c.ID, a.ID)
			}
			if e := s.RemoveParticipant(ctx, a, group, c.Username); e != nil {
				t.Fatal("admin removal depended on a block", e)
			}
			if state := groupBlockState(t, s, group, c.ID); state != "removed" {
				t.Fatal("removed member row", state)
			}
			if _, e := s.Post(ctx, a, group, packet(a, b)); e != nil {
				t.Fatal("send after removal", e)
			}
			d := fixtureUser(t, s, fmt.Sprintf("remove-block-invitee-%v", adminBlocks), "adult")
			groupBlock(t, s, d.ID, a.ID)
			if e := s.AddParticipant(ctx, a, group, d.Username); !errors.Is(e, platform.ErrForbidden) {
				t.Fatal("block did not refuse a group invitation", e)
			}
		})
	}
}

// A sanction removes the account from every conversation in the moderation
// transaction, and is refused outright when messaging revocation is not wired.
func TestPostgresModerationSuspensionEvictsConversations(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "suspend-evict-admin", "adult")
	b := fixtureUser(t, s, "suspend-evict-bystander", "adult")
	c := fixtureUser(t, s, "suspend-evict-subject", "adult")
	group := groupBlockGroup(t, s, a, b, c)
	direct := casePort3ActiveDirect(t, s, a, c)
	mod, safe := groupBlockModerator(t, s, "suspend-evict-moderator")
	target, e := safe.ResolveTarget(ctx, s.DB, "accounts", "user", c.ID)
	if e != nil {
		t.Fatal(e)
	}
	input := safety.ActionInput{Decision: "suspend", Reason: "harassment", SuspendDays: 1}
	if _, e = safety.New(s.DB, safety.Config{}).TakeAction(ctx, mod, target, input, 0); e == nil {
		t.Fatal("account sanction committed without messaging revocation")
	}
	var active bool
	var actions int
	if e = s.DB.QueryRow(ctx, `SELECT is_active,(SELECT count(*) FROM safety_moderationaction) FROM accounts_user WHERE id=$1`, c.ID).Scan(&active, &actions); e != nil || !active || actions != 0 {
		t.Fatal("refused sanction left partial state", active, actions, e)
	}
	if _, e = safe.TakeAction(ctx, mod, target, input, 0); e != nil {
		t.Fatal(e)
	}
	for _, id := range []int64{group, direct} {
		if state := groupBlockState(t, s, id, c.ID); state != "removed" {
			t.Fatal("suspended account kept a conversation row", id, state)
		}
	}
	var audits int
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='messaging.participation_revoked' AND actor_ref=$1 AND data->>'reason'='moderation_suspend' AND data->>'count'='2'`, c.ID).Scan(&audits); e != nil || audits != 1 {
		t.Fatal("moderation eviction audit", audits, e)
	}
	if _, e = s.Post(ctx, a, group, packet(a, b)); e != nil {
		t.Fatal("group send after a member's suspension", e)
	}
	if ok, e := s.CanView(ctx, s.DB, c, group); e != nil || ok {
		t.Fatal("suspended account retained group read", ok, e)
	}
}

// Lifting a sanction never restores rows; Start re-invites the direct peer, who
// must accept, and charges/audits only a real re-invitation.
func TestPostgresDirectReinviteAfterLiftedSuspension(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "reinvite-initiator", "adult")
	c := fixtureUser(t, s, "reinvite-subject", "adult")
	direct := casePort3ActiveDirect(t, s, a, c)
	mod, safe := groupBlockModerator(t, s, "reinvite-moderator")
	target, e := safe.ResolveTarget(ctx, s.DB, "accounts", "user", c.ID)
	if e != nil {
		t.Fatal(e)
	}
	action, e := safe.TakeAction(ctx, mod, target, safety.ActionInput{Decision: "suspend", Reason: "spam", SuspendDays: 1}, 0)
	if e != nil {
		t.Fatal(e)
	}
	if state := groupBlockState(t, s, direct, c.ID); state != "removed" {
		t.Fatal("suspension kept the direct row", state)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE safety_moderationaction SET expires_at=now()-interval '1 minute' WHERE id=$1`, action); e != nil {
		t.Fatal(e)
	}
	if lifted, e := safe.LiftSuspensions(ctx); e != nil || lifted != 1 {
		t.Fatal("expiry lift", lifted, e)
	}
	if state := groupBlockState(t, s, direct, c.ID); state != "removed" {
		t.Fatal("lift silently restored a conversation row", state)
	}
	// The removed party cannot re-enter on their own while the peer is active.
	if reused, e := s.Start(ctx, c, "direct", []string{a.Username}, ""); e != nil || reused != direct {
		t.Fatal("removed party's direct reuse", reused, e)
	}
	if state := groupBlockState(t, s, direct, c.ID); state != "removed" {
		t.Fatal("removed party reactivated their own direct seat without consent", state)
	}
	budget := func() int {
		t.Helper()
		var count int
		if e := s.DB.QueryRow(ctx, `SELECT COALESCE((SELECT count FROM messaging_go_ratebudget WHERE user_id=$1 AND action='messaging_start'),0)`, a.ID).Scan(&count); e != nil {
			t.Fatal(e)
		}
		return count
	}
	audits := func() int {
		t.Helper()
		var count int
		if e := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE event='messaging.direct_reinvited' AND actor_ref=$1`, a.ID).Scan(&count); e != nil {
			t.Fatal(e)
		}
		return count
	}
	before := budget()
	reused, e := s.Start(ctx, a, "direct", []string{c.Username}, "")
	if e != nil || reused != direct {
		t.Fatal("direct reuse", reused, e)
	}
	var state string
	var inviter int64
	if e = s.DB.QueryRow(ctx, `SELECT state,COALESCE(invited_by_id,0) FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, direct, c.ID).Scan(&state, &inviter); e != nil || state != "invited" || inviter != a.ID {
		t.Fatal("peer was not re-invited", state, inviter, e)
	}
	if mine := groupBlockState(t, s, direct, a.ID); mine != "active" {
		t.Fatal("initiator row", mine)
	}
	if got := budget(); got != before+1 || audits() != 1 {
		t.Fatal("re-invite was not charged and audited once", before, got)
	}
	if again, e := s.Start(ctx, a, "direct", []string{c.Username}, ""); e != nil || again != direct {
		t.Fatal("second direct reuse", again, e)
	}
	if got := budget(); got != before+1 || audits() != 1 {
		t.Fatal("plain reuse was charged or audited", before, got)
	}
	if ok, e := s.CanView(ctx, s.DB, c, direct); e != nil || ok {
		t.Fatal("re-invited peer read before accepting", ok, e)
	}
	if e = s.Transition(ctx, c, direct, "accept"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Post(ctx, a, direct, packet(a, c)); e != nil {
		t.Fatal("send after accepted re-invite", e)
	}
}

// A member whose consent lapsed loses their own access but cannot stop others.
func TestPostgresLapsedPeerConsentDoesNotStopGroupSend(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "lapsed-child-admin", "child")
	b := fixtureUser(t, s, "lapsed-child-member", "child")
	c := fixtureUser(t, s, "lapsed-child-bystander", "child")
	group := groupBlockGroup(t, s, a, b, c)
	if _, e := s.DB.Exec(ctx, `UPDATE accounts_parentalconsent SET expires_at=now()-interval '1 minute' WHERE minor_id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Post(ctx, a, group, packet(a, b, c)); e != nil {
		t.Fatal("one lapsed peer stopped the group", e)
	}
	if ok, e := s.CanView(ctx, s.DB, b, group); e != nil || ok {
		t.Fatal("lapsed member retained read", ok, e)
	}
	if _, e := s.Post(ctx, b, group, packet(a, b, c)); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("lapsed member sent", e)
	}
}

// An observer whose own assurance expired cannot stop the children's chat.
func TestPostgresExpiredGuardianAssuranceDoesNotStopChildren(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "expired-guardian-ward", "child")
	b := fixtureUser(t, s, "expired-guardian-peer", "child")
	g := fixtureUser(t, s, "expired-guardian", "adult")
	if _, e := s.RegisterKey(ctx, g, jwk(g.Username), "", nil); e != nil {
		t.Fatal(e)
	}
	direct := casePort3ActiveDirect(t, s, a, b)
	casePort3GuardianLink(t, s, g, a)
	if e := s.AddGuardian(ctx, g, direct); e != nil {
		t.Fatal(e)
	}
	if _, e := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'synthetic','fixture','adult',clock_timestamp(),now()-interval '1 minute','{}','')`, g.ID); e != nil {
		t.Fatal(e)
	}
	for _, sender := range []platform.Actor{a, b} {
		if _, e := s.Post(ctx, sender, direct, packet(a, b, g)); e != nil {
			t.Fatal("expired observer stopped the children", sender.Username, e)
		}
	}
	if ok, e := s.CanView(ctx, s.DB, g, direct); e != nil || ok {
		t.Fatal("expired observer retained read", ok, e)
	}
}
