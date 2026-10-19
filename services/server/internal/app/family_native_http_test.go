package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// No WithActor or constructed session: every protected request goes through the
// actual assembled App's password login, cookie reload and same-origin CSRF.
type familyAppCredential struct{ Session, CSRF *http.Cookie }

func familyAppLogin(t *testing.T, a *App, username, password string) familyAppCredential {
	t.Helper()
	seed := httptest.NewRecorder()
	a.ServeHTTP(seed, httptest.NewRequest("GET", "https://app.example/login/", nil))
	var csrf *http.Cookie
	for _, cookie := range seed.Result().Cookies() {
		if cookie.Name == "csrftoken" {
			csrf = cookie
		}
	}
	if seed.Code != http.StatusOK || csrf == nil {
		t.Fatal("real login CSRF unavailable", seed.Code)
	}
	raw, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "https://app.example/api/auth/login", strings.NewReader(string(raw)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("X-CSRFToken", csrf.Value)
	r.AddCookie(csrf)
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatal("real password login failed", w.Code)
	}
	credentials := familyAppCredential{}
	for _, cookie := range w.Result().Cookies() {
		switch cookie.Name {
		case "sessionid":
			credentials.Session = cookie
		case "csrftoken":
			credentials.CSRF = cookie
		}
	}
	if credentials.Session == nil || credentials.CSRF == nil {
		t.Fatal("real rotated login cookies absent")
	}
	return credentials
}

func familyAppRequest(t *testing.T, a *App, credentials familyAppCredential, method, path, origin string, values url.Values, proof bool) *httptest.ResponseRecorder {
	t.Helper()
	if values == nil {
		values = url.Values{}
	}
	copied := url.Values{}
	for key, items := range values {
		copied[key] = append([]string(nil), items...)
	}
	if proof && credentials.CSRF != nil {
		copied.Set("csrfmiddlewaretoken", credentials.CSRF.Value)
	}
	r := httptest.NewRequest(method, "https://app.example"+path, strings.NewReader(copied.Encode()))
	if method != "GET" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
	}
	if credentials.Session != nil {
		r.AddCookie(credentials.Session)
	}
	if credentials.CSRF != nil {
		r.AddCookie(credentials.CSRF)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func familyAppHTML(t *testing.T, a *App, credentials familyAppCredential, path string) string {
	t.Helper()
	w := familyAppRequest(t, a, credentials, "GET", path, "", nil, false)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		t.Fatal("registered native family GET failed", path, w.Code)
	}
	if w.Header().Get("Cache-Control") != "private, no-cache" || !strings.Contains(w.Header().Get("Vary"), "Cookie") || w.Header().Get("X-Social-Runtime") != "go" || !strings.Contains(w.Header().Get("Content-Security-Policy-Report-Only"), "frame-ancestors 'none'") || w.Header().Get("Reporting-Endpoints") == "" {
		t.Fatal("native family response lost existing common headers", path)
	}
	body := w.Body.String()
	if strings.Contains(body, "<script") || !strings.Contains(body, `<meta name="robots" content="noindex, nofollow">`) {
		t.Fatal("family GET did not use finite native template", path)
	}
	return body
}

