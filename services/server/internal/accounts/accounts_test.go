package accounts

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/media"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	nativeschema "github.com/DobosP/social_media_activities_app/services/server/internal/schema"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var accountTestDSN = flag.String("accounts-test-dsn", "", "disposable native Social accounts test database")

func accountFixture(t *testing.T) *Service {
	t.Helper()
	if *accountTestDSN == "" {
		t.Skip("disposable accounts-test-dsn not supplied")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, *accountTestDSN)
	if err != nil {
		t.Fatal("test database configuration invalid")
	}
	name := "accounts_native_test_" + strings.ToLower(rand.Text())
	schema := pgx.Identifier{name}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	rows, err := admin.Query(ctx, `SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename`)
	if err != nil {
		t.Fatal(err)
	}
	tables := []string{}
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	rows.Close()
	for _, table := range tables {
		if _, err = admin.Exec(ctx, "CREATE TABLE "+schema+"."+pgx.Identifier{table}.Sanitize()+" (LIKE public."+pgx.Identifier{table}.Sanitize()+" INCLUDING ALL)"); err != nil {
			t.Fatal(table, err)
		}
	}
	// Preserve the complete reviewed FK graph, including ORM NO ACTION constraints.
	rows, err = admin.Query(ctx, `SELECT t.relname,c.conname,pg_get_constraintdef(c.oid) FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='public' AND c.contype='f' ORDER BY t.relname,c.conname`)
	if err != nil {
		t.Fatal(err)
	}
	type fk struct{ table, name, definition string }
	keys := []fk{}
	cloned := map[string]bool{}
	for _, table := range tables {
		cloned[table] = true
	}
	for rows.Next() {
		var key fk
		if err = rows.Scan(&key.table, &key.name, &key.definition); err != nil {
			t.Fatal(err)
		}
		if cloned[key.table] {
			keys = append(keys, key)
		}
	}
	rows.Close()
	for _, key := range keys {
		definition := strings.ReplaceAll(key.definition, "REFERENCES public.", "REFERENCES "+schema+".")
		definition = strings.ReplaceAll(definition, "REFERENCES ", "REFERENCES ")
		if !strings.Contains(definition, "REFERENCES "+schema+".") {
			definition = strings.Replace(definition, "REFERENCES ", "REFERENCES "+schema+".", 1)
		}
		if _, err = admin.Exec(ctx, "ALTER TABLE "+schema+"."+pgx.Identifier{key.table}.Sanitize()+" ADD CONSTRAINT "+pgx.Identifier{key.name}.Sanitize()+" "+definition); err != nil {
			t.Fatal(key.table, err)
		}
	}
	if _, err = admin.Exec(ctx, "INSERT INTO "+schema+`.django_content_type SELECT * FROM public.django_content_type`); err != nil {
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(*accountTestDSN)
	if err != nil {
		t.Fatal("test database configuration invalid")
	}
	config.ConnConfig.RuntimeParams["search_path"] = name + ",public"
	config.MaxConns = 4
	db, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	// LIKE fixtures also need the native functions, capacity seed and triggers.
	if err = nativeschema.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	auth, err := authcore.New(authcore.Config{PublicURL: "https://app.example"}, store)
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, auth, "generated-test-binding-secret-32-bytes", Config{EUDIClientID: "test-age-client", IdentityUniquenessEnforced: true})
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return s
}
func accountUser(t *testing.T, s *Service, name, band, cohort string) platform.Actor {
	t.Helper()
	ctx := context.Background()
	var id int64
	err := s.DB.QueryRow(ctx, `INSERT INTO accounts_user(password,last_login,is_superuser,public_id,username,display_name,age_band,cohort,is_identity_verified,identity_verified_at,role,is_active,is_staff,date_joined) VALUES('!',NULL,false,gen_random_uuid(),$1,$1,$2,$3,$4,NULL,'user',true,false,now()) RETURNING id`, name, band, cohort, cohort != "unassigned").Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	a, err := s.actor(ctx, s.DB, id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func accountRequest(s *Service, a platform.Actor, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if a.ID > 0 {
		r = platform.WithActor(r, a)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	return out
}
func accountJSON(t *testing.T, out *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
		t.Fatal(out.Code, out.Body)
	}
	return body
}

func testESKey(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return key, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
}
func testESJWT(t *testing.T, key *ecdsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","typ":"JWT"}`))
	raw, _ := json.Marshal(claims)
	message := header + "." + base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return message + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func TestNativeEUDICryptographyAndDataMinimization(t *testing.T) {
	issuer, public := testESKey(t)
	holder, _ := testESKey(t)
	s := New(nil, nil, "", Config{EUDIClientID: "client", TrustedIssuers: map[string]string{"issuer": public}})
	base := map[string]any{"iss": "issuer", "sub": "holder-sub", "aud": "client", "nonce": "nonce", "exp": time.Now().Add(time.Hour).Unix(), "age_over_16": true, "age_over_18": true, "cnf": map[string]any{"jwk": map[string]string{"kty": "EC", "crv": "P-256", "x": base64.RawURLEncoding.EncodeToString(holder.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(holder.Y.FillBytes(make([]byte, 32)))}}}
	proof := testESJWT(t, holder, map[string]any{"aud": "client", "nonce": "nonce", "exp": time.Now().Add(time.Minute).Unix()})
	claims, status, err := s.verifyAge(testESJWT(t, issuer, base), proof, "nonce")
	if err != nil || status != "verified" || claims.Subject != "holder-sub" {
		t.Fatal("valid holder proof rejected", err)
	}
	for name, change := range map[string]func(map[string]any){"issuer": func(c map[string]any) { c["iss"] = "attacker" }, "audience": func(c map[string]any) { c["aud"] = "other" }, "nonce": func(c map[string]any) { c["nonce"] = "other" }, "expiry": func(c map[string]any) { c["exp"] = 1 }, "PII": func(c map[string]any) { c["birthdate"] = "2000-01-01" }, "contradictory": func(c map[string]any) { c["age_over_16"] = false }, "nonboolean": func(c map[string]any) { c["age_over_16"] = "true" }} {
		t.Run(name, func(t *testing.T) {
			copy := map[string]any{}
			for k, v := range base {
				copy[k] = v
			}
			change(copy)
			if _, _, err := s.verifyAge(testESJWT(t, issuer, copy), proof, "nonce"); err == nil {
				t.Fatal("unsafe age proof accepted")
			}
		})
	}
}

func TestNativePendingAccountSelfExportSettingsAndStyles(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "pending", "unknown", "unassigned")
	out := accountRequest(s, a, "GET", "/api/v1/accounts/me/", "")
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	self := accountJSON(t, out)
	if self["can_participate"] != false || self["is_identity_verified"] != false || self["cohort"] != "unassigned" {
		t.Fatal("login granted age or participation")
	}
	if len(self) != 13 {
		t.Fatal("me DTO field mismatch", len(self))
	}
	out = accountRequest(s, a, "POST", "/api/accounts/me/avatar-style/", `{"generation":2}`)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	out = accountRequest(s, a, "GET", "/api/accounts/me/avatar-style/", "")
	if out.Code != 200 || len(accountJSON(t, out)["previews"].([]any)) != 2 {
		t.Fatal(out.Code, out.Body)
	}
	if strings.Contains(out.Body.String(), "fingerprint") || strings.Contains(out.Body.String(), "salt") {
		t.Fatal("style internals exposed")
	}
	out = accountRequest(s, a, "PUT", "/api/accounts/me/settings/", `{"muted_kinds":["system","moderation","arrival"],"access":{"needs_step_free":true}}`)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	settings := accountJSON(t, out)
	if fmt.Sprint(settings["muted_kinds"]) != "[arrival]" {
		t.Fatal("required safety notifications muted", settings)
	}
	out = accountRequest(s, a, "GET", "/api/accounts/me/export/", "")
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	export := accountJSON(t, out)
	if export["schema_version"] != float64(5) || len(export) != 17 {
		t.Fatal("export DTO mismatch", len(export), export)
	}
	if strings.Contains(out.Body.String(), "holder_hash") || strings.Contains(out.Body.String(), "password") {
		t.Fatal("export exposes credentials or holder key")
	}
}

func TestNativeGuardianDisabledAndMutualConsent(t *testing.T) {
	s := accountFixture(t)
	g := accountUser(t, s, "guardian", "adult", "adult")
	w := accountUser(t, s, "ward", "under_16", "child")
	path := "/api/accounts/guardian-links/"
	body := `{"ward":"` + w.PublicID + `"}`
	out := accountRequest(s, g, "POST", path, body)
	if out.Code != 400 {
		t.Fatal("minor onboarding default enabled")
	}
	s.Config.AllowMinorOnboarding = true
	out = accountRequest(s, g, "POST", path, body)
	if out.Code != 201 {
		t.Fatal(out.Code, out.Body)
	}
	token := accountJSON(t, out)["token"].(string)
	var links int
	_ = s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_guardianrelationship`).Scan(&links)
	if links != 0 {
		t.Fatal("invite unilaterally linked ward")
	}
	out = accountRequest(s, w, "POST", path+token+"/accept/", `{}`)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	consent := "/api/accounts/wards/" + w.PublicID + "/consent/"
	out = accountRequest(s, g, "POST", consent, `{}`)
	if out.Code != 201 || accountJSON(t, out)["can_participate"] != true {
		t.Fatal(out.Code, out.Body)
	}
	out = accountRequest(s, g, "DELETE", consent, `{}`)
	if out.Code != 204 {
		t.Fatal(out.Code, out.Body)
	}
	out = accountRequest(s, w, "GET", "/api/accounts/me/", "")
	if accountJSON(t, out)["can_participate"] != false {
		t.Fatal("revoked consent still participates")
	}
}

func TestNativeEraseRealForeignKeysKeepsAuditAndBinding(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "erase", "adult", "adult")
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_identitybinding(holder_hash,user_id,created_at,released_at) VALUES($1,$2,now(),NULL)`, strings.Repeat("b", 64), a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO notifications_notificationpreference(user_id,muted_kinds) VALUES($1,'{}')`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.PickStyle(ctx, a, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, a, a); err != nil {
		t.Fatal(err)
	}
	var users int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_user WHERE id=$1`, a.ID).Scan(&users)
	if users != 0 {
		t.Fatal("account survived erasure")
	}
	var bound *int64
	err := s.DB.QueryRow(ctx, `SELECT user_id FROM accounts_identitybinding WHERE holder_hash=$1`, strings.Repeat("b", 64)).Scan(&bound)
	if err != nil || bound != nil {
		t.Fatal("binding history lost or still names deleted account", err)
	}
	var actor *int64
	var ref *int
	err = s.DB.QueryRow(ctx, `SELECT actor_id,actor_ref FROM safety_auditlog WHERE event='account.erased'`).Scan(&actor, &ref)
	if err != nil || actor != nil || ref == nil || int64(*ref) != a.ID {
		t.Fatal("audit hashchain actor reference erased", err)
	}
}

func TestNativeEUDIFullFlowSingleUseAndWalletUniqueness(t *testing.T) {
	s := accountFixture(t)
	issuer, public := testESKey(t)
	holder, _ := testESKey(t)
	s.Config.TrustedIssuers = map[string]string{"trusted-test-issuer": public}
	a := accountUser(t, s, "proof-user", "unknown", "unassigned")
	prove := func(actor platform.Actor, subject string) *httptest.ResponseRecorder {
		start := accountRequest(s, actor, "POST", "/api/accounts/verify-age/start/", `{}`)
		if start.Code != 200 {
			t.Fatal(start.Code, start.Body)
		}
		flow := accountJSON(t, start)
		nonce := flow["nonce"].(string)
		credential := testESJWT(t, issuer, map[string]any{"iss": "trusted-test-issuer", "aud": "test-age-client", "nonce": nonce, "sub": subject, "exp": time.Now().Add(time.Hour).Unix(), "age_over_16": true, "age_over_18": true, "cnf": map[string]any{"jwk": map[string]string{"kty": "EC", "crv": "P-256", "x": base64.RawURLEncoding.EncodeToString(holder.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(holder.Y.FillBytes(make([]byte, 32)))}}})
		proof := testESJWT(t, holder, map[string]any{"aud": "test-age-client", "nonce": nonce, "exp": time.Now().Add(time.Minute).Unix()})
		body, _ := json.Marshal(map[string]any{"state": flow["state"], "vp_token": credential, "holder_binding_proof": proof})
		out := accountRequest(s, actor, "POST", "/api/accounts/verify-age/", string(body))
		replay := accountRequest(s, actor, "POST", "/api/accounts/verify-age/", string(body))
		if replay.Code != 400 {
			t.Fatal("age ceremony replay accepted", replay.Code)
		}
		return out
	}
	out := prove(a, "durable-holder")
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	payload := accountJSON(t, out)
	if payload["can_participate"] != true || payload["cohort"] != "adult" {
		t.Fatal("proven adult not applied")
	}
	var evidence []byte
	if err := s.DB.QueryRow(context.Background(), `SELECT raw FROM accounts_ageassurance WHERE user_id=$1`, a.ID).Scan(&evidence); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(evidence), "durable-holder") || strings.Contains(string(evidence), "cnf") {
		t.Fatal("raw holder identity persisted")
	}
	other := accountUser(t, s, "other-proof", "unknown", "unassigned")
	out = prove(other, "durable-holder")
	if out.Code != 409 {
		t.Fatal("wallet linked to two accounts", out.Code, out.Body)
	}
	var verified bool
	_ = s.DB.QueryRow(context.Background(), `SELECT is_identity_verified FROM accounts_user WHERE id=$1`, other.ID).Scan(&verified)
	if verified {
		t.Fatal("failed duplicate binding granted age")
	}
	mac := hmac.New(sha256.New, s.Secret)
	_, _ = mac.Write([]byte("banned-holder"))
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_bannedidentity(holder_hash,created_at) VALUES($1,now())`, hex.EncodeToString(mac.Sum(nil))); err != nil {
		t.Fatal(err)
	}
	out = prove(other, "banned-holder")
	if out.Code != 403 {
		t.Fatal("banned wallet admitted", out.Code, out.Body)
	}
}

func TestNativeMobileTokenCredentialCompatibility(t *testing.T) {
	s := accountFixture(t)
	password := "correct horse battery"
	hash, err := authcore.HashPassword(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.Store.CreatePasswordUser(context.Background(), authcore.User{Username: "mobile-native", Name: "Self Name"}, hash)
	if err != nil {
		t.Fatal(err)
	}
	var a platform.Actor
	a, err = s.Store.Actor(context.Background(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	out := accountRequest(s, platform.Actor{}, "POST", "/api/auth/token/", `{"username":"mobile-native","password":"correct horse battery"}`)
	if out.Code != 200 {
		t.Fatal(out.Code, out.Body)
	}
	token := accountJSON(t, out)["token"].(string)
	if len(token) != 40 {
		t.Fatal("DRF token shape changed")
	}
	request := httptest.NewRequest("GET", "/api/accounts/me/", nil)
	request.Header.Set("Authorization", "Token "+token)
	actor, err := s.AuthenticateToken(request)
	if err != nil || actor.ID != a.ID {
		t.Fatal("native mobile token failed")
	}
	payload, err := s.Export(context.Background(), a, true)
	if err != nil {
		t.Fatal(err)
	}
	export, _ := json.Marshal(payload)
	if strings.Contains(string(export), token) {
		t.Fatal("credential leaked into export")
	}
	request.Method = "DELETE"
	out = httptest.NewRecorder()
	s.RevokeToken(out, request)
	if out.Code != 204 {
		t.Fatal(out.Code, out.Body)
	}
	if _, err = s.AuthenticateToken(request); err == nil {
		t.Fatal("revoked token remains active")
	}
}

func accountThread(t *testing.T, s *Service, owner platform.Actor) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	var category, area, group, thread int64
	err := s.DB.QueryRow(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,description,created_at,updated_at,parent_id) VALUES('test-category','Test category','',now(),now(),NULL) RETURNING id`).Scan(&category)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DB.QueryRow(ctx, `INSERT INTO communities_area(city,slug,name,derive_method,min_radius_m,is_active,created_at) VALUES('Cluj-Napoca','test-area','Test area','manual',500,true,now()) RETURNING id`).Scan(&area)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DB.QueryRow(ctx, `INSERT INTO social_group(tier,cohort,title,description,status,is_hidden,is_staff_curated,created_at,updated_at,activity_type_id,area_id,category_id,owner_id,is_publicly_listed) VALUES('category',$1,'Test private group','','active',false,true,now(),now(),NULL,$2,$3,$4,false) RETURNING id`, owner.Cohort, area, category, owner.ID).Scan(&group)
	if err != nil {
		t.Fatal(err)
	}
	err = s.DB.QueryRow(ctx, `INSERT INTO social_thread(created_at,activity_id,group_id) VALUES(now(),NULL,$1) RETURNING id`, group).Scan(&thread)
	if err != nil {
		t.Fatal(err)
	}
	return thread, group
}
func accountPost(t *testing.T, s *Service, user, thread int64, body string, hidden, deleted bool) int64 {
	t.Helper()
	var id int64
	err := s.DB.QueryRow(context.Background(), `INSERT INTO social_post(body,created_at,updated_at,author_id,thread_id,is_hidden,is_announcement,reply_to_id,shared_activity_id,shared_event_id,shared_place_id,is_author_deleted) VALUES($1,now(),now(),$2,$3,$4,false,NULL,NULL,NULL,NULL,$5) RETURNING id`, body, user, thread, hidden, deleted).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNativeExportOwnWithdrawalGuardianAndPlatformPrecedence(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "subject", "under_16", "child")
	other := accountUser(t, s, "peer", "under_16", "child")
	moderator := accountUser(t, s, "reviewer", "adult", "adult")
	thread, _ := accountThread(t, s, a)
	post := accountPost(t, s, a.ID, thread, "own withdrawn words", true, true)
	accountPost(t, s, other.ID, thread, "someone else's private words", false, false)
	for _, self := range []bool{true, false} {
		payload, err := s.Export(context.Background(), a, self)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(payload)
		if strings.Contains(string(encoded), "someone else's private words") {
			t.Fatal("peer-authored words exported")
		}
		posts := payload["thread_posts"].(map[string]any)
		entries := posts["items"].([]json.RawMessage)
		var row map[string]any
		_ = json.Unmarshal(entries[0], &row)
		want := "[removed]"
		if self {
			want = "own withdrawn words"
		}
		if row["body"] != want || row["status"] != "deleted_by_you" {
			t.Fatal("owner/guardian withdrawal policy mismatch", row)
		}
	}
	var ct int
	if err := s.DB.QueryRow(context.Background(), `SELECT id FROM django_content_type WHERE app_label='social' AND model='post'`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO safety_moderationaction(target_id,action,reason,notes,expires_at,created_at,moderator_id,target_type_id,report_id,lifted_at) VALUES($1,'remove','spam','moderator private notes',NULL,now(),$2,$3,NULL,NULL)`, post, moderator.ID, ct); err != nil {
		t.Fatal(err)
	}
	payload, err := s.Export(context.Background(), a, true)
	if err != nil {
		t.Fatal(err)
	}
	posts := payload["thread_posts"].(map[string]any)
	var row map[string]any
	_ = json.Unmarshal(posts["items"].([]json.RawMessage)[0], &row)
	if row["body"] != "[removed]" || row["status"] != "removed" {
		t.Fatal("platform standing REMOVE did not take precedence")
	}
	encoded, _ := json.Marshal(payload)
	if strings.Contains(string(encoded), "moderator private notes") || strings.Contains(string(encoded), moderator.PublicID) {
		t.Fatal("moderator PII/private notes exported")
	}
	safety := payload["safety_record"].(map[string]any)
	if safety["decisions_total"] != 1 {
		t.Fatal("own post decision missing", safety)
	}
	s.Config.ExportPostCap = 2
	accountPost(t, s, a.ID, thread, "newer words one", false, false)
	accountPost(t, s, a.ID, thread, "newer words two", false, false)
	posts, err = s.exportPosts(context.Background(), a.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if posts["total"] != 3 || posts["truncated"] != true {
		t.Fatal("export truncation not disclosed")
	}
	encoded, _ = json.Marshal(posts)
	if strings.Contains(string(encoded), "own withdrawn words") || !strings.Contains(string(encoded), "newer words two") {
		t.Fatal("export kept oldest posts")
	}
}

func TestNativeEraseOwnedGroupThreadAndAuthoredCiphertext(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "destroy", "adult", "adult")
	thread, group := accountThread(t, s, a)
	post := accountPost(t, s, a.ID, thread, "own private post", false, false)
	ctx := context.Background()
	var conversation int64
	if err := s.DB.QueryRow(ctx, `INSERT INTO messaging_conversation(kind,title,cohort,created_at,updated_at,creator_id,disappearing_seconds) VALUES('group','Private fixture','adult',now(),now(),$1,0) RETURNING id`, a.ID).Scan(&conversation); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO messaging_message(algorithm,ciphertext,iv,created_at,conversation_id,sender_id) VALUES('fixture-only','generated-test-ciphertext','fixture-nonce',now(),$1,$2)`, conversation, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, a, a); err != nil {
		t.Fatal(err)
	}
	for table, id := range map[string]int64{"social_group": group, "social_thread": thread, "social_post": post} {
		var exists bool
		if err := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+pgx.Identifier{table}.Sanitize()+" WHERE id=$1)", id).Scan(&exists); err != nil || exists {
			t.Fatal("owned private content survived erasure", table, err)
		}
	}
	var messages int
	_ = s.DB.QueryRow(ctx, `SELECT count(*) FROM messaging_message WHERE conversation_id=$1`, conversation).Scan(&messages)
	if messages != 0 {
		t.Fatal("authored ciphertext survived user deletion")
	}
}

