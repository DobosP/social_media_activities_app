package messaging

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func privacy6Fixture(t *testing.T) (*Service, *accounts.Service) {
	t.Helper()
	ctx := context.Background()
	db := testdb.New(t, *messagingTestDSN, nil)
	acc := accounts.New(db, nil, "synthetic-privacy6-public-binding", accounts.Config{})
	if err := acc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := EnsureSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	return New(db, platform.CursorCodec{Key: bytes.Repeat([]byte{6}, 32)}), acc
}

func TestPrivacyCasePort6UnknownConversationRecipientIsExact400(t *testing.T) {
	s, _ := privacy6Fixture(t)
	a := testdb.Actor(t, s.DB, "privacy6-unknown-recipient-owner", "adult")
	out := call(s, a, "POST", "/api/messaging/conversations/", map[string]any{"username": "ghost"})
	if out.Code != 400 {
		t.Fatalf("original unknownrecipient source contract expects400; actual%d", out.Code)
	}
	var conversations int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM messaging_conversation`).Scan(&conversations); err != nil || conversations != 0 {
		t.Fatal("unknown recipient created conversation", err)
	}
	// Resource reads keep their separate indistinguishable absence response.
	if out := call(s, a, "GET", "/api/messaging/keys/ghost/", nil); out.Code != 404 {
		t.Fatal("unknown key response changed", out.Code)
	}
}

func TestPrivacyCasePort6ExactCapOneRecipientRejectsBeforeBudget(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-cap1-owner", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-cap1-peer", "adult")
	if err := s.ConfigurePolicy(Policy{MaxCiphertextBytes: 65536, MaxGroupMembers: 1}); err != nil {
		t.Fatalf("source cap1 unsupported: %v", err)
	}
	id, err := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal("source direct start is independent of group cap", err)
	}
	if err := s.Transition(ctx, b, id, "accept"); err != nil {
		t.Fatal(err)
	}
	for _, who := range []platform.Actor{a, b} {
		if _, err := s.RegisterKey(ctx, who, jwk(who.Username), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if out := call(s, a, "GET", "/api/messaging/conversations/", nil); out.Code != 200 || len(casePort3Array(t, out.Body.Bytes())) != 1 {
		t.Fatal("direct cap1 metadata incorrectly applies group floor", out.Code)
	}
	if keys, err := s.ParticipantKeys(ctx, a, id); err != nil || len(keys) != 2 {
		t.Fatal("direct cap1 key roster incorrectly applies group floor", err)
	}
	s.RatePolicies = map[string]budgets.Policy{"messaging_send": {Limit: 1, Window: time.Minute}}
	if _, err := s.Post(ctx, a, id, packet(a, b)); err == nil {
		t.Fatal("two recipients bypassed exact group/send cap1")
	}
	var messages, keys, budgets int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM messaging_message),(SELECT count(*) FROM messaging_messagekey),(SELECT count(*) FROM messaging_go_ratebudget WHERE action='messaging_send')`).Scan(&messages, &keys, &budgets); err != nil || messages != 0 || keys != 0 || budgets != 0 {
		t.Fatal("oversized recipient work consumed send budget or stored rows", err)
	}
	if _, err := s.Start(ctx, a, "group", []string{b.Username}, "cap1 group"); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("group creation widened cap1", err)
	}
	if err := s.ConfigurePolicy(Policy{MaxCiphertextBytes: 65536, MaxGroupMembers: 256}); err != nil {
		t.Fatal(err)
	}
	message, err := s.Post(ctx, a, id, packet(a, b))
	if err != nil {
		t.Fatal("oversized rejection drained single send token", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_message WHERE id=$1`, message).Scan(&messages); err != nil || messages != 1 {
		t.Fatal("valid send after cap restoration not stored", err)
	}
	if _, err := s.Post(ctx, a, id, packet(a, b)); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("source single send limit did not enforce", err)
	}
}

func TestPrivacyCasePort6OwnConversationsExplicitPendingFilter(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-pending-owner", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-pending-invited", "adult")
	if _, err := s.Start(ctx, a, "direct", []string{b.Username}, ""); err != nil {
		t.Fatal(err)
	}
	owners, _, err := s.Conversations(ctx, a, "", 100, 0, false)
	if err != nil || len(owners) != 1 {
		t.Fatal("active owner list", err)
	}
	pending, _, err := s.Conversations(ctx, b, "", 100, 0, false)
	if err != nil || len(pending) != 1 {
		t.Fatal("default list omitted pending invite", err)
	}
	active, _, err := s.Conversations(ctx, b, "", 100, 0, false, false)
	if err != nil || len(active) != 0 {
		t.Fatal("explicit include_pending false exposed invite", err)
	}
	if _, _, err := s.Conversations(ctx, b, "", 100, 0, false, true, false); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("multiple pending options accepted", err)
	}
}

func TestPrivacyCasePort6ParticipantKeyFreshAuthorityMatrixAndTwoQueryGrowth(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-fresh-child-a", "child")
	b := testdb.Actor(t, s.DB, "privacy6-fresh-child-b", "child")
	g := testdb.Actor(t, s.DB, "privacy6-fresh-guardian", "adult")
	for _, who := range []platform.Actor{a, b, g} {
		if _, err := s.RegisterKey(ctx, who, jwk(who.Username), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	id := casePort3ActiveDirect(t, s, a, b)
	casePort3GuardianLink(t, s, g, a)
	if err := s.AddGuardian(ctx, g, id); err != nil {
		t.Fatal(err)
	}
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	read := func(t *testing.T, viewer platform.Actor, want int) int64 {
		t.Helper()
		trace.Reset()
		keys, err := s.ParticipantKeys(ctx, viewer, id)
		queries := trace.Count()
		if err != nil || len(keys) != want {
			t.Fatal("fresh authorized key roster", err)
		}
		return queries
	}
	small := read(t, a, 3)
	if small != 2 {
		t.Fatalf("CHILD source key roster uses%d queries, expected2<=4", small)
	}
	for i := 0; i < 10; i++ {
		guard := testdb.Actor(t, db, fmt.Sprintf("privacy6-growth-guardian-%d", i), "adult")
		if _, err := s.RegisterKey(ctx, guard, jwk(guard.Username), "", nil); err != nil {
			t.Fatal(err)
		}
		casePort3GuardianLink(t, s, guard, a)
		if err := s.AddGuardian(ctx, guard, id); err != nil {
			t.Fatal(err)
		}
	}
	large := read(t, a, 13)
	if large != 2 || large != small {
		t.Fatal("participant keys introduced per-member work", small, large)
	}
	t.Logf("source participant keys roster3->13 queries%d->%d originalceiling4", small, large)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	denied := func(t *testing.T, viewer platform.Actor) {
		t.Helper()
		keys, err := s.ParticipantKeys(ctx, viewer, id)
		if !errors.Is(err, platform.ErrForbidden) || len(keys) != 0 {
			t.Fatal("current authority withdrawal returned private keys", err)
		}
	}
	t.Run("forged_captured_adult_cannot_bypass_revoked_child_consent", func(t *testing.T) {
		exec(`UPDATE accounts_parentalconsent SET status='revoked' WHERE minor_id=$1`, a.ID)
		forged := a
		forged.Cohort = "adult"
		forged.AgeBand = "adult"
		forged.Role = "admin"
		forged.IsStaff = true
		forged.IsSuperuser = true
		denied(t, forged)
		exec(`UPDATE accounts_parentalconsent SET status='active' WHERE minor_id=$1`, a.ID)
	})
	t.Run("current_identity_withdrawn", func(t *testing.T) {
		exec(`UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, a.ID)
		denied(t, a)
		exec(`UPDATE accounts_user SET is_identity_verified=true WHERE id=$1`, a.ID)
	})
	t.Run("latest_proof_expiry_and_renewal", func(t *testing.T) {
		exec(`INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'synthetic','fixture','under_16',clock_timestamp(),now()-interval '1 minute','{}','')`, a.ID)
		denied(t, a)
		exec(`INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'synthetic','fixture','under_16',clock_timestamp(),now()+interval '1 year','{}','')`, a.ID)
		read(t, a, 13)
	})
	t.Run("current_cohort_replaces_captured_child", func(t *testing.T) {
		exec(`UPDATE accounts_user SET cohort='adult',age_band='adult' WHERE id=$1`, a.ID)
		denied(t, a)
		exec(`UPDATE accounts_user SET cohort='child',age_band='under_16' WHERE id=$1`, a.ID)
	})
	t.Run("deactivated_participant", func(t *testing.T) {
		exec(`UPDATE accounts_user SET is_active=false WHERE id=$1`, a.ID)
		denied(t, a)
		exec(`UPDATE accounts_user SET is_active=true WHERE id=$1`, a.ID)
	})
	t.Run("current_participant_role_guardian_cannot_use_forged_admin", func(t *testing.T) {
		exec(`UPDATE messaging_participant SET role='guardian' WHERE conversation_id=$1 AND user_id=$2`, id, a.ID)
		forged := a
		forged.Role = "admin"
		forged.Cohort = "adult"
		denied(t, forged)
		exec(`UPDATE messaging_participant SET role='admin' WHERE conversation_id=$1 AND user_id=$2`, id, a.ID)
	})
	t.Run("invited_participant", func(t *testing.T) {
		exec(`UPDATE messaging_participant SET state='invited' WHERE conversation_id=$1 AND user_id=$2`, id, a.ID)
		denied(t, a)
		exec(`UPDATE messaging_participant SET state='active' WHERE conversation_id=$1 AND user_id=$2`, id, a.ID)
	})
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprintf("mutual_block_%v", reverse), func(t *testing.T) {
			blocker, blocked := a.ID, b.ID
			if reverse {
				blocker, blocked = blocked, blocker
			}
			exec(`INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, blocker, blocked)
			denied(t, a)
			exec(`DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, blocker, blocked)
		})
	}
	t.Run("guardian_relation_and_block_delegation", func(t *testing.T) {
		read(t, g, 13)
		exec(`UPDATE accounts_guardianrelationship SET status='revoked' WHERE guardian_id=$1 AND ward_id=$2`, g.ID, a.ID)
		denied(t, g)
		exec(`UPDATE accounts_guardianrelationship SET status='active' WHERE guardian_id=$1 AND ward_id=$2`, g.ID, a.ID)
		exec(`INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, g.ID, a.ID)
		// The inherited source guardian ceremony preserves transparent oversight
		// across a ward block. Whether that block should revoke parental reading
		// is an explicit policy-review question, not an implemented block veto.
		read(t, g, 13)
		t.Log("policy-review gap: ward/guardian block retains inherited transparent observer reading")
		exec(`DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, g.ID, a.ID)
		exec(`UPDATE accounts_user SET is_active=false WHERE id=$1`, g.ID)
		denied(t, g)
		exec(`UPDATE accounts_user SET is_active=true WHERE id=$1`, g.ID)
	})
	stranger := testdb.Actor(t, db, "privacy6-forged-outsider", "adult")
	stranger.Role = "admin"
	stranger.IsStaff = true
	denied(t, stranger)
	read(t, a, 13)
}

