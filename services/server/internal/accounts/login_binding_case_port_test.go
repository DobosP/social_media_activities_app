package accounts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
)

type loginBindingStore struct {
	authcore.Store
	requested string
}

func (s *loginBindingStore) FindByUsername(_ context.Context, name string) (authcore.User, string, error) {
	s.requested = name
	return authcore.User{}, "", errors.New("synthetic verifier stop")
}

func TestLoginCredentialParserRejectsAliasesDuplicatesAndDelegatesSamePair(t *testing.T) {
	for _, body := range []string{
		`{"Username":"locked-target","password":"synthetic"}`,
		`{"username":"fresh-bucket","Username":"locked-target","password":"synthetic"}`,
		`{"username":"fresh-bucket","userNAME":"locked-target","password":"synthetic"}`,
		`{"username":"fresh-bucket","username":"locked-target","password":"synthetic"}`,
		`{"username":"fresh-bucket","user\u006eame":"locked-target","password":"synthetic"}`,
		`{"username":"locked-target","password":"synthetic","Password":"other"}`,
		`{"username":"locked-target","password":"synthetic","password":"other"}`,
		`{"username":{},"password":"synthetic"}`,
		`{"username":"locked-target","password":1}`,
		`{"username":"locked-target","password":"synthetic","role":"admin"}`,
		`{"username":"locked-target","password":"synthetic"} {}`,
		`null`, `[]`,
	} {
		if _, err := parseLoginCredentials([]byte(body)); err == nil {
			t.Fatal("ambiguous/malformed credentials admitted")
		}
	}
	s := New(nil, nil, "synthetic-review-binding-secret-32-bytes", Config{})
	ignored := []byte(`{"username":"locked-target","password":"synthetic","email":"` + strings.Repeat("<", 3000) + `"}`)
	clean, err := parseLoginCredentials(ignored)
	if err != nil {
		t.Fatal(err)
	}
	canonicalIgnored, err := json.Marshal(clean)
	if err != nil || len(canonicalIgnored) > 256 {
		t.Fatal("ignored metadata expanded delegated credential body", err)
	}
	for _, name := range []string{"locked-target", " locked-target", "locked-target ", "\t locked-target ", "\u2003locked-target\u2003"} {
		body, _ := json.Marshal(loginCredentials{Username: name, Password: "  synthetic password  "})
		credentials, err := parseLoginCredentials(body)
		if err != nil {
			t.Fatal(err)
		}
		if credentials.Username != "locked-target" || credentials.Password != "  synthetic password  " {
			t.Fatal("source username/password cleaning changed")
		}
		canonical, err := json.Marshal(credentials)
		if err != nil {
			t.Fatal(err)
		}
		store := &loginBindingStore{}
		auth, err := authcore.New(authcore.Config{PublicURL: "https://app.example"}, store)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("POST", "https://app.example/api/auth/login", bytes.NewReader(canonical))
		r.RemoteAddr = "192.0.2.66:4000"
		r.Header.Set("Origin", "https://app.example")
		r.Header.Set("X-CSRFToken", strings.Repeat("s", 52))
		r.AddCookie(&http.Cookie{Name: "csrftoken", Value: strings.Repeat("s", 52)})
		auth.Login(httptest.NewRecorder(), r)
		if store.requested != credentials.Username {
			t.Fatal("actual pinned decoder verified a different admitted username")
		}
		reserved, err := s.loginFailureKey(name, r.RemoteAddr)
		if err != nil {
			t.Fatal(err)
		}
		verified, err := s.loginFailureKey(store.requested, "192.0.2.66:5000")
		if err != nil || reserved != verified {
			t.Fatal("padding split reserved and actual verifier pair", err)
		}
	}
}

func TestLoginPOSTPaddingReplicasAndAliasBodiesCannotEscapeLockedPair(t *testing.T) {
	s, clock := loginCounterFixture(t, 4)
	password, cookie := loginHTTPFixture(t, s)
	replica := New(s.DB, s.Auth, string(s.Secret), s.Config)
	for i, name := range []string{" source-http-login", "source-http-login ", "\t source-http-login ", "\u2003source-http-login\u2003"} {
		service := s
		if i%2 == 1 {
			service = replica
		}
		out := loginHTTP(service, cookie, true, name, "wrong-synthetic", "192.0.2.66:4000")
		if out.Code != 200 || !strings.Contains(out.Body.String(), "Please enter a correct username and password") {
			t.Fatal("padding failed source form", out.Code)
		}
	}
	if failures, pending, _, _ := loginSnapshot(t, s, "source-http-login", "192.0.2.66:5000"); failures != 4 || pending != 0 {
		t.Fatal("padding/replicas split failure pair", failures, pending)
	}
	for _, body := range []string{
		`{"Username":"source-http-login","password":"wrong-synthetic"}`,
		`{"username":"fresh-bucket","Username":"source-http-login","password":"wrong-synthetic"}`,
		`{"username":"fresh-bucket","userNAME":"source-http-login","password":"wrong-synthetic"}`,
		`{"username":"fresh-bucket","username":"source-http-login","password":"wrong-synthetic"}`,
	} {
		r := httptest.NewRequest("POST", "https://app.example/api/auth/login", strings.NewReader(body))
		r.RemoteAddr = "192.0.2.66:6000"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.example")
		r.Header.Set("X-CSRFToken", cookie.Value)
		r.AddCookie(cookie)
		out := httptest.NewRecorder()
		replica.LoginPOST(out, r, false)
		if out.Code != 400 || len(out.Result().Cookies()) != 0 {
			t.Fatal("ambiguous API body reached authentication", out.Code)
		}
	}
	if out := loginHTTP(replica, cookie, false, " source-http-login ", password, "192.0.2.66:7000"); out.Code != 429 {
		t.Fatal("canonical JSON padding escaped actual locked pair", out.Code)
	}
	var pairs, sessions int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_go_login_failure`).Scan(&pairs); err != nil || pairs != 1 {
		t.Fatal("aliases minted fresh buckets", pairs, err)
	}
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_go_session`).Scan(&sessions); err != nil || sessions != 0 {
		t.Fatal("locked or ambiguous body created session", sessions, err)
	}
	clock.Advance(s.Config.LoginFailureWindow)
	if out := loginHTTP(replica, cookie, true, "\u2003 source-http-login \t", password, "192.0.2.66:8000"); out.Code != 302 {
		t.Fatal("legitimate source trim did not authenticate after expiry", out.Code)
	}
	if failures, pending, expiry, _ := loginSnapshot(t, s, "source-http-login", "192.0.2.66:9000"); failures != 0 || pending != 0 || expiry != nil {
		t.Fatal("padded success failed to clear exact admitted pair")
	}
}
