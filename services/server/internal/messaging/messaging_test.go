package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/chat"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	nativeschema "github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var messagingTestDSN = flag.String("messaging-test-dsn", "", "Explicit disposable fixture database; no environment credential discovery")

func fixture(t *testing.T) *Service {
	t.Helper()
	if *messagingTestDSN == "" {
		t.Skip("explicit disposable messaging-test-dsn not supplied")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, *messagingTestDSN)
	if e != nil {
		t.Fatal(e)
	}
	schema := fmt.Sprintf("messaging_native_test_%d", time.Now().UnixNano())
	if _, e = admin.Exec(ctx, `CREATE SCHEMA `+schema); e != nil {
		t.Fatal(e)
	}
	rows, e := admin.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' AND (tablename LIKE 'accounts_%' OR tablename LIKE 'messaging_%' OR tablename LIKE 'social_%' OR tablename LIKE 'safety_%' OR tablename LIKE 'notifications_%' OR tablename LIKE 'recommendations_%' OR tablename LIKE 'taxonomy_%' OR tablename LIKE 'places_%' OR tablename LIKE 'media_%' OR tablename='django_content_type') ORDER BY tablename`)
	if e != nil {
		t.Fatal(e)
	}
	var tables []string
	for rows.Next() {
		var name string
		if e = rows.Scan(&name); e != nil {
			t.Fatal(e)
		}
		tables = append(tables, name)
	}
	rows.Close()
	for _, name := range tables {
		if _, e = admin.Exec(ctx, `CREATE TABLE `+schema+`.`+pgx.Identifier{name}.Sanitize()+` (LIKE public.`+pgx.Identifier{name}.Sanitize()+` INCLUDING ALL)`); e != nil {
			t.Fatal(name, e)
		}
	}
	for _, name := range []string{"taxonomy_activitycategory", "taxonomy_activitytype", "django_content_type"} {
		if _, e = admin.Exec(ctx, `INSERT INTO `+schema+`.`+name+` SELECT * FROM public.`+name); e != nil {
			t.Fatal(e)
		}
	}
	cfg, e := pgxpool.ParseConfig(*messagingTestDSN)
	if e != nil {
		t.Fatal(e)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	cfg.MaxConns = 4
	db, e := pgxpool.NewWithConfig(ctx, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		db.Close()
		_, e := admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
		if e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	// LIKE fixtures also need the native functions, capacity seed and triggers.
	if e = nativeschema.Migrate(ctx, db); e != nil {
		t.Fatal(e)
	}
	if e = EnsureSchema(ctx, db); e != nil {
		t.Fatal(e)
	}
	return New(db, platform.CursorCodec{Key: bytes.Repeat([]byte{3}, 32)})
}
func TestPostgresPlainThreadLiveDelivery(t *testing.T) {
	s := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := fixtureUser(t, s, "plain-owner", "adult")
	b := fixtureUser(t, s, "plain-member", "adult")
	domain := social.New(s.DB, platform.RecordAudit)
	domain.BodyMarkup = func(text string, _ map[string]bool, _ bool) string { return html.EscapeString(text) }
	var place, activityType int64
	if e := s.DB.QueryRow(ctx, `INSERT INTO places_place(name,source,osm_type,osm_id,external_id,location,raw_tags,address_street,address_housenumber,address_city,address_postcode,address_country,opening_hours_raw,opening_hours,phone,website,first_seen_at,last_seen_at,attribution,license_name,provenance_url) VALUES('Synthetic live hall','osm','',NULL,'',ST_SetSRID(ST_MakePoint(23.6,46.77),4326),'{}','','','Cluj-Napoca','','RO','','{}','','',now(),now(),'','','') RETURNING id`).Scan(&place); e != nil {
		t.Fatal(e)
	}
	if e := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&activityType); e != nil {
		t.Fatal(e)
	}
	activity, e := domain.CreateActivity(ctx, a, social.ActivityInput{Place: place, ActivityType: activityType, Title: "Synthetic live activity", StartsAt: time.Now().Add(time.Hour)})
	if e != nil {
		t.Fatal(e)
	}
	membership, e := domain.Join(ctx, b, activity)
	if e != nil {
		t.Fatal(e)
	}
	if e = domain.Vote(ctx, a, membership, true, false); e != nil {
		t.Fatal(e)
	}
	var thread int64
	if e = s.DB.QueryRow(ctx, `SELECT id FROM social_thread WHERE activity_id=$1`, activity).Scan(&thread); e != nil {
		t.Fatal(e)
	}
	broker := chat.NewBroker(s.DB)
	go broker.Run(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !broker.Ready() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !broker.Ready() {
		t.Fatal("listener not ready")
	}
	plain, e := chat.PlainAdapter(broker, chat.PlainCallbacks{Authorize: func(ctx context.Context, a platform.Actor, id int64) (bool, error) {
		return domain.CanReadThread(ctx, s.DB, a, id)
	}, Write: func(ctx context.Context, a platform.Actor, id int64, body string, reply *int64) error {
		kind, owner, e := domain.ThreadOwner(ctx, a, id)
		if e != nil {
			return e
		}
		_, e = domain.WritePost(ctx, a, kind, owner, social.PostInput{Body: body, ReplyTo: reply}, false)
		return e
	}, Typing: domain.TypingIdentity, Post: domain.LivePost, Attachments: func(context.Context, platform.Actor, int64) ([]any, error) { return []any{}, nil }})
	if e != nil {
		t.Fatal(e)
	}
	authority := func(ctx context.Context, r *http.Request) (platform.Actor, error) {
		if r.Header.Get("Authorization") == "Fixture a" {
			return actor(ctx, s.DB, a.ID)
		}
		return actor(ctx, s.DB, b.ID)
	}
	live, e := chat.NewServer(broker, authority, map[string]chat.Adapter{"chat": plain, "messaging": s.LiveAdapter()}, chat.DefaultConfig())
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	live.Register(mux)
	server := httptest.NewServer(mux)
	defer server.Close()
	dial := func(name string) *websocket.Conn {
		t.Helper()
		url := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws/chat/%d/", thread)
		conn, _, e := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Fixture " + name}, "Origin": {server.URL}}})
		if e != nil {
			t.Fatal(e)
		}
		return conn
	}
	ca, cb := dial("a"), dial("b")
	defer ca.CloseNow()
	defer cb.CloseNow()
	if e = ca.Write(ctx, websocket.MessageText, []byte(`{"body":"<script>synthetic</script>"}`)); e != nil {
		t.Fatal(e)
	}
	readCtx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	_, raw, e := cb.Read(readCtx)
	if e != nil {
		t.Fatal(e)
	}
	var payload map[string]any
	if e = json.Unmarshal(raw, &payload); e != nil || payload["type"] != "message" || payload["body_html"] != "&lt;script&gt;synthetic&lt;/script&gt;" {
		t.Fatal("plain safe payload", e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE social_membership SET state='removed' WHERE user_id=$1 AND activity_id=$2`, b.ID, activity); e != nil {
		t.Fatal(e)
	}
	if e = ca.Write(ctx, websocket.MessageText, []byte(`{"body":"second synthetic"}`)); e != nil {
		t.Fatal(e)
	}
	_, _, e = cb.Read(readCtx)
	if websocket.CloseStatus(e) != websocket.StatusCode(4403) {
		t.Fatal("removed peer received live content", e)
	}
	cancel()
}
func fixtureUser(t *testing.T, s *Service, name, cohort string) platform.Actor {
	t.Helper()
	ctx := context.Background()
	a := platform.Actor{Username: name, DisplayName: name, AgeBand: "adult", Cohort: cohort, Role: "user", IdentityVerified: true, IsActive: true}
	if cohort == "child" {
		a.AgeBand = "under_16"
	}
	if cohort == "teen" {
		a.AgeBand = "16_17"
	}
	e := s.DB.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,$2,$3,true,now(),'user',true,false,now()) RETURNING id,public_id::text`, name, a.AgeBand, cohort).Scan(&a.ID, &a.PublicID)
	if e != nil {
		t.Fatal(e)
	}
	if cohort == "child" {
		if _, e = s.DB.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,'synthetic-guardian','active','',now(),now()+interval '1 year',NULL,'',now(),now())`, a.ID); e != nil {
			t.Fatal(e)
		}
	}
	return a
}
func jwk(label string) map[string]any {
	return map[string]any{"kty": "EC", "crv": "P-256", "x": "synthetic-" + label, "y": "synthetic-public-y", "ext": true}
}
func packet(users ...platform.Actor) MessageInput {
	in := MessageInput{Ciphertext: "Y2lwaGVy", IV: "aXY="}
	for _, user := range users {
		in.RecipientKeys = append(in.RecipientKeys, RecipientKey{RecipientPublicID: user.PublicID, EphemeralPublicJWK: jwk("ephemeral"), WrappedKey: "opaque-wrapped-content-key", WrapIV: "opaque-iv"})
	}
	return in
}
func call(s *Service, a platform.Actor, method, path string, body any) *httptest.ResponseRecorder {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(raw))
	if a.ID > 0 {
		r = platform.WithActor(r, a)
	}
	w := httptest.NewRecorder()
	mux := http.NewServeMux()
	s.Register(mux)
	mux.ServeHTTP(w, r)
	return w
}
func object(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var data map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
		t.Fatal(e)
	}
	return data
}
func TestPublicKeyAndBackupBoundaries(t *testing.T) {
	for _, bad := range []map[string]any{{"kty": "EC", "x": "public", "d": "private"}, {"kty": "RSA", "n": "public"}, {"kty": "EC", "x": map[string]any{"d": "private"}}, {"kty": "EC", "x": "public", "private_key": "private"}} {
		if publicJWK(bad) {
			t.Fatal("private or malformed key accepted")
		}
	}
	if !publicJWK(jwk("valid")) {
		t.Fatal("public key rejected")
	}
	if opaqueBackup(map[string]any{"d": "private"}) || opaqueBackup([]any{"opaque"}) || !opaqueBackup(map[string]any{"ct": "opaque", "iv": "opaque", "salt": "opaque", "v": float64(1)}) {
		t.Fatal("backup classification")
	}
	fp, e := keyFingerprint(map[string]any{"kty": "EC", "x": "abc"})
	if e != nil || len(fp) != 32 {
		t.Fatal(fp, e)
	}
	input := packet(platform.Actor{PublicID: "synthetic"})
	input.Ciphertext = strings.Repeat("é", 32769)
	if validateInput(&input, 256) == nil {
		t.Fatal("UTF8 byte ceiling")
	}
}
func TestAllMessagingRoutesRequireAuthentication(t *testing.T) {
	s := New(nil, platform.CursorCodec{})
	for _, path := range []string{"/api/messaging/keys/", "/api/v1/messaging/keys/", "/api/messaging/conversations/", "/api/v1/messaging/guardian/conversations/", "/api/messaging/conversations/1/messages/", "/api/messaging/conversations/1/keys/"} {
		if w := call(s, platform.Actor{}, "GET", path, nil); w.Code != 401 {
			t.Fatal(path, w.Code)
		}
	}
}

func TestPostgresMessagingContractsAndPrivacy(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "message-a", "adult")
	b := fixtureUser(t, s, "message-b", "adult")
	outsider := fixtureUser(t, s, "message-outsider", "adult")
	child := fixtureUser(t, s, "message-child", "child")
	if w := call(s, a, "GET", "/api/messaging/keys/", nil); w.Code != 404 {
		t.Fatal("missing key", w.Code)
	}
	backup := map[string]any{"ct": "opaque", "iv": "opaque", "salt": "opaque", "v": 1}
	key, e := s.RegisterKey(ctx, a, jwk("a"), "", backup)
	if e != nil {
		t.Fatal(e)
	}
	if key["wrapped_private_jwk"] == nil {
		t.Fatal("own backup missing")
	}
	if _, e = s.RegisterKey(ctx, b, jwk("b"), "", nil); e != nil {
		t.Fatal(e)
	}
	contact, e := s.ContactKey(ctx, a, b.Username)
	if e != nil || len(contact["fingerprint"].(string)) != 32 {
		t.Fatal(contact, e)
	}
	if _, exists := contact["wrapped_private_jwk"]; exists {
		t.Fatal("backup leaked")
	}
	if _, e = s.ContactKey(ctx, child, b.Username); !errors.Is(e, platform.ErrNotFound) {
		t.Fatal("cross-cohort registry", e)
	}
	fp := contact["fingerprint"].(string)
	if verified, e := s.VerifyKey(ctx, a, b.Username, fp); e != nil || verified["verified"] != true {
		t.Fatal(e)
	}
	if _, e = s.RegisterKey(ctx, b, jwk("b-rotated"), "", nil); e != nil {
		t.Fatal(e)
	}
	contact, e = s.ContactKey(ctx, a, b.Username)
	if e != nil || contact["verified"] != false {
		t.Fatal("rotation did not invalidate verification", contact, e)
	}
	direct, e := s.Start(ctx, a, "direct", []string{b.Username}, "ignored")
	if e != nil {
		t.Fatal(e)
	}
	if reused, e := s.Start(ctx, a, "direct", []string{b.Username}, ""); e != nil || reused != direct {
		t.Fatal("direct reuse", reused, e)
	}
	if _, e = s.Start(ctx, a, "direct", []string{child.Username}, ""); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("cross-cohort start", e)
	}
	if _, e = s.Post(ctx, b, direct, packet(a, b)); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("invited sender", e)
	}
	// Sender alone is an active recipient until the invitation is accepted.
	first, e := s.Post(ctx, a, direct, packet(a))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Transition(ctx, b, direct, "accept"); e != nil {
		t.Fatal(e)
	}
	if data, e := s.Messages(ctx, b, direct, 50, 0, 0); e != nil || len(data) != 0 {
		t.Fatal("history without own wrapped key leaked", len(data), e)
	}
	_ = first
	bad := packet(a)
	if _, e = s.Post(ctx, a, direct, bad); !errors.Is(e, platform.ErrInvalid) {
		t.Fatal("missing recipient", e)
	}
	bad = packet(a, b, outsider)
	if _, e = s.Post(ctx, a, direct, bad); !errors.Is(e, platform.ErrInvalid) {
		t.Fatal("smuggled recipient", e)
	}
	bad = packet(a, a)
	if _, e = s.Post(ctx, a, direct, bad); !errors.Is(e, platform.ErrInvalid) {
		t.Fatal("duplicate recipient", e)
	}
	message, e := s.Post(ctx, a, direct, packet(a, b))
	if e != nil {
		t.Fatal(e)
	}
	own, e := s.Message(ctx, b, message, false)
	if e != nil || own["key"] == nil {
		t.Fatal(e)
	}
	if _, exists := own["keys"]; exists {
		t.Fatal("REST returned other wrapped keys")
	}
	live, e := s.Message(ctx, b, message, true)
	if e != nil || len(live["keys"].([]any)) != 2 {
		t.Fatal("broadcast key set", e)
	}
	if _, e = s.Message(ctx, outsider, message, false); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("outsider decrypted metadata", e)
	}
	keys, e := s.ParticipantKeys(ctx, a, direct)
	if e != nil || len(keys) != 2 {
		t.Fatal(e)
	}
	for _, key := range keys {
		obj := key.(map[string]any)
		for _, name := range []string{"public_id", "username", "display_name", "role", "public_jwk", "fingerprint"} {
			if _, ok := obj[name]; !ok {
				t.Fatal("participant-key shape", name)
			}
		}
	}
	for i := 0; i < 3; i++ {
		if _, e = s.Post(ctx, a, direct, packet(a, b)); e != nil {
			t.Fatal(e)
		}
	}
	w := call(s, b, "GET", fmt.Sprintf("/api/v1/messaging/conversations/%d/messages/?limit=2", direct), nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	page := object(t, w)
	if len(page["results"].([]any)) != 2 || page["next_cursor"] == "" {
		t.Fatal("versioned history envelope", page)
	}
	cursor := page["next_cursor"].(string)
	w = call(s, b, "GET", fmt.Sprintf("/api/v1/messaging/conversations/%d/messages/?limit=2&cursor=%s", direct, cursor), nil)
	if w.Code != 200 || len(object(t, w)["results"].([]any)) != 2 {
		t.Fatal("history continuation", w.Code, w.Body.String())
	}
	report, e := s.Report(ctx, b, direct, message, "other", "Synthetic report note", "Client-disclosed evidence")
	if e != nil || report <= 0 {
		t.Fatal(e)
	}
	var detail string
	if e = s.DB.QueryRow(ctx, `SELECT detail FROM safety_report WHERE id=$1`, report).Scan(&detail); e != nil || !strings.Contains(detail, "Reporter-decrypted content:\nClient-disclosed evidence") {
		t.Fatal("report evidence composition", e)
	}
	var leaked bool
	if e = s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM safety_auditlog WHERE data::text LIKE '%Client-disclosed evidence%' OR data::text LIKE '%Y2lwaGVy%')`).Scan(&leaked); e != nil || leaked {
		t.Fatal("content in audit", e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, a.ID, b.ID); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.CanView(ctx, s.DB, b, direct); e != nil || ok {
		t.Fatal("block did not revoke access", ok, e)
	}
	if _, e = s.Post(ctx, a, direct, packet(a, b)); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("blocked sender", e)
	}
	// A block revokes reading, never the reporter-decrypted evidence path:
	// both while blocked by the sender and after blocking the sender back.
	if report, e = s.Report(ctx, b, direct, message, "harassment", "", "Evidence after block"); e != nil || report <= 0 {
		t.Fatal("block removed the message report path", e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, b.ID, a.ID); e != nil {
		t.Fatal(e)
	}
	if report, e = s.Report(ctx, b, direct, message, "harassment", "", "Evidence after blocking the sender"); e != nil || report <= 0 {
		t.Fatal("reporter's own block removed the message report path", e)
	}
	if _, e = s.DB.Exec(ctx, `DELETE FROM safety_block`); e != nil {
		t.Fatal(e)
	}
	if e = s.SetDisappearing(ctx, a, direct, 300); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE messaging_message SET created_at=now()-interval '1 hour' WHERE conversation_id=$1`, direct); e != nil {
		t.Fatal(e)
	}
	if data, e := s.Messages(ctx, b, direct, 50, 0, 0); e != nil || len(data) != 0 {
		t.Fatal("expired history served", e)
	}
	if n, e := s.PurgeExpired(ctx, 100); e != nil || n != 5 {
		t.Fatal("ciphertext purge", n, e)
	}
	var count int
	if e = s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_messagekey`).Scan(&count); e != nil || count != 0 {
		t.Fatal("wrapped keys survived purge", count, e)
	}
}

func TestPostgresGroupsGuardianAndRevocation(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	a := fixtureUser(t, s, "group-child-a", "child")
	b := fixtureUser(t, s, "group-child-b", "child")
	guardian := fixtureUser(t, s, "group-guardian", "adult")
	for _, u := range []platform.Actor{a, b, guardian} {
		if _, e := s.RegisterKey(ctx, u, jwk(u.Username), "", nil); e != nil {
			t.Fatal(e)
		}
	}
	group, e := s.Start(ctx, a, "group", []string{b.Username}, "One-invitee group")
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.Conversation(ctx, a, group)
	if e != nil || c["kind"] != "group" {
		t.Fatal("single invite group became direct", c, e)
	}
	if e = s.Transition(ctx, b, group, "accept"); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,status,relationship,consent_id,created_at,updated_at) VALUES($1,$2,'active','parent',NULL,now(),now())`, guardian.ID, a.ID); e != nil {
		t.Fatal(e)
	}
	if e = s.AddGuardian(ctx, guardian, group); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.CanView(ctx, s.DB, guardian, group); e != nil || !ok {
		t.Fatal("guardian read", ok, e)
	}
	if _, e = s.Post(ctx, guardian, group, packet(a, b, guardian)); !errors.Is(e, platform.ErrForbidden) {
		t.Fatal("guardian wrote", e)
	}
	if e = s.RemoveParticipant(ctx, a, group, guardian.Username); !errors.Is(e, platform.ErrInvalid) {
		t.Fatal("child evicted guardian", e)
	}
	if _, e = s.Post(ctx, a, group, packet(a, b, guardian)); e != nil {
		t.Fatal(e)
	}
	if list, _, e := s.Conversations(ctx, guardian, "", 50, 0, true); e != nil || len(list) != 1 {
		t.Fatal("guardian metadata list", len(list), e)
	}
	if e = s.Transition(ctx, a, group, "leave"); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.CanView(ctx, s.DB, guardian, group); e != nil || ok {
		t.Fatal("orphan guardian retained read", ok, e)
	}
	var state string
	if e = s.DB.QueryRow(ctx, `SELECT state FROM messaging_participant WHERE conversation_id=$1 AND user_id=$2`, group, guardian.ID).Scan(&state); e != nil || state != "removed" {
		t.Fatal("observer row not pruned", state, e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE accounts_parentalconsent SET status='revoked' WHERE minor_id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	if ok, e := s.CanView(ctx, s.DB, b, group); e != nil || ok {
		t.Fatal("consent revocation", ok, e)
	}
}

func TestPostgresWebsocketCrossReplicaAndFreshAccess(t *testing.T) {
	s := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := fixtureUser(t, s, "live-a", "adult")
	b := fixtureUser(t, s, "live-b", "adult")
	id, e := s.Start(ctx, a, "direct", []string{b.Username}, "")
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Transition(ctx, b, id, "accept"); e != nil {
		t.Fatal(e)
	}
	brokers := []*chat.Broker{chat.NewBroker(s.DB), chat.NewBroker(s.DB)}
	for _, broker := range brokers {
		go broker.Run(ctx)
	}
	deadline := time.Now().Add(3 * time.Second)
	for (!brokers[0].Ready() || !brokers[1].Ready()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !brokers[0].Ready() || !brokers[1].Ready() {
		t.Fatal("listeners not ready")
	}
	authority := func(ctx context.Context, r *http.Request) (platform.Actor, error) {
		raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Fixture ")
		if raw == "a" {
			return actor(ctx, s.DB, a.ID)
		}
		if raw == "b" {
			return actor(ctx, s.DB, b.ID)
		}
		return platform.Actor{}, platform.ErrForbidden
	}
	plain := s.LiveAdapter()
	servers := []*httptest.Server{}
	for _, broker := range brokers {
		live, e := chat.NewServer(broker, authority, map[string]chat.Adapter{"chat": plain, "messaging": s.LiveAdapter()}, chat.DefaultConfig())
		if e != nil {
			t.Fatal(e)
		}
		mux := http.NewServeMux()
		live.Register(mux)
		server := httptest.NewServer(mux)
		servers = append(servers, server)
		defer server.Close()
	}
	dial := func(server *httptest.Server, name string) *websocket.Conn {
		t.Helper()
		url := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws/messaging/%d/", id)
		conn, _, e := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": {"Fixture " + name}, "Origin": {server.URL}}})
		if e != nil {
			t.Fatal(e)
		}
		return conn
	}
	ca, cb := dial(servers[0], "a"), dial(servers[1], "b")
	defer ca.CloseNow()
	defer cb.CloseNow()
	raw, _ := json.Marshal(packet(a, b))
	if e = ca.Write(ctx, websocket.MessageText, raw); e != nil {
		t.Fatal(e)
	}
	readCtx, done := context.WithTimeout(ctx, 3*time.Second)
	defer done()
	_, delivered, e := cb.Read(readCtx)
	if e != nil {
		t.Fatal(e)
	}
	var message map[string]any
	if e = json.Unmarshal(delivered, &message); e != nil || message["type"] != "message" || len(message["keys"].([]any)) != 2 {
		t.Fatal("cross-replica payload", e)
	}
	if _, e = s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	if e = cb.Write(ctx, websocket.MessageText, raw); e != nil {
		t.Fatal(e)
	}
	_, _, e = cb.Read(readCtx)
	if websocket.CloseStatus(e) != websocket.StatusCode(4403) {
		t.Fatal("stale socket retained access", e)
	}
	cancel()
	for _, broker := range brokers {
		_ = broker
	}
}
