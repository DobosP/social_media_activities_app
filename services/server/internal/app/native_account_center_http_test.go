package app

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// All credentials, real rendered bodies and exports remain memory-only. Never
// print request/response/header/SQL values, keys, cookies, nonces or fixture rows.
type accountCenterAppFixture struct {
	app                     *App
	owner, peer             platform.Actor
	ownerCookie, peerCookie familyAppCredential
}

func newAccountCenterAppFixture(t *testing.T) accountCenterAppFixture {
	t.Helper()
	ctx := context.Background()
	db := testdb.New(t, *appDSN, nil)
	cfg := integrationConfig(t)
	cfg.StaticDir = cfg.SiteRoot + "/static"
	a, err := New(ctx, db, cfg, true)
	if err != nil {
		t.Fatal("assembled account application unavailable")
	}
	a.Web.Config.SiteName = "Generated account site"
	owner := testdb.Actor(t, db, "generated-account-center-owner", "adult")
	peer := testdb.Actor(t, db, "generated-account-center-peer", "adult")
	password := "synthetic-account-center-password-only"
	hash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		t.Fatal("synthetic password setup unavailable")
	}
	for _, actor := range []platform.Actor{owner, peer} {
		if _, err := db.Exec(ctx, `UPDATE accounts_user SET password=$2 WHERE id=$1`, actor.ID, hash); err != nil {
			t.Fatal("synthetic account setup unavailable")
		}
	}
	return accountCenterAppFixture{a, owner, peer, familyAppLogin(t, a, owner.Username, password), familyAppLogin(t, a, peer.Username, password)}
}
func accountCenterAppRequest(t *testing.T, a *App, credential familyAppCredential, method, path, origin string, form url.Values, proof bool, extra func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	values := url.Values{}
	for key, items := range form {
		values[key] = append([]string(nil), items...)
	}
	if proof && credential.CSRF != nil {
		values.Set("csrfmiddlewaretoken", credential.CSRF.Value)
	}
	r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(values.Encode()))
	if method != http.MethodGet && method != http.MethodHead {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	if credential.Session != nil {
		r.AddCookie(credential.Session)
	}
	if credential.CSRF != nil {
		r.AddCookie(credential.CSRF)
	}
	if extra != nil {
		extra(r)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func accountCenterAppHTML(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") || w.Header().Get("X-Social-Runtime") != "go" || !strings.Contains(w.Header().Get("Content-Security-Policy-Report-Only"), "frame-ancestors 'none'") || w.Header().Get("Reporting-Endpoints") == "" {
		t.Fatal("registered account HTML/header contract refused", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `class="account-center-native has-tabbar"`) || !strings.Contains(body, `name="csrfmiddlewaretoken"`) || strings.Contains(body, `id="spa-bootstrap"`) {
		t.Fatal("registered account route did not use finite native view")
	}
	return body
}
func (f accountCenterAppFixture) counts(t *testing.T) string {
	t.Helper()
	var value string
	err := f.app.DB.QueryRow(context.Background(), `SELECT jsonb_build_array((SELECT count(*) FROM accounts_user),(SELECT count(*) FROM authtoken_token WHERE user_id=$1),(SELECT count(*) FROM authtoken_token WHERE user_id=$2),(SELECT count(*) FROM accounts_go_session WHERE user_id=$1),(SELECT count(*) FROM accounts_go_session WHERE user_id=$2),(SELECT count(*) FROM accounts_guardianrelationship),(SELECT count(*) FROM safety_auditlog))::text`, f.owner.ID, f.peer.ID).Scan(&value)
	if err != nil {
		t.Fatal("synthetic account count snapshot unavailable")
	}
	return value
}
func (f accountCenterAppFixture) tokenCounts(t *testing.T) (int, int) {
	t.Helper()
	var own, peer int
	if f.app.DB.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM authtoken_token WHERE user_id=$1),(SELECT count(*) FROM authtoken_token WHERE user_id=$2)`, f.owner.ID, f.peer.ID).Scan(&own, &peer) != nil {
		t.Fatal("synthetic token counts unavailable")
	}
	return own, peer
}
func (f accountCenterAppFixture) token(t *testing.T, actor platform.Actor, created time.Time) string {
	t.Helper()
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatal("synthetic in-memory token unavailable")
	}
	key := hex.EncodeToString(raw[:])
	if _, err := f.app.DB.Exec(context.Background(), `INSERT INTO authtoken_token(key,user_id,created) VALUES($1,$2,$3) ON CONFLICT(user_id) DO UPDATE SET key=excluded.key,created=excluded.created`, key, actor.ID, created); err != nil {
		t.Fatal("synthetic token row unavailable")
	}
	return key
}

// Require the original relation to stay in the disposable first search schema.
// A missing local relation could otherwise fall through to public baseline data.
func (f accountCenterAppFixture) fixtureTable(t *testing.T, table string) (string, string) {
	t.Helper()
	var schema, resolved string
	err := f.app.DB.QueryRow(context.Background(), `SELECT current_schema(),(SELECT n.nspname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.oid=to_regclass($1))`, table).Scan(&schema, &resolved)
	if err != nil || !strings.HasPrefix(schema, "native_fixture_") || schema != resolved {
		t.Fatal("failure fixture relation does not shadow public")
	}
	return schema, pgx.Identifier{schema, table}.Sanitize()
}
func accountCenterAppUndefinedColumn(t *testing.T, err error) {
	t.Helper()
	var postgres *pgconn.PgError
	if !errors.As(err, &postgres) || postgres.Code != "42703" {
		t.Fatal("fixture-local selected column refusal unavailable")
	}
}

func accountCenterAppNotice(f accountCenterAppFixture, actor platform.Actor, path string) *http.Cookie {
	expiry := strconv.FormatInt(f.app.Accounts.Config.Now().Add(30*time.Second).Unix(), 10)
	mac := hmac.New(sha256.New, f.app.Accounts.Secret)
	_, _ = fmt.Fprintf(mac, "social.post_notice.v1\x00%s\x00%s\x00%d\x00%s", "d", expiry, actor.ID, path)
	return &http.Cookie{Name: "social_post_notice", Value: "d." + expiry + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}
}

func TestNativeAccountCenterAssembledHTTPPreservesExistingActionsAndAuthority(t *testing.T) {
	f := newAccountCenterAppFixture(t)
	ctx := context.Background()
	request := func(t *testing.T, credential familyAppCredential, method, path, origin string, values url.Values, proof bool, extra func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		return accountCenterAppRequest(t, f.app, credential, method, path, origin, values, proof, extra)
	}
	t.Run("registered-get-head-self-context-and-static", func(t *testing.T) {
		before := f.counts(t)
		for _, path := range []string{"/you/", "/settings/"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				body := accountCenterAppHTML(t, request(t, f.ownerCookie, method, path+"?user_id="+strconv.FormatInt(f.peer.ID, 10), "", nil, false, nil))
				if !strings.Contains(body, "Generated account site") || !strings.Contains(body, `href="https://app.example`+path+`"`) || !strings.Contains(body, f.owner.DisplayName) || strings.Contains(body, f.peer.DisplayName) || !strings.Contains(body, `class="avatar nav-avatar"`) || strings.Contains(body, `class="pill"`) {
					t.Fatal("self identity, GET enrichment or absent unread producer changed")
				}
				if path == "/settings/" && (!strings.Contains(body, `data-account-token-record="false"`) || strings.Contains(body, `action="/settings/api-token/revoke/"`) || !strings.Contains(body, `href="/account/export/"`) || !strings.Contains(body, `href="/account/delete/"`)) {
					t.Fatal("absent token or readonly account links changed")
				}
			}
		}
		notice := accountCenterAppNotice(f, f.owner, "/settings/")
		withNotice := request(t, f.ownerCookie, http.MethodGet, "/settings/", "", nil, false, func(r *http.Request) { r.AddCookie(notice) })
		noticeBody := accountCenterAppHTML(t, withNotice)
		consumed := false
		for _, cookie := range withNotice.Result().Cookies() {
			if cookie.Name == "social_post_notice" && cookie.MaxAge < 0 {
				consumed = true
			}
		}
		if !consumed || !strings.Contains(noticeBody, "This message had already been hidden by a moderation decision.") {
			t.Fatal("normal GET lost its existing post-notice consumption")
		}
		if f.counts(t) != before {
			t.Fatal("account GET/HEAD changed stored actions")
		}
		// Direct recorder HEAD writes are intentional; net/http suppresses the wire body.
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w := request(t, familyAppCredential{}, method, "/static/css/account-center-native.css", "", nil, false, nil)
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || w.Header().Get("Cache-Control") != "public, max-age=3600" || w.Header().Get("ETag") == "" || len(w.Result().Cookies()) != 0 || method == http.MethodGet && w.Body.Len() == 0 || method == http.MethodHead && w.Body.Len() != 0 {
				t.Fatal("scoped CSS GET/HEAD transport changed", w.Code)
			}
		}
	})
	t.Run("anonymous-method-csrf-origin-and-language-gap", func(t *testing.T) {
		before := f.counts(t)
		for _, path := range []string{"/you/?next=%2Fsettings%2F", "/settings/?user_id=0"} {
			for _, method := range []string{http.MethodGet, http.MethodHead} {
				w := request(t, familyAppCredential{}, method, path, "", nil, false, nil)
				if w.Code != http.StatusFound || w.Header().Get("Location") != "/login/?next="+url.QueryEscape(path) || strings.Contains(w.Body.String(), "account-center-native") {
					t.Fatal("anonymous page admission changed", w.Code)
				}
			}
		}
		for _, path := range []string{"/you/", "/settings/"} {
			w := request(t, f.ownerCookie, http.MethodPost, path, "https://app.example", nil, true, nil)
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
				t.Fatal("existing page method policy changed", w.Code)
			}
		}
		for _, probe := range []struct {
			proof         bool
			origin, value string
		}{{false, "https://app.example", ""}, {false, "https://app.example", "incorrect"}, {true, "https://foreign.invalid", ""}, {true, "", ""}} {
			w := request(t, f.ownerCookie, http.MethodPost, "/settings/api-token/revoke/", probe.origin, url.Values{"csrfmiddlewaretoken": {probe.value}}, probe.proof, nil)
			if w.Code != http.StatusForbidden || f.counts(t) != before {
				t.Fatal("bad CSRF/origin acquired account mutation", w.Code)
			}
		}
		badHeader := request(t, f.ownerCookie, http.MethodPost, "/settings/api-token/revoke/", "https://app.example", nil, true, func(r *http.Request) { r.Header.Set("X-CSRFToken", "incorrect") })
		if badHeader.Code != http.StatusForbidden || f.counts(t) != before {
			t.Fatal("posted CSRF replaced an explicit bad header", badHeader.Code)
		}
		referer := request(t, f.ownerCookie, http.MethodPost, "/settings/api-token/revoke/", "", nil, true, func(r *http.Request) { r.Header.Set("Referer", "https://app.example/settings/") })
		if referer.Code != http.StatusFound || referer.Header().Get("Location") != "/settings/" || f.counts(t) != before {
			t.Fatal("existing same-origin Referer/form admission changed", referer.Code)
		}
		w := request(t, f.ownerCookie, http.MethodPost, "/i18n/setlang/", "https://app.example", url.Values{"language": {"ro"}, "next": {"/settings/"}}, true, nil)
		if w.Code != http.StatusNotFound || f.counts(t) != before {
			t.Fatal("separate missing language action was silently implemented", w.Code)
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			w = request(t, f.ownerCookie, method, "/settings/api-token/revoke/", "", nil, false, nil)
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" || f.counts(t) != before {
				t.Fatal("revoke action acquired a GET/HEAD mutation", w.Code)
			}
		}
	})
	t.Run("stored-expired-token-metadata-self-only-revoke-and-idempotence", func(t *testing.T) {
		key := f.token(t, f.owner, time.Date(2020, time.January, 15, 10, 0, 0, 0, time.UTC))
		peerKey := f.token(t, f.peer, f.app.Accounts.Config.Now())
		body := accountCenterAppHTML(t, request(t, f.ownerCookie, http.MethodGet, "/settings/", "", nil, false, nil))
		if !strings.Contains(body, `data-account-token-record="true"`) || !strings.Contains(body, "15 Jan 2020") || !strings.Contains(body, `action="/settings/api-token/revoke/"`) || strings.Contains(body, key) || strings.Contains(body, peerKey) {
			t.Fatal("stored token record confused with validity or disclosed a key")
		}
		w := request(t, familyAppCredential{}, http.MethodGet, "/settings/", "", nil, false, func(r *http.Request) { r.Header.Set("Authorization", "Token "+key) })
		if w.Code != http.StatusUnauthorized {
			t.Fatal("expired token acquired authenticated account authority", w.Code)
		}
		validToken := request(t, familyAppCredential{}, http.MethodGet, "/settings/", "", nil, false, func(r *http.Request) { r.Header.Set("Authorization", "Token "+peerKey) })
		validBody := accountCenterAppHTML(t, validToken)
		if !strings.Contains(validBody, f.peer.DisplayName) || strings.Contains(validBody, f.owner.DisplayName) || strings.Contains(validBody, peerKey) {
			t.Fatal("valid explicit token lost current self scope or disclosed credential")
		}
		var sessions int
		if f.app.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_session`).Scan(&sessions) != nil {
			t.Fatal("session count unavailable")
		}
		for i := 0; i < 2; i++ {
			w = request(t, f.ownerCookie, http.MethodPost, "/settings/api-token/revoke/?user_id="+strconv.FormatInt(f.peer.ID, 10), "https://app.example", url.Values{"user_id": {strconv.FormatInt(f.peer.ID, 10)}, "token": {peerKey}}, true, nil)
			own, peer := f.tokenCounts(t)
			var remaining int
			if f.app.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_go_session`).Scan(&remaining) != nil {
				t.Fatal("session count unavailable")
			}
			if w.Code != http.StatusFound || w.Header().Get("Location") != "/settings/" || own != 0 || peer != 1 || remaining != sessions || len(w.Result().Cookies()) != 0 {
				t.Fatal("browser revocation lost self scope/idempotence/session independence", w.Code)
			}
		}
		body = accountCenterAppHTML(t, request(t, f.peerCookie, http.MethodGet, "/settings/", "", nil, false, nil))
		if !strings.Contains(body, `data-account-token-record="true"`) || strings.Contains(body, peerKey) {
			t.Fatal("other account token metadata changed or key disclosed")
		}
	})
	t.Run("revoke-error-preserves-post-context-no-avatar-query-or-notice-consumption", func(t *testing.T) {
		f.token(t, f.owner, f.app.Accounts.Config.Now())
		var schema string
		if f.app.DB.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema) != nil {
			t.Fatal("synthetic schema unavailable")
		}
		_, table := f.fixtureTable(t, "authtoken_token")
		function := pgx.Identifier{schema, "account_center_delete_refusal"}.Sanitize()
		if _, err := f.app.DB.Exec(ctx, `CREATE FUNCTION `+function+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic account revoke refusal'; END; $$; CREATE TRIGGER account_center_delete_refusal BEFORE DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION `+function+`()`); err != nil {
			t.Fatal("synthetic revoke refusal unavailable")
		}
		t.Cleanup(func() {
			if _, err := f.app.DB.Exec(ctx, `DROP TRIGGER account_center_delete_refusal ON `+table+`; DROP FUNCTION `+function+`()`); err != nil {
				t.Error("remove own revoke refusal")
			}
		})
		_, avatar := f.fixtureTable(t, "accounts_signatureavatar")
		if _, err := f.app.DB.Exec(ctx, `ALTER TABLE `+avatar+` RENAME COLUMN generation TO account_center_generation_unavailable`); err != nil {
			t.Fatal("synthetic avatar read refusal unavailable")
		}
		defer func() {
			if _, err := f.app.DB.Exec(ctx, `ALTER TABLE `+avatar+` RENAME COLUMN account_center_generation_unavailable TO generation`); err != nil {
				t.Error("restore own avatar column")
			}
		}()
		f.fixtureTable(t, "accounts_signatureavatar")
		_, avatarErr := accounts.Avatar(ctx, f.app.DB, f.owner.ID)
		accountCenterAppUndefinedColumn(t, avatarErr)
		before := f.counts(t)
		path := "/settings/api-token/revoke/"
		notice := accountCenterAppNotice(f, f.owner, path)
		w := request(t, f.ownerCookie, http.MethodPost, path, "https://app.example", nil, true, func(r *http.Request) { r.AddCookie(notice) })
		body := accountCenterAppHTML(t, w)
		if !strings.Contains(body, `<title>Settings · Social Activities</title>`) || !strings.Contains(body, `href="`+path+`"`) || strings.Count(body, `name="next" value="`+path+`"`) != 2 || !strings.Contains(body, "This action is temporarily unavailable. Please try again.") || !strings.Contains(body, `data-account-token-record="true"`) || strings.Contains(body, `class="avatar nav-avatar"`) || strings.Contains(body, "synthetic account revoke refusal") || strings.Contains(body, "This message had already been hidden") || w.Header().Get("Location") != "" || f.counts(t) != before {
			t.Fatal("POST failure became GET enrichment, disclosed details or changed state")
		}
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == "social_post_notice" {
				t.Fatal("direct error consumed a pending post notice")
			}
		}
		w = request(t, f.ownerCookie, http.MethodGet, "/settings/", "", nil, false, nil)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("GET did not retain its separate common avatar read", w.Code)
		}
	})
	t.Run("failed-settings-reconstruction-remains-service-unavailable", func(t *testing.T) {
		f.token(t, f.owner, f.app.Accounts.Config.Now())
		schema, table := f.fixtureTable(t, "authtoken_token")
		function := pgx.Identifier{schema, "account_center_rebuild_delete_refusal"}.Sanitize()
		if _, err := f.app.DB.Exec(ctx, `CREATE FUNCTION `+function+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic settings rebuild refusal'; END; $$; CREATE TRIGGER account_center_rebuild_delete_refusal BEFORE DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION `+function+`()`); err != nil {
			t.Fatal("fixture-local rebuild delete refusal unavailable")
		}
		t.Cleanup(func() {
			if _, err := f.app.DB.Exec(ctx, `DROP TRIGGER account_center_rebuild_delete_refusal ON `+table+`; DROP FUNCTION `+function+`()`); err != nil {
				t.Error("remove own rebuild delete refusal")
			}
		})
		if _, err := f.app.DB.Exec(ctx, `ALTER TABLE `+table+` RENAME COLUMN created TO account_center_created_unavailable`); err != nil {
			t.Fatal("fixture-local settings read refusal unavailable")
		}
		defer func() {
			if _, err := f.app.DB.Exec(ctx, `ALTER TABLE `+table+` RENAME COLUMN account_center_created_unavailable TO created`); err != nil {
				t.Error("restore own token creation column")
			}
		}()
		f.fixtureTable(t, "authtoken_token")
		// The actual settings producer selects created; keep its local table
		// present and prove that selection fails before the registered request.
		var created time.Time
		readErr := f.app.DB.QueryRow(ctx, `SELECT created FROM authtoken_token WHERE user_id=$1`, f.owner.ID).Scan(&created)
		accountCenterAppUndefinedColumn(t, readErr)
		before := f.counts(t)
		w := request(t, f.ownerCookie, http.MethodPost, "/settings/api-token/revoke/", "https://app.example", nil, true, nil)
		if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Location") != "" || len(w.Result().Cookies()) != 0 || f.counts(t) != before || strings.Contains(w.Body.String(), "account-center-native") || strings.Contains(w.Body.String(), "account_center_created_unavailable") || strings.Contains(w.Body.String(), "synthetic settings rebuild refusal") {
			t.Fatal("failed reconstruction became successful HTML, changed self/session/peer state or leaked detail", w.Code)
		}
	})
	t.Run("current-active-expired-session-and-token-admission", func(t *testing.T) {
		before := f.counts(t)
		defer func() {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true,cohort='adult',age_band='adult',is_identity_verified=true WHERE id=$1`, f.owner.ID); err != nil {
				t.Error("restore own admission fields")
			}
		}()
		for _, cohort := range []string{"unassigned", "teen", "child"} {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET cohort=$2,is_identity_verified=false WHERE id=$1`, f.owner.ID, cohort); err != nil {
				t.Fatal("synthetic pending role fixture unavailable")
			}
			accountCenterAppHTML(t, request(t, f.ownerCookie, http.MethodGet, "/you/", "", nil, false, nil))
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal("synthetic inactive fixture unavailable")
		}
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			path := "/settings/"
			if method == http.MethodPost {
				path = "/settings/api-token/revoke/"
			}
			w := request(t, f.ownerCookie, method, path, "https://app.example", nil, true, nil)
			if w.Code != http.StatusFound || !strings.HasPrefix(w.Header().Get("Location"), "/login/") || strings.Contains(w.Body.String(), "account-center-native") || f.counts(t) != before {
				t.Fatal("inactive session retained page/action authority", w.Code)
			}
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true,cohort='adult' WHERE id=$1`, f.owner.ID); err != nil {
			t.Fatal("restore own active fixture")
		}
		var expires time.Time
		if f.app.DB.QueryRow(ctx, `SELECT expires_at FROM accounts_go_session WHERE user_id=$1`, f.peer.ID).Scan(&expires) != nil {
			t.Fatal("synthetic session unavailable")
		}
		if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_go_session SET expires_at=now()-interval '1 hour' WHERE user_id=$1`, f.peer.ID); err != nil {
			t.Fatal("synthetic session expiry unavailable")
		}
		defer func() {
			if _, err := f.app.DB.Exec(ctx, `UPDATE accounts_go_session SET expires_at=$2 WHERE user_id=$1`, f.peer.ID, expires); err != nil {
				t.Error("restore own peer session expiry")
			}
		}()
		w := request(t, f.peerCookie, http.MethodGet, "/settings/", "", nil, false, nil)
		if w.Code != http.StatusFound || strings.Contains(w.Body.String(), "account-center-native") {
			t.Fatal("expired session kept page authority", w.Code)
		}
		w = request(t, f.ownerCookie, http.MethodGet, "/settings/", "", nil, false, func(r *http.Request) { r.Header.Set("Authorization", "Token invalid") })
		if w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "account-center-native") {
			t.Fatal("invalid explicit token fell through to session authority", w.Code)
		}
	})
	t.Run("export-head-preview-and-spa-boundaries", func(t *testing.T) {
		before := f.counts(t)
		w := request(t, f.ownerCookie, http.MethodHead, "/account/export/", "", nil, false, nil)
		if w.Code != http.StatusForbidden || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Disposition") != "" {
			t.Fatal("export HEAD acquired download authority", w.Code)
		}
		w = request(t, f.ownerCookie, http.MethodGet, "/account/export/?user_id="+strconv.FormatInt(f.peer.ID, 10), "", nil, false, nil)
		var exported struct {
			Profile struct {
				PublicID string `json:"public_id"`
			} `json:"profile"`
			Access map[string]any `json:"api_access"`
		}
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Disposition"), f.owner.PublicID) || json.Unmarshal(w.Body.Bytes(), &exported) != nil || exported.Profile.PublicID != f.owner.PublicID || len(exported.Access) != 2 {
			t.Fatal("self export transport or metadata boundary changed", w.Code)
		}
		w = request(t, f.ownerCookie, http.MethodGet, "/account/delete/?user_id="+strconv.FormatInt(f.peer.ID, 10), "", nil, false, nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "What gets permanently deleted") || f.counts(t) != before {
			t.Fatal("readonly erasure preview changed state or scope", w.Code)
		}
		oldSPA := f.app.Web.Config.SPA
		f.app.Web.Config.SPA = true
		defer func() { f.app.Web.Config.SPA = oldSPA }()
		for _, path := range []string{"/you/", "/settings/"} {
			w = request(t, f.ownerCookie, http.MethodGet, path+"?_data=1", "", nil, false, nil)
			var payload struct {
				Route string                     `json:"route"`
				Data  map[string]json.RawMessage `json:"data"`
			}
			if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &payload) != nil || payload.Route != strings.Trim(path, "/") || len(payload.Data) == 0 || strings.Contains(w.Body.String(), "account-center-native") {
				t.Fatal("private SPA data branch changed", w.Code)
			}
			w = request(t, f.ownerCookie, http.MethodGet, path, "", nil, false, nil)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `id="spa-bootstrap"`) || strings.Contains(w.Body.String(), "account-center-native") {
				t.Fatal("explicit SPA shell branch was intercepted", w.Code)
			}
		}
		if f.counts(t) != before {
			t.Fatal("export/preview/SPA mutated account state")
		}
	})
	t.Run("logout-refusal-then-existing-cookie-clear-and-session-delete", func(t *testing.T) {
		before := f.counts(t)
		getLogout := request(t, f.ownerCookie, http.MethodGet, "/logout/", "", nil, false, nil)
		if getLogout.Code != http.StatusMethodNotAllowed || f.counts(t) != before {
			t.Fatal("logout acquired a GET mutation", getLogout.Code)
		}
		w := request(t, f.ownerCookie, http.MethodPost, "/logout/", "https://app.example", nil, false, nil)
		if w.Code != http.StatusForbidden || f.counts(t) != before {
			t.Fatal("logout bypassed shared CSRF", w.Code)
		}
		var schema string
		if f.app.DB.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema) != nil {
			t.Fatal("synthetic schema unavailable")
		}
		function := pgx.Identifier{schema, "account_center_logout_refusal"}.Sanitize()
		table := pgx.Identifier{schema, "accounts_go_session"}.Sanitize()
		if _, err := f.app.DB.Exec(ctx, `CREATE FUNCTION `+function+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic account logout refusal'; END; $$; CREATE TRIGGER account_center_logout_refusal BEFORE DELETE ON `+table+` FOR EACH ROW EXECUTE FUNCTION `+function+`()`); err != nil {
			t.Fatal("synthetic logout refusal unavailable")
		}
		w = request(t, f.ownerCookie, http.MethodPost, "/logout/", "https://app.example", nil, true, nil)
		if _, err := f.app.DB.Exec(ctx, `DROP TRIGGER account_center_logout_refusal ON `+table+`; DROP FUNCTION `+function+`()`); err != nil {
			t.Fatal("remove own logout refusal")
		}
		if w.Code != http.StatusServiceUnavailable || len(w.Result().Cookies()) != 0 || w.Header().Get("Location") != "" || f.counts(t) != before {
			t.Fatal("failed logout cleared cookies/session or reported success", w.Code)
		}
		w = request(t, f.ownerCookie, http.MethodPost, "/logout/", "https://app.example", nil, true, nil)
		cleared := map[string]bool{}
		for _, cookie := range w.Result().Cookies() {
			if cookie.MaxAge < 0 && cookie.Value == "" {
				cleared[cookie.Name] = true
			}
		}
		var own, peer int
		if f.app.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_go_session WHERE user_id=$1),(SELECT count(*) FROM accounts_go_session WHERE user_id=$2)`, f.owner.ID, f.peer.ID).Scan(&own, &peer) != nil {
			t.Fatal("synthetic session counts unavailable")
		}
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" || !cleared["sessionid"] || !cleared["csrftoken"] || own != 0 || peer != 1 {
			t.Fatal("successful logout lost cookie/session/self scope", w.Code)
		}
		w = request(t, f.ownerCookie, http.MethodGet, "/settings/", "", nil, false, nil)
		if w.Code != http.StatusFound || strings.Contains(w.Body.String(), "account-center-native") {
			t.Fatal("revoked cookie retained account page authority", w.Code)
		}
	})
}
