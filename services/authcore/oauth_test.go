package authcore

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type mockOIDC struct {
	mu          sync.Mutex
	server      *httptest.Server
	key         *rsa.PrivateKey
	nonce       string
	challenge   string
	claimChange func(map[string]any)
	tokenCalls  int
}

func newMockOIDC(t *testing.T) *mockOIDC {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	mock := &mockOIDC{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]any{"issuer": mock.server.URL, "authorization_endpoint": mock.server.URL + "/authorize", "token_endpoint": mock.server.URL + "/token", "jwks_uri": mock.server.URL + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("GET /keys", func(w http.ResponseWriter, r *http.Request) {
		respond(w, http.StatusOK, map[string]any{"keys": []any{map[string]string{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "mock-key", "n": base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())}}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		mock.tokenCalls++
		if err := r.ParseForm(); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "approved-code" || base64.RawURLEncoding.EncodeToString(digest[:]) != mock.challenge {
			t.Errorf("missing or incorrect PKCE verifier")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		claims := map[string]any{"iss": mock.server.URL, "aud": "google-client", "sub": "google-subject", "nonce": mock.nonce, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "same@example.com", "email_verified": true, "name": "Google User"}
		if mock.claimChange != nil {
			mock.claimChange(claims)
		}
		respond(w, http.StatusOK, map[string]any{"access_token": "mock-access", "token_type": "Bearer", "expires_in": 3600, "id_token": signedJWT(t, key, claims)})
	})
	mock.server = httptest.NewServer(mux)
	t.Cleanup(mock.server.Close)
	return mock
}

func signedJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "mock-key", "typ": "JWT"})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func oidcService(t *testing.T, mock *mockOIDC) (*Service, *testStore, *http.ServeMux) {
	t.Helper()
	store := newTestStore()
	service, err := New(Config{PublicURL: "https://app.example", GoogleClientID: "google-client", GoogleClientSecret: "mock-client-secret"}, store)
	if err != nil {
		t.Fatal(err)
	}
	service.googleIssuer = mock.server.URL
	mux := http.NewServeMux()
	service.Register(mux, "/api/auth")
	return service, store, mux
}

func startMockOAuth(t *testing.T, mux *http.ServeMux, mock *mockOIDC) (string, *http.Cookie) {
	t.Helper()
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/oauth/google/start?next=%2Fgames", nil))
	if out.Code != http.StatusFound {
		t.Fatalf("oauth start %d %s", out.Code, out.Body)
	}
	destination, err := url.Parse(out.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	query := destination.Query()
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" || query.Get("nonce") == "" || query.Get("redirect_uri") != "https://app.example/api/auth/oauth/google/callback" {
		t.Fatal("missing OAuth security parameters", query)
	}
	mock.mu.Lock()
	mock.nonce = query.Get("nonce")
	mock.challenge = query.Get("code_challenge")
	mock.mu.Unlock()
	return query.Get("state"), findCookie(t, out, "oauth_google")
}

func callbackMock(mux *http.ServeMux, state string, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/oauth/google/callback?state="+url.QueryEscape(state)+"&code=approved-code", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, r)
	return out
}

func TestOIDCSignedIdentityPKCEAndSingleUse(t *testing.T) {
	mock := newMockOIDC(t)
	service, store, mux := oidcService(t, mock)
	local, _ := store.CreatePasswordUser(context.Background(), User{Username: "local", Email: "same@example.com"}, "hash")
	state, cookie := startMockOAuth(t, mux, mock)
	out := callbackMock(mux, state, cookie)
	if out.Code != http.StatusSeeOther || out.Header().Get("Location") != "/games" {
		t.Fatalf("callback %d %s", out.Code, out.Body)
	}
	session := findCookie(t, out, "sessionid")
	r := httptest.NewRequest(http.MethodGet, "https://app.example", nil)
	r.AddCookie(session)
	user, err := service.Authenticate(r)
	if err != nil || user.ID == local.ID || user.Email != "same@example.com" {
		t.Fatalf("external identity %v %v", user, err)
	}
	if out = callbackMock(mux, state, cookie); out.Code != http.StatusBadRequest {
		t.Fatal("state replay accepted")
	}
	mock.mu.Lock()
	calls := mock.tokenCalls
	mock.mu.Unlock()
	if calls != 1 {
		t.Fatalf("replay exchanged token %d times", calls)
	}
}

func TestOIDCRejectsInvalidClaims(t *testing.T) {
	mock := newMockOIDC(t)
	changes := map[string]func(map[string]any){
		"nonce":           func(c map[string]any) { c["nonce"] = "attacker-nonce" },
		"issuer":          func(c map[string]any) { c["iss"] = "https://evil.example" },
		"audience":        func(c map[string]any) { c["aud"] = "other-client" },
		"expired":         func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() },
		"missing subject": func(c map[string]any) { delete(c, "sub") },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			_, store, mux := oidcService(t, mock)
			state, cookie := startMockOAuth(t, mux, mock)
			mock.mu.Lock()
			mock.claimChange = change
			mock.mu.Unlock()
			out := callbackMock(mux, state, cookie)
			if out.Code != http.StatusBadRequest || len(store.sessions) != 0 {
				t.Fatalf("invalid claims accepted %d %s", out.Code, out.Body)
			}
		})
	}
}