func TestPrivacyCasePort6CapOneDoesNotWeakenPairGatesOrAllowGroupInvite(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-cap-invite-owner", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-cap-invite-peer", "adult")
	c := testdb.Actor(t, s.DB, "privacy6-cap-invite-third", "adult")
	teen := testdb.Actor(t, s.DB, "privacy6-cap-invite-teen", "teen")
	group, err := s.Start(ctx, a, "group", []string{b.Username}, "bounded group")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ConfigurePolicy(Policy{MaxCiphertextBytes: 65536, MaxGroupMembers: 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddParticipant(ctx, a, group, c.Username); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("group invite widened cap1", err)
	}
	if _, err := s.Start(ctx, a, "direct", []string{teen.Username}, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("direct cap1 changed cohort gate", err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, a.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, a, "direct", []string{c.Username}, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("direct cap1 changed block gate", err)
	}
	childA := testdb.Actor(t, s.DB, "privacy6-cap-child-a", "child")
	childB := testdb.Actor(t, s.DB, "privacy6-cap-child-b", "child")
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked' WHERE minor_id=$1`, childB.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(ctx, childA, "direct", []string{childB.Username}, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("direct cap1 changed CHILD consent gate", err)
	}
}

func privacy6SignedJWT(t *testing.T, key *ecdsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256"}`))
	body, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	message := header + "." + base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...)
	return message + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestPrivacyCasePort6ActualSignedAgeVerifyAutomaticallyEvictsOldConversation(t *testing.T) {
	s, acc := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-cohort-owner", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-cohort-reverified", "adult")
	id := casePort3ActiveDirect(t, s, a, b)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	acc.Config.EUDIClientID = "synthetic-local-privacy6"
	acc.Config.TrustedIssuers = map[string]string{"synthetic-local-age-issuer": string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))}
	start := httptest.NewRecorder()
	acc.AgeStart(start, platform.WithActor(httptest.NewRequest("POST", "/api/accounts/age/start/", nil), b))
	if start.Code != 200 {
		t.Fatal("synthetic local age start", start.Code)
	}
	state := object(t, start)
	now := time.Now()
	token := privacy6SignedJWT(t, key, map[string]any{"iss": "synthetic-local-age-issuer", "aud": acc.Config.EUDIClientID, "nonce": state["nonce"], "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "age_over_16": true, "age_over_18": false})
	raw, _ := json.Marshal(map[string]any{"state": state["state"], "vp_token": token})
	verify := httptest.NewRecorder()
	acc.AgeVerify(verify, platform.WithActor(httptest.NewRequest("POST", "/api/accounts/age/verify/", strings.NewReader(string(raw))), b))
	if verify.Code != 200 {
		t.Fatal("signed synthetic age verification", verify.Code)
	}
	var cohort, stateValue string
	var audited int
	if err := s.DB.QueryRow(ctx, `SELECT u.cohort,p.state,(SELECT count(*) FROM safety_auditlog WHERE event='messaging.participation_revoked' AND actor_ref=$1 AND data->>'reason'='cohort_changed') FROM accounts_user u JOIN messaging_participant p ON p.user_id=u.id AND p.conversation_id=$2 WHERE u.id=$1`, b.ID, id).Scan(&cohort, &stateValue, &audited); err != nil || cohort != "teen" || stateValue == "active" || stateValue != "removed" || audited != 1 {
		t.Fatal("actual AgeVerify failed automatic persisted cohort eviction", err)
	}
	var ownerState string
	if err := s.DB.QueryRow(ctx, `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, a.ID).Scan(&ownerState); err != nil || ownerState != "active" {
		t.Fatal("cohort change evicted unrelated owner", err)
	}
}

func TestPrivacyCasePort6ActualErasureDeletesAuthoredCiphertextRow(t *testing.T) {
	s, acc := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-erase-peer", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-erase-author", "adult")
	id := casePort3ActiveDirect(t, s, a, b)
	authored, err := s.Post(ctx, b, id, packet(a, b))
	if err != nil {
		t.Fatal(err)
	}
	retained, err := s.Post(ctx, a, id, packet(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if err := acc.Erase(ctx, b, b); err != nil {
		t.Fatal("actual synthetic self erasure", err)
	}
	var deleted, keys, kept int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM messaging_message WHERE id=$1),(SELECT count(*) FROM messaging_messagekey WHERE message_id=$1),(SELECT count(*) FROM messaging_message WHERE id=$2)`, authored, retained).Scan(&deleted, &keys, &kept); err != nil || deleted != 0 || keys != 0 || kept != 1 {
		t.Fatal("erasure merely nulled sender or deleted unrelated ciphertext", err)
	}
}

func TestPrivacyCasePort6ExactGroupInviteLifecycleAndOutsiderSettingsReports(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-lifecycle-owner", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-lifecycle-peer", "adult")
	c := testdb.Actor(t, s.DB, "privacy6-lifecycle-third", "adult")
	outsider := testdb.Actor(t, s.DB, "privacy6-lifecycle-outsider", "adult")
	group, err := s.Start(ctx, a, "group", []string{b.Username, c.Username}, "Hikers")
	if err != nil {
		t.Fatal(err)
	}
	var title, creatorState string
	var invited int
	if err := s.DB.QueryRow(ctx, `SELECT c.title,(SELECT state FROM messaging_participant WHERE conversation_id=c.id AND user_id=$2),(SELECT count(*) FROM messaging_participant WHERE conversation_id=c.id AND state='invited') FROM messaging_conversation c WHERE c.id=$1`, group, a.ID).Scan(&title, &creatorState, &invited); err != nil || title != "Hikers" || creatorState != "active" || invited != 2 {
		t.Fatal("exact source group invitation states", err)
	}
	if err := s.Transition(ctx, b, group, "accept"); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, group, "accept"); err == nil {
		t.Fatal("second invite acceptance admitted")
	}
	if err := s.Transition(ctx, b, group, "leave"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.DB.QueryRow(ctx, `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, group, b.ID).Scan(&state); err != nil || state != "left" {
		t.Fatal("leave did not persist exact source state", err)
	}
	direct := casePort3ActiveDirect(t, s, a, b)
	if err := s.SetDisappearing(ctx, outsider, direct, 3600); err == nil {
		t.Fatal("outsider changed disappearance setting")
	}
	message, err := s.Post(ctx, a, direct, packet(a, b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Report(ctx, outsider, direct, message, "spam", "", ""); err == nil {
		t.Fatal("outsider reported inaccessible ciphertext")
	}
	var reports int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report`).Scan(&reports); err != nil || reports != 0 {
		t.Fatal("outsider report persisted", err)
	}
}

func TestPrivacyCasePort6ParticipantKeysExactCeilingFour(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-query-child-a", "child")
	b := testdb.Actor(t, s.DB, "privacy6-query-child-b", "child")
	g := testdb.Actor(t, s.DB, "privacy6-query-guardian", "adult")
	for _, actor := range []platform.Actor{a, b, g} {
		if _, err := s.RegisterKey(ctx, actor, jwk(actor.Username), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	id := casePort3ActiveDirect(t, s, a, b)
	casePort3GuardianLink(t, s, g, a)
	if err := s.AddGuardian(ctx, g, id); err != nil {
		t.Fatal(err)
	}
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	trace.Reset()
	keys, err := s.ParticipantKeys(ctx, a, id)
	queries := trace.Count()
	if err != nil || len(keys) != 3 {
		t.Fatal("exact source participant fixture incomplete", err)
	}
	t.Logf("source child+child+guardian participant keys actualqueries=%d originalceiling=4", queries)
	if queries > 4 {
		t.Fatal(fmt.Sprintf("participant-key query source ceiling violated:%d>4", queries))
	}
}
