package messaging

import (
	"context"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func casePort3GuardianLink(t *testing.T, s *Service, g, ward platform.Actor) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,status,relationship,consent_id,created_at,updated_at) VALUES($1,$2,'active','parent',NULL,now(),now())`, g.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
}

func TestCasePort3GuardianEnrollmentAuthorityNegativeMatrix(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case3-enroll-child-a", "child")
	b := fixtureUser(t, s, "case3-enroll-child-b", "child")
	unrelated := fixtureUser(t, s, "case3-enroll-unrelated-ward", "child")
	id := casePort3ActiveDirect(t, s, a, b)
	for _, scenario := range []string{"unlinked-adult", "wrong-ward", "keyless-guardian", "teen-ward"} {
		t.Run(scenario, func(t *testing.T) {
			g := fixtureUser(t, s, "case3-enroll-g-"+scenario, "adult")
			conversation := id
			if scenario != "keyless-guardian" {
				if _, err := s.RegisterKey(ctx, g, jwk(g.Username), "", nil); err != nil {
					t.Fatal(err)
				}
			}
			switch scenario {
			case "wrong-ward":
				casePort3GuardianLink(t, s, g, unrelated)
			case "keyless-guardian":
				casePort3GuardianLink(t, s, g, a)
			case "teen-ward":
				teen := fixtureUser(t, s, "case3-enroll-teen", "teen")
				peer := fixtureUser(t, s, "case3-enroll-teen-peer", "teen")
				conversation = casePort3ActiveDirect(t, s, teen, peer)
				casePort3GuardianLink(t, s, g, teen)
			}
			if err := s.AddGuardian(ctx, g, conversation); err == nil {
				t.Fatal("guardian enrolled without reviewed child basis")
			}
			var count int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, conversation, g.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("refused guardian enrollment wrote participant", err)
			}
		})
	}
}

func TestCasePort3GuardianTransparentReadonlyKeysAndActualAPI(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case3-observer-child-a", "child")
	b := fixtureUser(t, s, "case3-observer-child-b", "child")
	g := fixtureUser(t, s, "case3-transparent-guardian", "adult")
	outsider := fixtureUser(t, s, "case3-observer-outsider", "child")
	for _, who := range []platform.Actor{a, b, g} {
		if _, err := s.RegisterKey(ctx, who, jwk(who.Username), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	id := casePort3ActiveDirect(t, s, a, b)
	casePort3GuardianLink(t, s, g, a)
	discovery := call(s, g, "GET", "/api/messaging/guardian/conversations/", nil)
	if discovery.Code != 200 {
		t.Fatal("guardian discovery API failed", discovery.Code)
	}
	found := false
	for _, row := range casePort3Array(t, discovery.Body.Bytes()) {
		if row["id"] == float64(id) {
			found = true
		}
	}
	if !found {
		t.Fatal("guardian couldn't discover linked ward conversation")
	}
	keysPath := fmt.Sprintf("/api/messaging/conversations/%d/keys/", id)
	before := call(s, a, "GET", keysPath, nil)
	if before.Code != 200 {
		t.Fatal(before.Code)
	}
	keys := casePort3Array(t, before.Body.Bytes())
	names := map[string]bool{}
	for _, key := range keys {
		names[key["username"].(string)] = true
	}
	if len(names) != 2 || !names[a.Username] || !names[b.Username] {
		t.Fatal("actual participant keys API omitted active children")
	}
	if out := call(s, outsider, "GET", keysPath, nil); out.Code != 403 {
		t.Fatal("outsider participant keys API status changed", out.Code)
	}
	enrolled := call(s, g, "POST", fmt.Sprintf("/api/messaging/conversations/%d/guardian/", id), nil)
	if enrolled.Code != 201 {
		t.Fatal("guardian enrollment API failed", enrolled.Code)
	}
	visible := false
	for _, row := range object(t, enrolled)["participants"].([]any) {
		participant := row.(map[string]any)
		if participant["role"] == "guardian" {
			visible = true
		}
	}
	if !visible {
		t.Fatal("guardian role hidden from actual participant serialization")
	}
	var role, state string
	if err := s.DB.QueryRow(ctx, `SELECT role,state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, g.ID).Scan(&role, &state); err != nil || role != "guardian" || state != "active" {
		t.Fatal("observer wasn't active guardian participant", err)
	}
	if ok, err := s.CanView(ctx, s.DB, g, id); err != nil || !ok {
		t.Fatal("guardian didn't receive transparent view authority", err)
	}
	if _, err := s.Post(ctx, a, id, packet(a, b, g)); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Messages(ctx, g, id, 50, 0, 0)
	if err != nil || len(rows) != 1 || rows[0].(map[string]any)["key"] == nil {
		t.Fatal("authorized guardian didn't receive own decryptable history key", err)
	}
	if _, err := s.Post(ctx, g, id, packet(a, b, g)); err == nil {
		t.Fatal("guardian wrote into child channel")
	}
	if err := s.RemoveParticipant(ctx, a, id, g.Username); err == nil {
		t.Fatal("child admin evicted supervisory guardian")
	}
	participantKeys, err := s.ParticipantKeys(ctx, a, id)
	if err != nil || len(participantKeys) != 3 {
		t.Fatal("transparent guardian public key omitted", err)
	}
	guardianKey := false
	for _, raw := range participantKeys {
		key := raw.(map[string]any)
		if key["username"] == g.Username && key["role"] == "guardian" {
			guardianKey = true
		}
	}
	if !guardianKey {
		t.Fatal("guardian role absent from participant keys")
	}
	if err := s.Transition(ctx, g, id, "leave"); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CanView(ctx, s.DB, g, id); err != nil || ok {
		t.Fatal("voluntary guardian leave retained read authority", err)
	}
}

