package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestSourceLoginAssembledAppNormalizesTrustedPeerAndSharesFailures(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	ctx := context.Background()
	cfg := integrationConfig(t)
	cfg.Accounts.LoginFailureLimit = 3
	cfg.Accounts.LoginFailureWindow = 15 * time.Minute
	cfg.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	cfg.ProxyHops = 1
	a, err := New(ctx, db, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	password := "synthetic-login-password-only"
	hash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.Store.CreatePasswordUser(ctx, authcore.User{Username: "source-login-user"}, hash); err != nil {
		t.Fatal(err)
	}
	seed := httptest.NewRecorder()
	a.ServeHTTP(seed, httptest.NewRequest("GET", "https://app.example/login/", nil))
	var csrf *http.Cookie
	for _, c := range seed.Result().Cookies() {
		if c.Name == "csrftoken" {
			csrf = c
		}
	}
	if seed.Code != 200 || csrf == nil {
		t.Fatal("registeredlogin CSRF", seed.Code)
	}
	post := func(path, password, remote, forwarded string) *httptest.ResponseRecorder {
		t.Helper()
		values := url.Values{"username": {"source-login-user"}, "password": {password}, "csrfmiddlewaretoken": {csrf.Value}}
		r := httptest.NewRequest("POST", "https://app.example"+path, strings.NewReader(values.Encode()))
		r.RemoteAddr = remote
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Origin", "https://app.example")
		r.AddCookie(csrf)
		if forwarded != "" {
			r.Header.Set("X-Forwarded-For", forwarded)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	for _, peer := range []struct{ address, xff string }{{"192.0.2.40:30001", "203.0.113.1"}, {"192.0.2.40:30002", "203.0.113.2"}, {"10.1.1.1:31001", "192.0.2.40"}} {
		w := post("/login/", "wrong", peer.address, peer.xff)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "correct username and password") {
			t.Fatal("source login failure did not render form", w.Code)
		}
	}
	w := post("/login/", password, "10.1.1.2:32001", "192.0.2.40")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Too many failed login attempts") {
		t.Fatal("trusted proxy/newports escaped shared username-IP failure bucket", w.Code)
	}
	var sessions int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM accounts_go_session`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("locked login created session", sessions, err)
	}
}
