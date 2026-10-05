package safety

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

const restrictedCasePassword = "synthetic-restricted-case-password"

func restrictedCaseFixture(t *testing.T) (*Service, platform.Actor, int64, *http.ServeMux) {
	t.Helper()
	ctx := context.Background()
	db := testdb.New(t, *safetyTestDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	store := accounts.NewStore(db)
	auth, err := authcore.New(authcore.Config{PublicURL: "https://fixture.local"}, store)
	if err != nil {
		t.Fatal(err)
	}
	acc := accounts.New(db, auth, "synthetic-restricted-case-binding", accounts.Config{})
	if err := acc.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	s := New(db, Config{Accounts: acc})
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	subject := testdb.Actor(t, db, "restricted-case-subject", "adult")
	mod := testdb.Actor(t, db, "restricted-case-private-moderator", "adult")
	mod.Role = "moderator"
	if _, err := db.Exec(ctx, `UPDATE accounts_user SET role='moderator',is_staff=true WHERE id=$1`, mod.ID); err != nil {
		t.Fatal(err)
	}
	hash, err := authcore.HashPassword(ctx, restrictedCasePassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE accounts_user SET password=$2 WHERE id=$1`, subject.ID, hash); err != nil {
		t.Fatal(err)
	}
	target, err := s.ResolveTarget(ctx, db, "accounts", "user", subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	action, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "suspend", Reason: "harassment", SuspendDays: 1, Notes: "private restriction case moderator note"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Register(mux)
	return s, subject, action, mux
}

func restrictedCasePost(t *testing.T, mux *http.ServeMux, address string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	seed := httptest.NewRecorder()
	mux.ServeHTTP(seed, httptest.NewRequest("GET", "https://fixture.local/account/restricted/", nil))
	var csrf *http.Cookie
	for _, cookie := range seed.Result().Cookies() {
		if cookie.Name == "csrftoken" {
			csrf = cookie
		}
	}
	if seed.Code != 200 || csrf == nil {
		t.Fatal("pre-auth CSRF unavailable", seed.Code)
	}
	copy := url.Values{}
	for key, values := range values {
		copy[key] = append([]string{}, values...)
	}
	copy.Set("csrf_token", csrf.Value)
	r := httptest.NewRequest("POST", "https://fixture.local/account/restricted/", strings.NewReader(copy.Encode()))
	r.RemoteAddr = address
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "https://fixture.local")
	r.AddCookie(csrf)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func restrictedCaseNoSession(t *testing.T, s *Service, subject int64, w *httptest.ResponseRecorder) {
	t.Helper()
	var sessions int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_go_session WHERE user_id=$1`, subject).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("credential proof minted a participation session", err, sessions)
	}
	for _, cookie := range w.Result().Cookies() {
		if cookie.Name == "sessionid" {
			t.Fatal("credential proof set an auth cookie")
		}
	}
}

