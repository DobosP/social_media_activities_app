package authcore

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// This store is a test double, never an exported production implementation.
type testStore struct {
	mu        sync.Mutex
	users     map[string]User
	passwords map[string]string
	sessions  map[string]Session
	external  map[string]string
	flows     map[string]OAuthFlow
}

func newTestStore() *testStore {
	return &testStore{users: map[string]User{}, passwords: map[string]string{}, sessions: map[string]Session{}, external: map[string]string{}, flows: map[string]OAuthFlow{}}
}
func (s *testStore) FindByUsername(ctx context.Context, name string) (User, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, user := range s.users {
		if user.Username == name {
			return user, s.passwords[user.ID], nil
		}
	}
	return User{}, "", ErrNotFound
}
func (s *testStore) CreatePasswordUser(ctx context.Context, user User, hash string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, old := range s.users {
		if old.Username == user.Username {
			return User{}, ErrConflict
		}
	}
	user.ID = randomToken()
	s.users[user.ID] = user
	s.passwords[user.ID] = hash
	return user, nil
}
func (s *testStore) FindOrCreateExternal(ctx context.Context, provider, subject string, profile User) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := provider + ":" + subject
	if id, ok := s.external[key]; ok {
		return s.users[id], nil
	}
	profile.ID = randomToken()
	profile.Username = profile.ID
	s.external[key] = profile.ID
	s.users[profile.ID] = profile
	return profile, nil
}
func (s *testStore) CreateSession(ctx context.Context, session Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.TokenHash] = session
	return nil
}
func (s *testStore) GetSession(ctx context.Context, hash string, now time.Time) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[hash]
	if !ok || !session.ExpiresAt.After(now) {
		return User{}, ErrNotFound
	}
	return s.users[session.UserID], nil
}
func (s *testStore) DeleteSession(ctx context.Context, hash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, hash)
	return nil
}
func (s *testStore) CreateOAuthFlow(ctx context.Context, hash string, flow OAuthFlow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.flows) >= 1024 {
		return ErrConflict
	}
	s.flows[hash] = flow
	return nil
}
func (s *testStore) ConsumeOAuthFlow(ctx context.Context, hash string, now time.Time) (OAuthFlow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, ok := s.flows[hash]
	delete(s.flows, hash)
	if !ok || !flow.ExpiresAt.After(now) {
		return OAuthFlow{}, ErrNotFound
	}
	return flow, nil
}

func testService(t *testing.T) (*Service, *testStore, *http.ServeMux) {
	t.Helper()
	store := newTestStore()
	service, err := New(Config{PublicURL: "https://app.example", SessionTTL: time.Hour}, store)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	service.Register(mux, "/api/auth")
	return service, store, mux
}

func csrfCookie(t *testing.T, mux *http.ServeMux) *http.Cookie {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/csrf", nil)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, request)
	if out.Code != http.StatusOK {
		t.Fatalf("csrf status %d", out.Code)
	}
	for _, cookie := range out.Result().Cookies() {
		if cookie.Name == "csrftoken" {
			return cookie
		}
	}
	t.Fatal("missing csrf cookie")
	return nil
}

func writeRequest(path, body string, csrf *http.Cookie, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "https://app.example"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://app.example")
	r.Header.Set("Content-Type", "application/json")
	if csrf != nil {
		r.AddCookie(csrf)
		r.Header.Set("X-CSRFToken", csrf.Value)
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	return r
}

func findCookie(t *testing.T, response *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == name {
			return cookie
		}
	}
	t.Fatalf("missing cookie %s", name)
	return nil
}