func TestOAuthRejectsUnboundAndExpiredState(t *testing.T) {
	mock := newMockOIDC(t)
	for _, scenario := range []string{"missing cookie", "different browser", "expired", "wrong provider"} {
		t.Run(scenario, func(t *testing.T) {
			service, _, mux := oidcService(t, mock)
			state, cookie := startMockOAuth(t, mux, mock)
			switch scenario {
			case "missing cookie":
				cookie = nil
			case "different browser":
				cookie.Value = randomToken()
			case "expired":
				service.now = func() time.Time { return time.Now().Add(6 * time.Minute) }
			case "wrong provider":
				service.config.FacebookClientID = "fb-client"
				service.config.FacebookClientSecret = "fb-secret"
				service.config.FacebookAPIVersion = "v25.0"
			}
			var out *httptest.ResponseRecorder
			if scenario == "wrong provider" {
				r := httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/oauth/facebook/callback?state="+state+"&code=approved-code", nil)
				r.AddCookie(&http.Cookie{Name: "oauth_facebook", Value: cookie.Value})
				out = httptest.NewRecorder()
				mux.ServeHTTP(out, r)
			} else {
				out = callbackMock(mux, state, cookie)
			}
			if out.Code != http.StatusBadRequest {
				t.Fatalf("unbound state accepted %d", out.Code)
			}
		})
	}
}

func TestFacebookAppScopedTokenValidation(t *testing.T) {
	for _, scenario := range []string{"success", "other application", "different subject", "expired"} {
		t.Run(scenario, func(t *testing.T) {
			store := newTestStore()
			service, err := New(Config{PublicURL: "https://app.example", FacebookClientID: "facebook-client", FacebookClientSecret: "mock-secret", FacebookAPIVersion: "v25.0"}, store)
			if err != nil {
				t.Fatal(err)
			}
			var challenge string
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/oauth/access_token":
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					digest := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
					if base64.RawURLEncoding.EncodeToString(digest[:]) != challenge {
						t.Error("Facebook PKCE parameter missing")
					}
					respond(w, http.StatusOK, map[string]any{"access_token": "facebook-token", "token_type": "Bearer", "expires_in": 3600})
				case "/debug_token":
					if r.Header.Get("Authorization") != "Bearer facebook-client|mock-secret" {
						t.Error("missing application credential")
					}
					app := "facebook-client"
					expiry := time.Now().Add(time.Hour).Unix()
					if scenario == "other application" {
						app = "attacker-client"
					}
					if scenario == "expired" {
						expiry = 1
					}
					respond(w, http.StatusOK, map[string]any{"data": map[string]any{"is_valid": true, "app_id": app, "user_id": "facebook-subject", "expires_at": expiry}})
				case "/me":
					if r.Header.Get("Authorization") != "Bearer facebook-token" || r.URL.Query().Get("appsecret_proof") == "" {
						t.Error("missing token proof")
					}
					subject := "facebook-subject"
					if scenario == "different subject" {
						subject = "attacker-subject"
					}
					respond(w, http.StatusOK, map[string]string{"id": subject, "email": "same@example.com", "name": "Facebook User"})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer provider.Close()
			service.facebookBase = provider.URL
			service.facebookAuth = provider.URL + "/authorize"
			mux := http.NewServeMux()
			service.Register(mux, "/api/auth")
			out := httptest.NewRecorder()
			mux.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/oauth/facebook/start", nil))
			if out.Code != http.StatusFound {
				t.Fatalf("start %d", out.Code)
			}
			location, _ := url.Parse(out.Header().Get("Location"))
			challenge = location.Query().Get("code_challenge")
			state := location.Query().Get("state")
			browser := findCookie(t, out, "oauth_facebook")
			r := httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/oauth/facebook/callback?state="+state+"&code=approved-code", nil)
			r.AddCookie(browser)
			out = httptest.NewRecorder()
			mux.ServeHTTP(out, r)
			if scenario == "success" {
				if out.Code != http.StatusSeeOther {
					t.Fatalf("Facebook callback %d %s", out.Code, out.Body)
				}
				if len(store.sessions) != 1 {
					t.Fatal("no session")
				}
			} else if out.Code != http.StatusBadRequest || len(store.sessions) != 0 {
				t.Fatalf("bad Facebook identity accepted %d", out.Code)
			}
		})
	}
}

func TestReturnPathRejectsExternalRedirect(t *testing.T) {
	for _, raw := range []string{"https://evil.example", "//evil.example", "/\\evil.example", "/games\r\nLocation:evil", "/%5cevil.example", "/%2f%2fevil.example", "/games%0d%0aLocation:evil", "/games%00"} {
		if safeReturnPath(raw) != "/" {
			t.Errorf("unsafe redirect %q", raw)
		}
	}
	if safeReturnPath("/games?mode=daily") != "/games?mode=daily" {
		t.Fatal("safe redirect rejected")
	}
}

func TestProviderTransportBoundsResponses(t *testing.T) {
	service, _, _ := testService(t)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(strings.Repeat("x", (1<<20)+1))) }))
	defer provider.Close()
	var value any
	if err := service.providerJSON(context.Background(), provider.URL, "mock-token", &value); err == nil {
		t.Fatal("unbounded provider response")
	}
}
