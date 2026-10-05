package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/avatars"
	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/recommendations"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/coder/websocket"
)

func TestPrivacyCasePort6KeyFingerprintCanonicalOrderingAndChangedKey(t *testing.T) {
	key := map[string]any{"kty": "EC", "crv": "P-256", "x": "QUJD", "y": "REVG"}
	reversed := map[string]any{"y": "REVG", "x": "QUJD", "crv": "P-256", "kty": "EC"}
	fp, err := keyFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	other, err := keyFingerprint(reversed)
	if err != nil || fp != other || len(fp) != 32 {
		t.Fatal("source fingerprint canonical/deterministic32", err)
	}
	key["x"] = "ZZZZ"
	changed, err := keyFingerprint(key)
	if err != nil || fp == changed {
		t.Fatal("fingerprint lost key-specific property", err)
	}
}

func TestPrivacyCasePort6ActualVerificationAPIReflectsAndRejectsBadFingerprint(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	a := testdb.Actor(t, s.DB, "privacy6-verify-viewer", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-verify-subject", "adult")
	key := jwk("privacy6-public-key")
	if _, err := s.RegisterKey(ctx, b, key, "", nil); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := keyFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/messaging/keys/" + b.Username + "/"
	before := call(s, a, "GET", path, nil)
	if before.Code != 200 || object(t, before)["fingerprint"] != fingerprint || object(t, before)["verified"] != false {
		t.Fatal("unverified key API status/fingerprint", before.Code)
	}
	rejected := call(s, a, "POST", "/api/messaging/verify/", map[string]any{"username": b.Username, "fingerprint": "nope"})
	if rejected.Code != 400 {
		t.Fatal("bad verification source API status", rejected.Code)
	}
	var records int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_keyverification`).Scan(&records); err != nil || records != 0 {
		t.Fatal("bad fingerprint persisted verification", err)
	}
	verified := call(s, a, "POST", "/api/messaging/verify/", map[string]any{"username": b.Username, "fingerprint": fingerprint})
	if verified.Code != 200 || object(t, verified)["verified"] != true {
		t.Fatal("actual verify API did not reflect status", verified.Code)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_keyverification WHERE verifier_id=$1 AND subject_id=$2 AND fingerprint=$3`, a.ID, b.ID, fingerprint).Scan(&records); err != nil || records != 1 {
		t.Fatal("actual verify API missing exact verifier/subject DB row", err)
	}
	after := call(s, a, "GET", path, nil)
	if after.Code != 200 || object(t, after)["verified"] != true {
		t.Fatal("verification not reflected in contact key API", after.Code)
	}
}

func TestPrivacyCasePort6MessagingUserRefSharesCanonicalInterestAndEmptyAvatar(t *testing.T) {
	s, _ := privacy6Fixture(t)
	ctx := context.Background()
	empty := testdb.Actor(t, s.DB, "privacy6-avatar-empty", "adult")
	stars := testdb.Actor(t, s.DB, "privacy6-avatar-stars", "adult")
	ref, err := userRef(ctx, s.DB, empty.ID)
	if err != nil {
		t.Fatal(err)
	}
	data := ref.(map[string]any)
	want := avatars.DataURI(avatars.IdenticonSVG(empty.Username, 80))
	if data["avatar"] != want {
		t.Fatal("empty messaging avatar differs from canonical identicon")
	}
	var category int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,description,created_at,updated_at) VALUES('team_sport','Team Sport','',now(),now()) RETURNING id`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"privacy6-ball", "privacy6-foot"} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO taxonomy_activitytype(slug,name,aliases,is_active,created_at,updated_at,category_id,family_friendly,wellness) VALUES($1,$1,'[]',true,now(),now(),$2,true,false)`, slug, category); err != nil {
			t.Fatal(err)
		}
	}
	slugs, err := recommendations.New(s.DB, nil, nil).SetInterests(ctx, stars, []string{"privacy6-ball", "privacy6-foot"})
	if err != nil || len(slugs) != 2 {
		t.Fatal("source declared interests fixture", err)
	}
	ref, err = userRef(ctx, s.DB, stars.ID)
	if err != nil {
		t.Fatal(err)
	}
	data = ref.(map[string]any)
	canonical, err := accounts.Avatar(ctx, s.DB, stars.ID)
	if err != nil {
		t.Fatal(err)
	}
	independent := avatars.DataURI(avatars.ConstellationSVG(stars.Username, []avatars.Node{{Color: "#ff7a45", Category: "team_sport"}, {Color: "#ff7a45", Category: "team_sport"}}, []avatars.Edge{{0, 1}}, avatars.Options{PX: 80}))
	if data["avatar"] != canonical || data["avatar"] != independent || data["avatar"] == avatars.DataURI(avatars.IdenticonSVG(stars.Username, 80)) {
		t.Fatal("messaging declared-interest avatar differs from canonical constellation")
	}
	if len(data) != 4 || data["public_id"] != stars.PublicID || data["username"] != stars.Username || data["display_name"] != stars.DisplayName {
		t.Fatal("messaging identity ref projection widened")
	}
}