func TestNativeFamilyAssembledHTTPScopesCSRFAndCurrentAuthority(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	ctx := context.Background()
	cfg := integrationConfig(t)
	cfg.Accounts.AllowMinorOnboarding = true
	a, err := New(ctx, db, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	guardian := testdb.Actor(t, db, "generated-family-http-guardian", "adult")
	co := testdb.Actor(t, db, "generated-family-http-co-guardian", "adult")
	stranger := testdb.Actor(t, db, "generated-family-http-stranger", "adult")
	ward := testdb.Actor(t, db, "generated-family-http-child", "child")
	foreign := testdb.Actor(t, db, "private-unrelated-family-child", "child")
	invitee := testdb.Actor(t, db, "generated-family-http-invitee", "child")
	password := "synthetic-family-http-password-only"
	passwordHash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []platform.Actor{guardian, co, stranger, ward, invitee} {
		if _, err = db.Exec(ctx, `UPDATE accounts_user SET password=$2 WHERE id=$1`, actor.ID, passwordHash); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]int64{{guardian.ID, ward.ID}, {co.ID, ward.ID}, {co.ID, foreign.ID}} {
		if _, err = db.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',NULL,now(),now())`, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `UPDATE accounts_parentalconsent SET guardian_identifier=$2 WHERE minor_id=$1`, ward.ID, guardian.PublicID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,$2,'active','',now(),now()+interval '1 year',NULL,'',now(),now())`, ward.ID, co.PublicID); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO taxonomy_activitycategory(slug,name,parent_id,description,created_at,updated_at) VALUES('generated-family-http-sport','Generated family sport',NULL,'',now(),now())`); err != nil {
		t.Fatal(err)
	}
	g := familyAppLogin(t, a, guardian.Username, password)
	c := familyAppLogin(t, a, co.Username, password)
	outsider := familyAppLogin(t, a, stranger.Username, password)
	child := familyAppLogin(t, a, ward.Username, password)
	limits := fmt.Sprintf("/wards/%d/limits/", ward.ID)
	topics := fmt.Sprintf("/wards/%d/topics/", ward.ID)
	revoke := fmt.Sprintf("/wards/%d/revoke/", ward.ID)
	count := func(t *testing.T, sql string, args ...any) int {
		t.Helper()
		var n int
		if err := db.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	post := func(t *testing.T, credentials familyAppCredential, path string, values url.Values) *httptest.ResponseRecorder {
		t.Helper()
		w := familyAppRequest(t, a, credentials, "POST", path, "https://app.example", values, true)
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/wards/" {
			t.Fatal("family workflow redirect", path, w.Code)
		}
		return w
	}
	t.Run("scoped-native-get-and-readonly-ward", func(t *testing.T) {
		body := familyAppHTML(t, a, g, "/wards/?ward_pk="+fmt.Sprint(foreign.ID))
		for _, want := range []string{ward.Username, limits, topics, revoke, `name="allowed_weekdays"`, `name="allowed_categories"`, `name="topics"`, "I've arrived"} {
			if !strings.Contains(body, want) {
				t.Fatal("own current native control absent", want)
			}
		}
		if strings.Contains(body, foreign.Username) || strings.Contains(body, fmt.Sprintf("/wards/%d/", foreign.ID)) {
			t.Fatal("guardian GET disclosed other household")
		}
		body = familyAppHTML(t, a, outsider, "/wards/")
		if strings.Contains(body, ward.Username) || strings.Contains(body, limits) {
			t.Fatal("unlinked active adult received ward detail")
		}
		body = familyAppHTML(t, a, child, "/guardianship/?user_id="+fmt.Sprint(stranger.ID))
		if !strings.Contains(body, guardian.Username) || !strings.Contains(body, co.Username) || strings.Contains(body, stranger.Username) || strings.Contains(body, "/revoke/") || strings.Contains(body, "/limits/") || strings.Contains(body, `name="ward_username"`) {
			t.Fatal("ward explanation scope or readonly contract")
		}
		anon := familyAppRequest(t, a, familyAppCredential{}, "GET", "/wards/", "", nil, false)
		if anon.Code != http.StatusFound || !strings.HasPrefix(anon.Header().Get("Location"), "/login/?next=") {
			t.Fatal("anonymous family page admitted", anon.Code)
		}
	})
	t.Run("actual-csrf-and-origin-refusals-write-nothing", func(t *testing.T) {
		for _, probe := range []struct {
			proof  bool
			origin string
		}{{false, "https://app.example"}, {true, "https://outside.invalid"}} {
			w := familyAppRequest(t, a, g, "POST", limits, probe.origin, url.Values{"max_open_joins": {"2"}}, probe.proof)
			if w.Code != http.StatusForbidden {
				t.Fatal("actual family CSRF/origin refusal missing", w.Code)
			}
		}
		if count(t, `SELECT count(*) FROM accounts_guardianguardrail`) != 0 || count(t, `SELECT count(*) FROM safety_auditlog WHERE event='guardian.guardrail_set'`) != 0 {
			t.Fatal("rejected browser mutation wrote guardrails/audit")
		}
		post(t, outsider, limits, url.Values{"max_open_joins": {"2"}})
		if count(t, `SELECT count(*) FROM accounts_guardianguardrail`) != 0 {
			t.Fatal("unlinked current session changed ward limits")
		}
	})
	t.Run("existing-four-form-workflows-and-exact-repeated-fields", func(t *testing.T) {
		post(t, g, limits, url.Values{"supervised_only": {"on"}, "earliest_start_hour": {"8"}, "latest_start_hour": {"20"}, "max_open_joins": {"3"}, "allowed_weekdays": {"1", "3"}, "allowed_categories": {"generated-family-http-sport"}})
		var days string
		var earliest, latest, maximum int
		var supervised bool
		var categories []string
		if err := db.QueryRow(ctx, `SELECT r.allowed_weekdays,r.earliest_start_hour,r.latest_start_hour,r.max_open_joins,r.supervised_only,r.allowed_categories FROM accounts_guardianguardrail r JOIN accounts_guardianrelationship g ON g.id=r.relationship_id WHERE g.guardian_id=$1 AND g.ward_id=$2`, guardian.ID, ward.ID).Scan(&days, &earliest, &latest, &maximum, &supervised, &categories); err != nil || days != "13" || earliest != 8 || latest != 20 || maximum != 3 || !supervised || len(categories) != 1 || categories[0] != "generated-family-http-sport" {
			t.Fatal("exact submitted hard limits not stored", err)
		}
		post(t, g, topics, url.Values{"topics": {"generated-family-http-sport"}})
		chosen, err := a.Recommendations.TopicSlugs(ctx, ward)
		if err != nil || len(chosen) != 1 || chosen[0] != "generated-family-http-sport" {
			t.Fatal("exact submitted soft topics not stored", err)
		}
		body := familyAppHTML(t, a, g, "/wards/")
		if !strings.Contains(body, `value="1" checked`) || !strings.Contains(body, `value="3" checked`) || !strings.Contains(body, `name="topics" value="generated-family-http-sport" checked`) {
			t.Fatal("stored repeated values not reflected in current native form")
		}
		unknown := post(t, g, "/wards/invite/", url.Values{"ward_username": {"not-a-generated-account"}, "relationship": {"parent"}})
		known := post(t, g, "/wards/invite/", url.Values{"ward_username": {invitee.Username}, "relationship": {"parent"}})
		if unknown.Body.String() != known.Body.String() {
			t.Fatal("browser invite acknowledgement enumerates account existence")
		}
		if count(t, `SELECT count(*) FROM accounts_guardianlinkinvite WHERE guardian_id=$1 AND ward_id=$2 AND status='pending'`, guardian.ID, invitee.ID) != 1 || count(t, `SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active'`, guardian.ID, invitee.ID) != 0 {
			t.Fatal("invite was not pending-only, or bypassed ward acceptance")
		}
		post(t, g, revoke, nil)
		if count(t, `SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='revoked'`, guardian.ID, ward.ID) != 1 || count(t, `SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active'`, co.ID, ward.ID) != 1 || count(t, `SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=$1 AND guardian_identifier=$2 AND status='active'`, ward.ID, co.PublicID) != 1 {
			t.Fatal("own revoke changed co-guardian authority/consent")
		}
		if count(t, `SELECT count(*) FROM safety_auditlog WHERE event='guardian.revoked' AND actor_ref=$1`, guardian.ID) != 1 {
			t.Fatal("existing atomic revocation audit absent")
		}
		if strings.Contains(familyAppHTML(t, a, g, "/wards/"), ward.Username) || !strings.Contains(familyAppHTML(t, a, c, "/wards/"), ward.Username) {
			t.Fatal("current relationship change not reflected per household")
		}
	})
	t.Run("existing-session-reloads-revoked-inactive-and-adult-ward-authority", func(t *testing.T) {
		post(t, g, limits, url.Values{"max_open_joins": {"1"}})
		var maximum int
		if err := db.QueryRow(ctx, `SELECT r.max_open_joins FROM accounts_guardianguardrail r JOIN accounts_guardianrelationship g ON g.id=r.relationship_id WHERE g.guardian_id=$1 AND g.ward_id=$2`, guardian.ID, ward.ID).Scan(&maximum); err != nil || maximum != 3 {
			t.Fatal("revoked session changed retained own limits", err)
		}
		if _, err := db.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, guardian.ID); err != nil {
			t.Fatal(err)
		}
		inactive := familyAppRequest(t, a, g, "GET", "/wards/", "", nil, false)
		if inactive.Code != http.StatusFound || !strings.HasPrefix(inactive.Header().Get("Location"), "/login/?next=") {
			t.Fatal("inactive credential remained authenticated", inactive.Code)
		}
		if _, err := db.Exec(ctx, `UPDATE accounts_user SET cohort='adult',age_band='adult' WHERE id=$1`, ward.ID); err != nil {
			t.Fatal(err)
		}
		adultBody := familyAppHTML(t, a, c, "/wards/")
		if strings.Contains(adultBody, limits) || strings.Contains(adultBody, topics) || !strings.Contains(adultBody, revoke) {
			t.Fatal("current adult ward view retained stale child controls or removed own relationship revoke")
		}
		w := familyAppRequest(t, a, c, "POST", limits, "https://app.example", url.Values{"max_open_joins": {"1"}}, true)
		if w.Code != http.StatusOK {
			t.Fatal("current adult ward guardrail refusal did not render current own panel", w.Code)
		}
		if count(t, `SELECT count(*) FROM accounts_guardianguardrail r JOIN accounts_guardianrelationship g ON g.id=r.relationship_id WHERE g.guardian_id=$1 AND g.ward_id=$2`, co.ID, ward.ID) != 0 {
			t.Fatal("cohort change did not stop stale child control")
		}
		post(t, c, topics, url.Values{"topics": {"generated-family-http-sport"}})
		chosen, err := a.Recommendations.TopicSlugs(ctx, ward)
		if err != nil || len(chosen) != 1 || chosen[0] != "generated-family-http-sport" {
			t.Fatal("current adult ward soft steering mutated", err)
		}
	})
}