func TestConfigRequiresHTTPSAndPersistentStore(t *testing.T) {
	store := newTestStore()
	for _, origin := range []string{"http://app.example", "https://app.example/path", "https://user:pass@app.example", "https://app.example?secret=x", "//app.example"} {
		if _, err := New(Config{PublicURL: origin}, store); err == nil {
			t.Errorf("accepted origin %s", origin)
		}
	}
	if _, err := New(Config{PublicURL: "https://app.example"}, nil); err == nil {
		t.Fatal("accepted nil store")
	}
	if _, err := New(Config{PublicURL: "http://127.0.0.1:9000"}, store); err != nil {
		t.Fatal(err)
	}
	if _, err := New(Config{PublicURL: "https://app.example", FacebookAPIVersion: "../../me"}, store); err == nil {
		t.Fatal("accepted invalid provider version")
	}
}

func TestSignupLoginRotationAndLogout(t *testing.T) {
	service, store, mux := testService(t)
	csrf := csrfCookie(t, mux)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, writeRequest("/api/auth/signup", `{"username":"player","password":"correct horse battery","email":"player@example.com"}`, csrf))
	if out.Code != http.StatusCreated {
		t.Fatalf("signup %d %s", out.Code, out.Body)
	}
	session := findCookie(t, out, "sessionid")
	csrf = findCookie(t, out, "csrftoken")
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || len(session.Value) != 52 {
		t.Fatalf("unsafe session cookie %#v", session)
	}
	if csrf.HttpOnly {
		t.Fatal("csrf cookie unavailable to frontend")
	}
	if _, ok := store.sessions[session.Value]; ok {
		t.Fatal("stored raw session bearer")
	}
	request := httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/me", nil)
	request.AddCookie(session)
	user, err := service.Authenticate(request)
	if err != nil || user.Username != "player" {
		t.Fatalf("authenticate %v %v", user, err)
	}
	out = httptest.NewRecorder()
	mux.ServeHTTP(out, writeRequest("/api/auth/login", `{"username":"player","password":"correct horse battery"}`, csrf, session))
	if out.Code != http.StatusOK {
		t.Fatalf("login %d %s", out.Code, out.Body)
	}
	rotated := findCookie(t, out, "sessionid")
	csrf = findCookie(t, out, "csrftoken")
	if rotated.Value == session.Value {
		t.Fatal("login did not rotate session")
	}
	if _, err := service.Authenticate(request); !errors.Is(err, ErrNotFound) {
		t.Fatal("old session remains usable")
	}
	out = httptest.NewRecorder()
	mux.ServeHTTP(out, writeRequest("/api/auth/logout", `{}`, csrf, rotated))
	if out.Code != http.StatusOK {
		t.Fatalf("logout %d", out.Code)
	}
	if findCookie(t, out, "sessionid").MaxAge != -1 {
		t.Fatal("logout did not clear cookie")
	}
	request = httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/me", nil)
	request.AddCookie(rotated)
	if _, err := service.Authenticate(request); !errors.Is(err, ErrNotFound) {
		t.Fatal("logout did not revoke persisted session")
	}
}

func TestCSRFFailsBeforeStoreOrHash(t *testing.T) {
	_, store, mux := testService(t)
	csrf := csrfCookie(t, mux)
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, func(r *http.Request) { r.Header.Del("X-CSRFToken") }, func(r *http.Request) { r.Header.Del("Origin") }, func(r *http.Request) { r.Header.Set("Origin", "null") }} {
		r := writeRequest("/api/auth/signup", `{"username":"player","password":"correct horse battery"}`, csrf)
		mutate(r)
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, r)
		if out.Code != http.StatusForbidden {
			t.Errorf("csrf accepted with status %d", out.Code)
		}
	}
	if len(store.users) != 0 {
		t.Fatal("failed csrf created user")
	}
}

func TestSessionExpiryAndUnknownToken(t *testing.T) {
	service, store, _ := testService(t)
	user := User{ID: "1", Username: "existing"}
	store.users[user.ID] = user
	token := randomToken()
	store.sessions[tokenHash(token)] = Session{TokenHash: tokenHash(token), UserID: user.ID, ExpiresAt: service.now().Add(-time.Second)}
	r := httptest.NewRequest(http.MethodGet, "https://app.example", nil)
	r.AddCookie(&http.Cookie{Name: "sessionid", Value: token})
	if _, err := service.Authenticate(r); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired session authenticated")
	}
}