func TestCasePort2RestrictedCredentialHTMLStatesNoSessionAndOwnedAppeal(t *testing.T) {
	s, subject, action, mux := restrictedCaseFixture(t)
	ctx := context.Background()
	t.Run("credential_form_wrong_and_valid_proof", func(t *testing.T) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "https://fixture.local/account/restricted/", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `name="password"`) {
			t.Fatal("pre-auth credential form", w.Code)
		}
		w = restrictedCasePost(t, mux, "198.51.100.20:1001", url.Values{"username": {subject.Username}, "password": {"WRONG"}})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "verify those details") || strings.Contains(w.Body.String(), "Harassment") {
			t.Fatal("failed proof disclosed statement", w.Code)
		}
		w = restrictedCasePost(t, mux, "198.51.100.20:1002", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Harassment") || !strings.Contains(w.Body.String(), `name="appeal_token"`) {
			t.Fatal("verified restriction statement", w.Code)
		}
		for _, hidden := range []string{"restricted-case-private-moderator", "private restriction case moderator note"} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Fatal("statement leaked private actor/note")
			}
		}
		restrictedCaseNoSession(t, s, subject.ID, w)
	})
	t.Run("token_files_only_its_owned_appeal", func(t *testing.T) {
		proof := restrictedCasePost(t, mux, "198.51.100.21:2001", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
		match := regexp.MustCompile(`name="appeal_token" value="([^"]+)"`).FindStringSubmatch(proof.Body.String())
		if len(match) != 2 {
			t.Fatal("no owned appeal capability")
		}
		w := restrictedCasePost(t, mux, "198.51.100.21:2002", url.Values{"appeal_token": {match[1]}, "statement": {"I believe this was a mistake"}})
		if w.Code != 200 || !strings.Contains(strings.ToLower(w.Body.String()), "received") {
			t.Fatal("token appeal was not received", w.Code)
		}
		var appellant int64
		var statement string
		if err := s.DB.QueryRow(ctx, `SELECT appellant_id,statement FROM safety_moderationappeal WHERE action_id=$1`, action).Scan(&appellant, &statement); err != nil || appellant != subject.ID || statement != "I believe this was a mistake" {
			t.Fatal("capability did not bind the affected user/action", err)
		}
		restrictedCaseNoSession(t, s, subject.ID, w)
		w = restrictedCasePost(t, mux, "198.51.100.21:2003", url.Values{"appeal_token": {match[1]}, "statement": {"Replay"}})
		if strings.Contains(strings.ToLower(w.Body.String()), "appeal was received") {
			t.Fatal("single-purpose capability replay accepted")
		}
	})
	t.Run("active_and_self_deactivated_are_distinct_after_valid_credentials", func(t *testing.T) {
		if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true WHERE id=$1`, subject.ID); err != nil {
			t.Fatal(err)
		}
		w := restrictedCasePost(t, mux, "198.51.100.22:3001", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
		if w.Code != 200 || !strings.Contains(strings.ToLower(w.Body.String()), "active") || strings.Contains(w.Body.String(), `name="appeal_token"`) || strings.Contains(w.Body.String(), "Harassment") {
			t.Fatal("active verified account disclosed restriction")
		}
		if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, subject.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.DB.Exec(ctx, `UPDATE safety_moderationaction SET lifted_at=now() WHERE target_id=$1 AND target_type_id=(SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user')`, subject.ID); err != nil {
			t.Fatal(err)
		}
		w = restrictedCasePost(t, mux, "198.51.100.22:3002", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
		if w.Code != 200 || !strings.Contains(strings.ToLower(w.Body.String()), "no moderation decision") || strings.Contains(w.Body.String(), `name="appeal_token"`) {
			t.Fatal("self-deactivated verified account disclosed restriction")
		}
		restrictedCaseNoSession(t, s, subject.ID, w)
	})
}

func TestCasePort2RestrictedAdmissionNormalizesPortsSharesNamespaceAndPinsWindow(t *testing.T) {
	s, subject, _, mux := restrictedCaseFixture(t)
	ctx := context.Background()
	now := time.Now()
	s.Config.Now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if _, _, err := s.RestrictionAccess(ctx, subject.Username, "incorrect", fmt.Sprintf("198.51.100.30:%d", 1000+i)); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("failed credential admission", i, err)
		}
	}
	w := restrictedCasePost(t, mux, "198.51.100.30:9999", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Too many attempts") || strings.Contains(w.Body.String(), "Harassment") {
		t.Fatal("source-port rotation bypassed proof admission")
	}
	s.Config.Accounts.Config.Now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		if valid, err := s.Config.Accounts.LoginFailures(ctx, strings.ToUpper(subject.Username), fmt.Sprintf("198.51.100.31:%d", 4000+i), func(context.Context) (bool, error) { return false, nil }); err != nil || valid {
			t.Fatal("normal authentication shared failure namespace setup", err)
		}
	}
	if _, _, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.31:9998"); !errors.Is(err, ErrRate) {
		t.Fatal("pre-auth remedy bypassed normal auth namespace", err)
	}
	now = now.Add(61 * time.Second)
	if _, _, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.30:9997"); !errors.Is(err, ErrRate) {
		t.Fatal("source fifteen-minute failure window cleared after one minute", err)
	}
	now = now.Add(15 * time.Minute)
	if _, _, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.30:9996"); err != nil {
		t.Fatal("source failure window did not expire", err)
	}
	for i := 0; i < 9; i++ {
		if _, _, err := s.RestrictionAccess(ctx, subject.Username, "incorrect", "198.51.100.32:6000"); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("success-clear setup", err)
		}
	}
	if _, _, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.32:6001"); err != nil {
		t.Fatal("valid own proof did not clear failed pair", err)
	}
	for i := 0; i < 9; i++ {
		if _, _, err := s.RestrictionAccess(ctx, subject.Username, "incorrect", "198.51.100.32:6002"); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("old failures survived successful proof", err)
		}
	}
}