func TestNativeEraseQueuesPrivateMediaBlobDeletion(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "media-owner", "adult", "adult")
	ctx := context.Background()
	if err := media.EnsureSchema(ctx, s.DB); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO media_photo(kind,storage_key,content_type,byte_size,sha256,width,height,scan_status,exif_stripped,created_at,thread_id,uploader_id,phash,thumb_storage_key) VALUES('profile','generated-test/private-image.avif','image/avif',100,$1,16,16,'clean',true,now(),NULL,$2,'','generated-test/private-thumb.webp')`, strings.Repeat("a", 64), a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Erase(ctx, a, a); err != nil {
		t.Fatal(err)
	}
	var images, queued int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM media_photo`).Scan(&images); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM media_go_blobdeletion WHERE storage_key IN ('generated-test/private-image.avif','generated-test/private-thumb.webp')`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if images != 0 || queued != 2 {
		t.Fatal("private media erasure/outbox was incomplete", images, queued)
	}
}

func TestNativeSessionStoreActiveUserAndTenTokenCap(t *testing.T) {
	s := accountFixture(t)
	a := accountUser(t, s, "session-budget", "unknown", "unassigned")
	ctx := context.Background()
	for i := 0; i < 12; i++ {
		err := s.Store.CreateSession(ctx, authcore.Session{TokenHash: fmt.Sprintf("%064d", i), UserID: fmt.Sprint(a.ID), ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
	}
	var count int
	err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_session WHERE user_id=$1`, a.ID).Scan(&count)
	if err != nil || count != 10 {
		t.Fatal("unbounded active sessions", count, err)
	}
	for _, id := range []string{"not-an-id", "0", "999999999999"} {
		if err := s.Store.CreateSession(ctx, authcore.Session{TokenHash: strings.Repeat("a", 64), UserID: id, ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
			t.Fatal("missing identity minted session")
		}
	}
	if _, err = s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.CreateSession(ctx, authcore.Session{TokenHash: strings.Repeat("a", 64), UserID: fmt.Sprint(a.ID), ExpiresAt: time.Now().Add(time.Hour)}); err == nil {
		t.Fatal("disabled identity minted session")
	}
}