func TestProvidersFailClosedWithoutCredentials(t *testing.T) {
	_, _, mux := testService(t)
	for _, provider := range []string{"google", "facebook", "unknown"} {
		out := httptest.NewRecorder()
		mux.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/oauth/"+provider+"/start", nil))
		if out.Code != http.StatusServiceUnavailable {
			t.Errorf("unconfigured %s status %d", provider, out.Code)
		}
	}
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, httptest.NewRequest(http.MethodGet, "https://app.example/api/auth/providers", nil))
	var providers map[string]bool
	if err := json.Unmarshal(out.Body.Bytes(), &providers); err != nil {
		t.Fatal(err)
	}
	if providers["google"] || providers["facebook"] || !providers["password"] {
		t.Fatal(providers)
	}
}

func TestBoundedBodiesAndRateLimit(t *testing.T) {
	_, _, mux := testService(t)
	csrf := csrfCookie(t, mux)
	out := httptest.NewRecorder()
	mux.ServeHTTP(out, writeRequest("/api/auth/signup", `{"username":"player","password":"`+strings.Repeat("x", 17<<10)+`"}`, csrf))
	if out.Code != http.StatusBadRequest {
		t.Fatalf("oversized status %d", out.Code)
	}
	for i := 0; i < 10; i++ {
		out = httptest.NewRecorder()
		mux.ServeHTTP(out, writeRequest("/api/auth/signup", `{"username":"?"}`, csrf))
	}
	if out.Code != http.StatusTooManyRequests {
		t.Fatalf("unbounded attempts %d", out.Code)
	}
}

func TestPasswordDjangoCompatibilityAndWorkBounds(t *testing.T) {
	// Expected bytes computed independently with Python hashlib.pbkdf2_hmac.
	encoded := "pbkdf2_sha256$1000$django-test-salt$V9X3atZE8fGNP2JD7x37QLrK1IWf3ml9/hPcqE3LZ8I="
	valid, err := VerifyPassword(context.Background(), "correct horse battery", encoded)
	if err != nil || !valid {
		t.Fatalf("Django fixture %v %v", valid, err)
	}
	valid, err = VerifyPassword(context.Background(), "wrong", encoded)
	if err != nil || valid {
		t.Fatal("accepted wrong password")
	}
	for _, hash := range []string{"argon2$unsupported", "pbkdf2_sha256$3000000$salt$anything", "pbkdf2_sha256$0$salt$anything", "pbkdf2_sha256$1000$$anything"} {
		valid, err := VerifyPassword(context.Background(), "correct horse battery", hash)
		if err != nil || valid {
			t.Errorf("accepted hash %q", hash)
		}
	}
	hash, err := HashPassword(context.Background(), "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	valid, err = VerifyPassword(context.Background(), "correct horse battery", hash)
	if err != nil || !valid {
		t.Fatal("new hash verification failed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Occupy all slots so a canceled waiter cannot begin expensive hashing.
	for range cap(passwordSlots) {
		passwordSlots <- struct{}{}
	}
	defer func() {
		for range cap(passwordSlots) {
			<-passwordSlots
		}
	}()
	if _, err := HashPassword(ctx, "correct horse battery"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled work %v", err)
	}
}

func TestExternalIdentityNeverLinksEmail(t *testing.T) {
	store := newTestStore()
	local, err := store.CreatePasswordUser(context.Background(), User{Username: "local", Email: "same@example.com"}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	external, err := store.FindOrCreateExternal(context.Background(), "google", "subject", User{Email: "same@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if external.ID == local.ID {
		t.Fatal("linked by email")
	}
	again, _ := store.FindOrCreateExternal(context.Background(), "google", "subject", User{Email: "other@example.com"})
	if again.ID != external.ID {
		t.Fatal("stable external ID changed with email")
	}
}