func TestPrivacyCasePort6ActualWebsocketRelayEchoAndExactRecipientSet(t *testing.T) {
	s, _ := privacy6Fixture(t)
	a := testdb.Actor(t, s.DB, "privacy6-live-a", "adult")
	b := testdb.Actor(t, s.DB, "privacy6-live-b", "adult")
	invited := testdb.Actor(t, s.DB, "privacy6-live-invited", "adult")
	outsider := testdb.Actor(t, s.DB, "privacy6-live-outsider", "adult")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := casePort3ActiveDirect(t, s, a, b)
	pending, err := s.Start(ctx, a, "direct", []string{invited.Username}, "")
	if err != nil {
		t.Fatal(err)
	}
	broker := chat.NewBroker(s.DB)
	done := make(chan struct{})
	go func() { broker.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("synthetic broker did not stop")
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for !broker.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !broker.Ready() {
		t.Fatal("private fixture live broker not ready")
	}
	users := map[string]platform.Actor{"a": a, "b": b, "invited": invited, "outsider": outsider}
	authority := func(ctx context.Context, r *http.Request) (platform.Actor, error) {
		user, ok := users[strings.TrimPrefix(r.Header.Get("Authorization"), "Synthetic ")]
		if !ok {
			return platform.Actor{}, platform.ErrForbidden
		}
		return actor(ctx, s.DB, user.ID)
	}
	adapter := s.LiveAdapter()
	live, err := chat.NewServer(broker, authority, map[string]chat.Adapter{"chat": adapter, "messaging": adapter}, chat.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	live.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	dial := func(name string, conversation int64) (*websocket.Conn, *http.Response, error) {
		return websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http")+fmt.Sprintf("/ws/messaging/%d/", conversation), &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Synthetic " + name}, "Origin": {server.URL}}})
	}
	for _, tc := range []struct {
		name         string
		conversation int64
	}{{"invited", pending}, {"outsider", id}} {
		t.Run(tc.name, func(t *testing.T) {
			conn, resp, err := dial(tc.name, tc.conversation)
			if conn != nil {
				conn.CloseNow()
			}
			if err == nil || resp == nil || resp.StatusCode != 403 {
				t.Fatal("source forbidden websocket connected", tc.name, err)
			}
		})
	}
	ca, _, err := dial("a", id)
	if err != nil {
		t.Fatal(err)
	}
	defer ca.CloseNow()
	cb, _, err := dial("b", id)
	if err != nil {
		t.Fatal(err)
	}
	defer cb.CloseNow()
	packet := packet(a, b)
	packet.Ciphertext = "Y2lwaGVy"
	packet.IV = "aXY="
	raw, err := json.Marshal(packet)
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Write(ctx, websocket.MessageText, raw); err != nil {
		t.Fatal(err)
	}
	readCtx, finish := context.WithTimeout(ctx, 10*time.Second)
	defer finish()
	for _, tc := range []struct {
		name string
		conn *websocket.Conn
	}{{"sender_echo", ca}, {"peer", cb}} {
		t.Run(tc.name, func(t *testing.T) {
			_, raw, err := tc.conn.Read(readCtx)
			if err != nil {
				t.Fatal(err)
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["type"] != "message" || payload["ciphertext"] != "Y2lwaGVy" {
				t.Fatal("live relay lost exact encrypted payload")
			}
			recipients := map[string]bool{}
			for _, key := range payload["keys"].([]any) {
				recipients[key.(map[string]any)["recipient_public_id"].(string)] = true
			}
			if !reflect.DeepEqual(recipients, map[string]bool{a.PublicID: true, b.PublicID: true}) {
				t.Fatal("live broadcast recipient key set differs from actual members")
			}
		})
	}
}
