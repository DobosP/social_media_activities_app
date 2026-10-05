package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func tokenLogin(s *Service, name, password, peer string) *httptest.ResponseRecorder {
	body, _ := json.Marshal(map[string]string{"username": name, "password": password})
	r := httptest.NewRequest("POST", "https://app.example/api/auth/token/", bytes.NewReader(body))
	r.RemoteAddr = peer
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ObtainToken(w, r)
	return w
}

func TestObtainTokenSharesFailedLoginCounter(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	password, cookie := loginHTTPFixture(t, s)
	for i := 0; i < 10; i++ {
		if out := tokenLogin(s, "source-http-login", "wrong-synthetic", fmt.Sprintf("192.0.2.130:%d", 1000+i)); out.Code != 400 || !strings.Contains(out.Body.String(), "Invalid credentials.") {
			t.Fatal("token failure response changed", i, out.Code)
		}
	}
	if out := tokenLogin(s, "source-http-login", password, "192.0.2.130:2000"); out.Code != 429 || !strings.Contains(out.Body.String(), "Try again later.") {
		t.Fatal("token endpoint kept verifying a locked username+peer pair", out.Code)
	}
	if out := loginHTTP(s, cookie, false, "source-http-login", password, "192.0.2.130:3000"); out.Code != 429 {
		t.Fatal("token failures did not lock the same pair on login", out.Code)
	}
	var tokens int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM authtoken_token`).Scan(&tokens); err != nil || tokens != 0 {
		t.Fatal("locked pair minted a token", tokens, err)
	}
	if out := loginHTTP(s, cookie, false, "source-http-login", password, "192.0.2.131:1"); out.Code != 200 {
		t.Fatal("token failures locked a different peer", out.Code)
	}
	if out := tokenLogin(s, "source-http-login", password, "192.0.2.131:2"); out.Code != 200 || !strings.Contains(out.Body.String(), `"token"`) {
		t.Fatal("valid token login from another peer refused", out.Code)
	}
}

func TestObtainTokenSuccessClearsFailuresAndOversizeCreatesNoRows(t *testing.T) {
	s, _ := loginCounterFixture(t, 10)
	password, cookie := loginHTTPFixture(t, s)
	for _, body := range []struct{ name, password string }{
		{"source-http-login", strings.Repeat("p", 1025)},
		{strings.Repeat("u", 151), "wrong-synthetic"},
	} {
		if out := tokenLogin(s, body.name, body.password, "192.0.2.140:1"); out.Code != 400 || !strings.Contains(out.Body.String(), "Invalid credentials.") {
			t.Fatal("oversize token credential response changed", out.Code)
		}
	}
	for _, table := range []string{"accounts_go_login_failure", "accounts_go_login_reservation"} {
		if rows := admissionRowCount(t, s, table); rows != 0 {
			t.Fatal("hash-free token rejection created failure state", table, rows)
		}
	}
	for i := 0; i < 3; i++ {
		if out := loginHTTP(s, cookie, false, "source-http-login", "wrong-synthetic", "192.0.2.141:1"); out.Code != 401 {
			t.Fatal(out.Code)
		}
	}
	if failures, _, _, _ := loginSnapshot(t, s, "source-http-login", "192.0.2.141:2"); failures != 3 {
		t.Fatal("login failures not recorded", failures)
	}
	if out := tokenLogin(s, "source-http-login", password, "192.0.2.141:3"); out.Code != 200 {
		t.Fatal("valid token login refused", out.Code)
	}
	if failures, pending, expiry, _ := loginSnapshot(t, s, "source-http-login", "192.0.2.141:4"); failures != 0 || pending != 0 || expiry != nil {
		t.Fatal("successful token login did not clear the shared failure pair", failures, pending)
	}
}
