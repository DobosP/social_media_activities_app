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

func TestSourceLoginAssembledReplicasBindCanonicalCredentialAndFailurePair(t *testing.T) {
	db := testdb.New(t, *appDSN, nil)
	ctx := context.Background()
	cfg := integrationConfig(t)
	cfg.Accounts.LoginFailureLimit = 4
	cfg.Accounts.LoginFailureWindow = 15 * time.Minute
	cfg.TrustedProxyCIDRs = []string{"10.0.0.0/8"}
	cfg.ProxyHops = 1
	a, err := New(ctx, db, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := New(ctx, db, cfg, true)
	if err != nil {
		t.Fatal(err)
	}
	const password = "synthetic-binding-password-only"
	hash, err := authcore.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.CreatePasswordUser(ctx, authcore.User{Username: "binding-login-user"}, hash); err != nil {
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
		t.Fatal("assembled login CSRF", seed.Code)
	}
	post := func(server http.Handler, path, kind, body, remote string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("POST", "https://app.example"+path, strings.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Content-Type", kind)
		r.Header.Set("Origin", "https://app.example")
		r.Header.Set("X-CSRFToken", csrf.Value)
		r.AddCookie(csrf)
		if strings.HasPrefix(remote, "10.") {
			r.Header.Set("X-Forwarded-For", "192.0.2.90")
		}
		w := httptest.NewRecorder()
		server.ServeHTTP(w, r)
		return w
	}
	for i, name := range []string{" binding-login-user", "binding-login-user ", "\t binding-login-user ", "\u2003binding-login-user\u2003"} {
		server := http.Handler(a)
		remote := "192.0.2.90:30001"
		if i%2 == 1 {
			server = b
			remote = "10.1.1.1:40001"
		}
		body := url.Values{"username": {name}, "password": {"wrong-synthetic"}, "csrfmiddlewaretoken": {csrf.Value}}.Encode()
		w := post(server, "/login/", "application/x-www-form-urlencoded", body, remote)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "correct username and password") {
			t.Fatal("assembled padding source failure", w.Code)
		}
	}
	for _, body := range []string{
		`{"Username":"binding-login-user","password":"wrong-synthetic"}`,
		`{"username":"fresh-bucket","Username":"binding-login-user","password":"wrong-synthetic"}`,
		`{"username":"fresh-bucket2","userNAME":"binding-login-user","password":"wrong-synthetic"}`,
		`{"username":"fresh-bucket3","username":"binding-login-user","password":"wrong-synthetic"}`,
	} {
		if w := post(b, "/api/auth/login", "application/json", body, "10.1.1.2:40002"); w.Code != 400 {
			t.Fatal("assembled alias body authenticated", w.Code)
		}
	}
	w := post(b, "/api/auth/login", "application/json", `{"username":" binding-login-user ","password":"`+password+`"}`, "10.1.1.3:40003")
	if w.Code != 429 {
		t.Fatal("assembled alias/padding/replica escaped source failure pair", w.Code)
	}
	var pairs, failures, pending, sessions int
	if err := db.QueryRow(ctx, `SELECT count(*),coalesce(sum(failures),0) FROM accounts_go_login_failure`).Scan(&pairs, &failures); err != nil || pairs != 1 || failures != 4 {
		t.Fatal("assembled canonical counter", pairs, failures, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM accounts_go_login_reservation`).Scan(&pending); err != nil || pending != 0 {
		t.Fatal("assembled slot leak", pending, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM accounts_go_session`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("assembled bypass created session", sessions, err)
	}
}
