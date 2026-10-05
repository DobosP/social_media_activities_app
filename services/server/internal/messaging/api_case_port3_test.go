package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func casePort3Array(t *testing.T, raw []byte) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func casePort3ActiveDirect(t *testing.T, s *Service, a, b platform.Actor) int64 {
	t.Helper()
	id, err := s.Start(context.Background(), a, "direct", []string{b.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(context.Background(), b, id, "accept"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCasePort3MessagingActualKeyRegistryAPIScopeAndShape(t *testing.T) {
	s := fixture(t)
	a := fixtureUser(t, s, "case3-registry-a", "adult")
	b := fixtureUser(t, s, "case3-registry-b", "adult")
	child := fixtureUser(t, s, "case3-registry-child", "child")
	for _, user := range []platform.Actor{a, b, child} {
		key := jwk(user.Username)
		created := call(s, user, "POST", "/api/messaging/keys/", map[string]any{"public_jwk": key})
		if created.Code != 201 {
			t.Fatal("key registry POST failed", created.Code)
		}
		own := call(s, user, "GET", "/api/messaging/keys/", nil)
		if own.Code != 200 || !reflect.DeepEqual(object(t, own)["public_jwk"], key) {
			t.Fatal("own key API roundtrip changed publicJWK", own.Code)
		}
	}
	if out := call(s, a, "POST", "/api/messaging/keys/", map[string]any{"public_jwk": map[string]any{"kty": "EC", "x": "synthetic-public", "d": "synthetic-private-sentinel"}}); out.Code != 400 {
		t.Fatal("privateJWK API status changed", out.Code)
	}
	contact := call(s, a, "GET", "/api/messaging/keys/"+b.Username+"/", nil)
	if contact.Code != 200 {
		t.Fatal("same-cohort public key API refused", contact.Code)
	}
	data := object(t, contact)
	if data["user"].(map[string]any)["username"] != b.Username {
		t.Fatal("contact key API selected wrong user")
	}
	if _, present := data["wrapped_private_jwk"]; present {
		t.Fatal("contact key API exposed private backup")
	}
	if out := call(s, a, "GET", "/api/messaging/keys/"+child.Username+"/", nil); out.Code != 404 {
		t.Fatal("cross-cohort registry API existence leaked", out.Code)
	}
}

func TestCasePort3MessagingActualConversationCreationAndInvitationAPI(t *testing.T) {
	s := fixture(t)
	a := fixtureUser(t, s, "case3-conversation-a", "adult")
	b := fixtureUser(t, s, "case3-conversation-b", "adult")
	c := fixtureUser(t, s, "case3-conversation-c", "adult")
	child := fixtureUser(t, s, "case3-conversation-child", "child")
	direct := call(s, a, "POST", "/api/messaging/conversations/", map[string]any{"kind": "direct", "username": b.Username})
	if direct.Code != 201 {
		t.Fatal("direct conversation API failed", direct.Code)
	}
	data := object(t, direct)
	if data["kind"] != "direct" || data["my_state"] != "active" {
		t.Fatal("direct creator API authority shape changed")
	}
	id := int64(data["id"].(float64))
	var creatorState, creatorRole, targetState, cohort string
	if err := s.DB.QueryRow(context.Background(), `SELECT a.state,a.role,b.state,c.cohort FROM messaging_conversation c JOIN messaging_participant a ON a.conversation_id=c.id AND a.user_id=$2 JOIN messaging_participant b ON b.conversation_id=c.id AND b.user_id=$3 WHERE c.id=$1`, id, a.ID, b.ID).Scan(&creatorState, &creatorRole, &targetState, &cohort); err != nil || creatorState != "active" || creatorRole != "admin" || targetState != "invited" || cohort != "adult" {
		t.Fatal("direct invitation state/role/cohort contract changed", err)
	}
	listing := call(s, b, "GET", "/api/messaging/conversations/", nil)
	if listing.Code != 200 {
		t.Fatal(listing.Code)
	}
	rows := casePort3Array(t, listing.Body.Bytes())
	if len(rows) != 1 || rows[0]["my_state"] != "invited" {
		t.Fatal("pending invite missing from actual API list")
	}
	accepted := call(s, b, "POST", fmt.Sprintf("/api/messaging/conversations/%d/accept/", id), nil)
	if accepted.Code != 200 || object(t, accepted)["my_state"] != "active" {
		t.Fatal("accept API did not promote membership", accepted.Code)
	}
	if out := call(s, a, "POST", "/api/messaging/conversations/", map[string]any{"kind": "direct", "username": child.Username}); out.Code != 400 {
		t.Fatal("cross-cohort direct API status changed", out.Code)
	}
	group := call(s, a, "POST", "/api/messaging/conversations/", map[string]any{"kind": "group", "title": "Trail crew", "usernames": []string{b.Username, c.Username}})
	if group.Code != 201 || object(t, group)["title"] != "Trail crew" || len(object(t, group)["participants"].([]any)) != 3 {
		t.Fatal("group API title/participant shape changed", group.Code)
	}
	if _, err := s.Start(context.Background(), a, "group", nil, ""); err == nil {
		t.Fatal("empty group target list admitted")
	}
	if _, err := s.Start(context.Background(), a, "group", []string{b.Username, child.Username}, ""); err == nil {
		t.Fatal("mixed-cohort group target admitted")
	}
}

func TestCasePort3MessagingActualCiphertextHistoryReportAndOutsiderAPI(t *testing.T) {
	s := fixture(t)
	a := fixtureUser(t, s, "case3-wire-a", "adult")
	b := fixtureUser(t, s, "case3-wire-b", "adult")
	outsider := fixtureUser(t, s, "case3-wire-outsider", "adult")
	id := casePort3ActiveDirect(t, s, a, b)
	path := fmt.Sprintf("/api/messaging/conversations/%d/messages/", id)
	in := packet(a, b)
	posted := call(s, a, "POST", path, in)
	if posted.Code != 201 {
		t.Fatal("ciphertext POST API failed", posted.Code)
	}
	message := int64(object(t, posted)["id"].(float64))
	history := call(s, b, "GET", path, nil)
	if history.Code != 200 {
		t.Fatal(history.Code)
	}
	rows := casePort3Array(t, history.Body.Bytes())
	if len(rows) != 1 || rows[0]["ciphertext"] != in.Ciphertext || rows[0]["key"].(map[string]any)["wrapped_key"] != "opaque-wrapped-content-key" {
		t.Fatal("actual history ciphertext/own-key shape changed")
	}
	if out := call(s, a, "POST", path, packet(a)); out.Code != 400 {
		t.Fatal("incomplete recipients API status changed", out.Code)
	}
	if out := call(s, outsider, "GET", path, nil); out.Code != 403 {
		t.Fatal("outsider history API status changed", out.Code)
	}
	if out := call(s, outsider, "POST", path, MessageInput{Ciphertext: "x", IV: "y", RecipientKeys: []RecipientKey{}}); out.Code != 400 {
		t.Fatal("outsider send API status changed", out.Code)
	}
	report := call(s, b, "POST", fmt.Sprintf("%s%d/report/", path, message), map[string]any{"reason": "harassment", "decrypted_excerpt": "synthetic client report excerpt"})
	if report.Code != 201 {
		t.Fatal("actual report API failed", report.Code)
	}
	var found bool
	if err := s.DB.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM safety_report WHERE reporter_id=$1 AND reason='harassment' AND position('synthetic client report excerpt' in detail)>0)`, b.ID).Scan(&found); err != nil || !found {
		t.Fatal("report API did not persist reason/excerpt", err)
	}
	if out := call(s, platform.Actor{}, "GET", path, nil); out.Code != 401 && out.Code != 403 {
		t.Fatal("anonymous actual history API was not gated", out.Code)
	}
}

func TestCasePort3MessagingActualListAndNewestHistoryCursorBounds(t *testing.T) {
	s := fixture(t)
	s.ConversationLimit = 3
	s.MessagePageLimit = 5
	a := fixtureUser(t, s, "case3-bounded-owner", "adult")
	var peers []platform.Actor
	var ids []int64
	for i := 0; i < 6; i++ {
		b := fixtureUser(t, s, fmt.Sprintf("case3-bounded-peer-%d", i), "adult")
		id, err := s.Start(context.Background(), a, "direct", []string{b.Username}, "")
		if err != nil {
			t.Fatal(err)
		}
		peers = append(peers, b)
		ids = append(ids, id)
	}
	listing := call(s, a, "GET", "/api/messaging/conversations/", nil)
	if listing.Code != 200 || len(casePort3Array(t, listing.Body.Bytes())) != 3 {
		t.Fatal("legacy conversation list did not obey hard cap", listing.Code)
	}
	s.ConversationLimit = 4
	listing = call(s, a, "GET", "/api/v1/messaging/conversations/?limit=2", nil)
	page := object(t, listing)
	if listing.Code != 200 || page["limit"] != float64(2) || len(page["results"].([]any)) != 2 || page["next_cursor"] == "" {
		t.Fatal("v1 conversation cursor envelope changed", listing.Code)
	}
	if err := s.Transition(context.Background(), peers[0], ids[0], "accept"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 9; i++ {
		in := packet(a, peers[0])
		in.Ciphertext = fmt.Sprintf("Y2lwaGVy%d", i)
		if _, err := s.Post(context.Background(), a, ids[0], in); err != nil {
			t.Fatal(err)
		}
	}
	legacyPath := fmt.Sprintf("/api/messaging/conversations/%d/messages/", ids[0])
	history := call(s, peers[0], "GET", legacyPath, nil)
	rows := casePort3Array(t, history.Body.Bytes())
	if history.Code != 200 || len(rows) != 5 || rows[len(rows)-1]["ciphertext"] != "Y2lwaGVy8" {
		t.Fatal("legacy history didn't keep newest5", history.Code)
	}
	path := fmt.Sprintf("/api/v1/messaging/conversations/%d/messages/", ids[0])
	first := call(s, peers[0], "GET", path+"?limit=3", nil)
	page = object(t, first)
	ciphertexts := func(value any) []string {
		var out []string
		for _, row := range value.([]any) {
			out = append(out, row.(map[string]any)["ciphertext"].(string))
		}
		return out
	}
	if first.Code != 200 || !reflect.DeepEqual(ciphertexts(page["results"]), []string{"Y2lwaGVy6", "Y2lwaGVy7", "Y2lwaGVy8"}) {
		t.Fatal("v1 newest3 chronological history changed", first.Code)
	}
	older := call(s, peers[0], "GET", path+"?limit=3&cursor="+page["next_cursor"].(string), nil)
	if !reflect.DeepEqual(ciphertexts(object(t, older)["results"]), []string{"Y2lwaGVy3", "Y2lwaGVy4", "Y2lwaGVy5"}) {
		t.Fatal("older cursor didn't select previous chronological page")
	}
	bounded := call(s, peers[0], "GET", path+"?limit=50", nil)
	if object(t, bounded)["limit"] != float64(5) || len(object(t, bounded)["results"].([]any)) != 5 {
		t.Fatal("caller limit widened configured history cap")
	}
	smaller := call(s, peers[0], "GET", path+"?limit=2", nil)
	if len(object(t, smaller)["results"].([]any)) != 2 {
		t.Fatal("caller couldn't narrow history cap")
	}
}

func TestCasePort3MessagingMetadataSearchCannotRediscoverDepartedParticipant(t *testing.T) {
	s := fixture(t)
	a := fixtureUser(t, s, "case3-search-owner", "adult")
	b := fixtureUser(t, s, "case3-zelda", "adult")
	c := fixtureUser(t, s, "case3-quincy", "adult")
	id, err := s.Start(context.Background(), a, "group", []string{b.Username, c.Username}, "Trail crew")
	if err != nil {
		t.Fatal(err)
	}
	for _, peer := range []platform.Actor{b, c} {
		if err := s.Transition(context.Background(), peer, id, "accept"); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Transition(context.Background(), c, id, "leave"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := s.DB.QueryRow(context.Background(), `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, id, c.ID).Scan(&state); err != nil || state != "left" {
		t.Fatal("leave didn't persist hidden-participant state", err)
	}
	active := call(s, a, "GET", "/api/messaging/conversations/?q=zelda", nil)
	if active.Code != 200 || len(casePort3Array(t, active.Body.Bytes())) != 1 {
		t.Fatal("active participant query stopped matching", active.Code)
	}
	left := call(s, a, "GET", "/api/messaging/conversations/?q=quincy", nil)
	if left.Code != 200 || len(casePort3Array(t, left.Body.Bytes())) != 0 || strings.Contains(left.Body.String(), c.Username) {
		t.Fatal("search rediscovered departed participant", left.Code)
	}
}