func TestCasePort3MessagingFreshReadVetoesAndDisappearingAuthority(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "case3-read-owner", "adult")
	b := fixtureUser(t, s, "case3-read-peer", "adult")
	outsider := fixtureUser(t, s, "case3-read-outsider", "adult")
	id := casePort3ActiveDirect(t, s, a, b)
	if ok, err := s.CanView(ctx, s.DB, b, id); err != nil || !ok {
		t.Fatal("baseline active peer couldn't view", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CanView(ctx, s.DB, b, id); err != nil || ok {
		t.Fatal("captured active actor retained view after deactivation", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true,cohort='teen',age_band='16_17' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CanView(ctx, s.DB, b, id); err != nil || ok {
		t.Fatal("captured adult retained view after cohort correction", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET cohort='adult',age_band='adult' WHERE id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisappearing(ctx, b, id, 3600); err != nil {
		t.Fatal("direct peer couldn't set source timer", err)
	}
	var seconds int
	if err := s.DB.QueryRow(ctx, `SELECT disappearing_seconds FROM messaging_conversation WHERE id=$1`, id).Scan(&seconds); err != nil || seconds != 3600 {
		t.Fatal("timer value wasn't stored", err)
	}
	if err := s.SetDisappearing(ctx, a, id, 42); err == nil {
		t.Fatal("unsupported timer accepted")
	}
	path := fmt.Sprintf("/api/messaging/conversations/%d/disappearing/", id)
	if out := call(s, a, "POST", path, map[string]any{"seconds": 86400}); out.Code != 200 || object(t, out)["disappearing_seconds"] != float64(86400) {
		t.Fatal("actual disappearing API wire value changed", out.Code)
	}
	if out := call(s, outsider, "POST", path, map[string]any{"seconds": 86400}); out.Code != 400 {
		t.Fatal("outsider timer API status changed", out.Code)
	}
	group, err := s.Start(ctx, a, "group", []string{b.Username}, "Timer group")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(ctx, b, group, "accept"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDisappearing(ctx, b, group, 3600); err == nil {
		t.Fatal("ordinary group member set timer")
	}
	if err := s.SetDisappearing(ctx, a, group, 3600); err != nil {
		t.Fatal("group admin couldn't set timer", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT disappearing_seconds FROM messaging_conversation WHERE id=$1`, group).Scan(&seconds); err != nil || seconds != 3600 {
		t.Fatal("group admin timer wasn't persisted", err)
	}
}