func TestCasePort2RestrictedLargeActionBindingAndStatementPublication(t *testing.T) {
	s, subject, action, mux := restrictedCaseFixture(t)
	ctx := context.Background()
	const large int64 = 9007199254740993
	if _, err := s.DB.Exec(ctx, `UPDATE safety_moderationaction SET id=$2,created_at='2030-01-02T12:00:00Z',expires_at='2030-01-03T12:00:00Z' WHERE id=$1`, action, large); err != nil {
		t.Fatal(err)
	}
	s.Config.Now = func() time.Time { return time.Date(2030, 1, 2, 13, 0, 0, 0, time.UTC) }
	statement, token, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.40:3000")
	if err != nil || token == "" {
		t.Fatal("large-ID capability mint failed", err)
	}
	if fmt.Sprint(statement["action_id"]) != strconv.FormatInt(large, 10) {
		t.Fatal("large action rounded in statement")
	}
	var got int64
	if err := s.DB.QueryRow(ctx, `SELECT action_id FROM safety_go_restrictioncapability WHERE token_hash=$1`, capHash(token)).Scan(&got); err != nil || got != large {
		t.Fatal("large capability target rounded", err, got)
	}
	w := restrictedCasePost(t, mux, "198.51.100.40:3001", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
	if !strings.Contains(w.Body.String(), "2 Jan 2030, 14:00") || !strings.Contains(w.Body.String(), "3 Jan 2030, 14:00") || !strings.Contains(w.Body.String(), "due to lift") {
		t.Fatal("statement dates missing")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE safety_moderationaction SET action='ban',expires_at=NULL WHERE id=$1`, large); err != nil {
		t.Fatal(err)
	}
	w = restrictedCasePost(t, mux, "198.51.100.40:3002", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
	if !strings.Contains(w.Body.String(), "permanent restriction") {
		t.Fatal("lifetime statement missing")
	}
	if _, err := s.FileAppeal(ctx, platform.Actor{ID: subject.ID}, large, "Statement status publication"); err != nil {
		t.Fatal(err)
	}
	w = restrictedCasePost(t, mux, "198.51.100.40:3003", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
	if !strings.Contains(w.Body.String(), "Pending review") || strings.Contains(w.Body.String(), `name="appeal_token"`) {
		t.Fatal("appeal status missing or contest offered twice")
	}
}

func TestCasePort2RestrictedInvalidStatementPreservesVisibleRemedy(t *testing.T) {
	s, subject, action, mux := restrictedCaseFixture(t)
	proof := restrictedCasePost(t, mux, "198.51.100.50:1001", url.Values{"username": {subject.Username}, "password": {restrictedCasePassword}})
	parseToken := func(body string) string {
		t.Helper()
		match := regexp.MustCompile(`name="appeal_token" value="([^"]+)"`).FindStringSubmatch(body)
		if len(match) != 2 {
			t.Fatal("verified remedy disappeared after a correctable input error")
		}
		return match[1]
	}
	invalid := restrictedCasePost(t, mux, "198.51.100.50:1002", url.Values{"appeal_token": {parseToken(proof.Body.String())}, "statement": {"   "}})
	if invalid.Code != 200 || !strings.Contains(invalid.Body.String(), "Harassment") {
		t.Fatal("invalid appeal failed to rerender the owned current statement")
	}
	var count int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM safety_moderationappeal WHERE action_id=$1`, action).Scan(&count); err != nil || count != 0 {
		t.Fatal("invalid appeal created a decision record", err)
	}
	valid := restrictedCasePost(t, mux, "198.51.100.50:1003", url.Values{"appeal_token": {parseToken(invalid.Body.String())}, "statement": {"Corrected contest statement"}})
	if valid.Code != 200 || !strings.Contains(strings.ToLower(valid.Body.String()), "received") {
		t.Fatal("corrected appeal required another password proof")
	}
	restrictedCaseNoSession(t, s, subject.ID, valid)
}
