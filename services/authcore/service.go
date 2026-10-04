// Package authcore supplies login identity only. Applications remain responsible
// for consent, age verification, authorization, moderation and data erasure.
package authcore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

var (
	ErrNotFound     = errors.New("identity not found")
	ErrConflict     = errors.New("identity already exists")
	ErrCSRF         = errors.New("invalid request origin or CSRF token")
	usernamePattern = regexp.MustCompile(`^[\pL\pN_.@+\-]{3,150}$`)
	versionPattern  = regexp.MustCompile(`^v[1-9][0-9]*\.0$`)
)

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	Avatar   string `json:"avatar,omitempty"`
}

// Sessions store token hashes, never the bearer token sent to the browser.
type Session struct {
	TokenHash string
	UserID    string
	ExpiresAt time.Time
}

// A Store must persist identities and revocable sessions. FindOrCreateExternal
// keys identities by provider plus subject, and MUST NOT link by email address.
// GetSession checks expiry and rejects disabled/deleted users on every read.
type Store interface {
	FindByUsername(context.Context, string) (User, string, error)
	CreatePasswordUser(context.Context, User, string) (User, error)
	FindOrCreateExternal(context.Context, string, string, User) (User, error)
	CreateSession(context.Context, Session) error
	GetSession(context.Context, string, time.Time) (User, error)
	DeleteSession(context.Context, string) error
}

type Config struct {
	PublicURL            string
	GoogleClientID       string
	GoogleClientSecret   string
	FacebookClientID     string
	FacebookClientSecret string
	FacebookAPIVersion   string
	CookieName           string
	SessionTTL           time.Duration
	// Optional relative callback paths preserve existing provider registrations.
	OAuthCallbackPaths map[string]string
}

type Service struct {
	config       Config
	store        Store
	origin       string
	secure       bool
	http         *http.Client
	mu           sync.Mutex
	flows        map[string]oauthFlow
	attempts     map[string]attemptWindow
	google       *oidc.Provider
	googleMu     sync.Mutex
	googleIssuer string
	facebookBase string
	facebookAuth string
	now          func() time.Time
}

type attemptWindow struct {
	Count int
	Until time.Time
}

func New(config Config, store Store) (*Service, error) {
	u, err := url.Parse(config.PublicURL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("PublicURL must be an absolute origin")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return nil, errors.New("PublicURL must use HTTPS (HTTP is allowed only on loopback)")
	}
	if store == nil {
		return nil, errors.New("a persistent identity store is required")
	}
	if config.CookieName == "" {
		config.CookieName = "sessionid"
	}
	if len(config.CookieName) > 64 || (&http.Cookie{Name: config.CookieName}).Valid() != nil || config.CookieName == "csrftoken" || strings.HasPrefix(config.CookieName, "oauth_") {
		return nil, errors.New("invalid CookieName")
	}
	if strings.HasPrefix(config.CookieName, "__Host-") && u.Scheme != "https" {
		return nil, errors.New("__Host- cookies require HTTPS")
	}
	if config.SessionTTL == 0 {
		config.SessionTTL = 24 * time.Hour
	}
	if config.SessionTTL < time.Minute || config.SessionTTL > 30*24*time.Hour {
		return nil, errors.New("SessionTTL must be between one minute and 30 days")
	}
	if config.FacebookAPIVersion != "" && !versionPattern.MatchString(config.FacebookAPIVersion) {
		return nil, errors.New("FacebookAPIVersion must be vNN.0")
	}
	paths := make(map[string]string, len(config.OAuthCallbackPaths))
	for provider, path := range config.OAuthCallbackPaths {
		parsed, err := url.Parse(path)
		if (provider != "google" && provider != "facebook") || err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\\r\n") {
			return nil, errors.New("OAuth callback paths must be relative paths")
		}
		paths[provider] = path
	}
	config.OAuthCallbackPaths = paths
	transport := boundedTransport{base: http.DefaultTransport, maxBytes: 1 << 20}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Service{config: config, store: store, origin: u.Scheme + "://" + u.Host, secure: u.Scheme == "https", http: client, flows: make(map[string]oauthFlow), attempts: make(map[string]attemptWindow), googleIssuer: "https://accounts.google.com", facebookBase: "https://graph.facebook.com/" + config.FacebookAPIVersion, facebookAuth: "https://www.facebook.com/" + config.FacebookAPIVersion + "/dialog/oauth", now: time.Now}, nil
}

func isLoopback(host string) bool {
	return host == "localhost" || (net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func (s *Service) Register(mux *http.ServeMux, prefix string) {
	prefix = strings.TrimSuffix(prefix, "/")
	mux.HandleFunc("POST "+prefix+"/signup", s.Signup)
	mux.HandleFunc("POST "+prefix+"/login", s.Login)
	mux.HandleFunc("POST "+prefix+"/logout", s.Logout)
	mux.HandleFunc("GET "+prefix+"/me", s.Current)
	mux.HandleFunc("GET "+prefix+"/csrf", s.CSRF)
	mux.HandleFunc("GET "+prefix+"/providers", s.Providers)
	mux.HandleFunc("GET "+prefix+"/oauth/{provider}/start", s.OAuthStart)
	mux.HandleFunc("GET "+prefix+"/oauth/{provider}/callback", s.OAuthCallback)
}

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
func randomToken() string { return rand.Text() + rand.Text() }

// Authenticate does not authorize any application operation.
func (s *Service) Authenticate(r *http.Request) (User, error) {
	cookie, err := r.Cookie(s.config.CookieName)
	if err != nil || len(cookie.Value) != 52 {
		return User{}, ErrNotFound
	}
	return s.store.GetSession(r.Context(), tokenHash(cookie.Value), s.now())
}

func (s *Service) cookie(w http.ResponseWriter, name, value string, ttl time.Duration, httpOnly bool) {
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: httpOnly, Secure: s.secure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge, Expires: s.now().Add(ttl)})
}

// CheckCSRF uses both a same-origin check and a browser-only double-submit token.
// Application write handlers must call it even before a user has authenticated.
func (s *Service) CheckCSRF(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		u, err := url.Parse(r.Header.Get("Referer"))
		if err == nil && u.Host != "" {
			origin = u.Scheme + "://" + u.Host
		}
	}
	if origin != s.origin {
		return ErrCSRF
	}
	cookie, err := r.Cookie("csrftoken")
	provided := r.Header.Get("X-CSRFToken")
	if provided == "" {
		provided = r.Header.Get("X-CSRF-Token")
	}
	if err != nil || len(cookie.Value) != 52 || len(provided) != len(cookie.Value) || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(provided)) != 1 {
		return ErrCSRF
	}
	return nil
}

func (s *Service) CSRF(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	value := randomToken()
	s.cookie(w, "csrftoken", value, s.config.SessionTTL, false)
	respond(w, http.StatusOK, map[string]string{"csrf_token": value})
}

// EnsureCSRF seeds a token if absent, for an application's own /api/me handler.
// Preserving an existing token prevents races between concurrent tabs/readers.
func (s *Service) EnsureCSRF(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie("csrftoken"); err == nil && len(cookie.Value) == 52 {
		return cookie.Value
	}
	value := randomToken()
	s.cookie(w, "csrftoken", value, s.config.SessionTTL, false)
	return value
}

func (s *Service) Current(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	s.EnsureCSRF(w, r)
	user, err := s.Authenticate(r)
	if errors.Is(err, ErrNotFound) {
		respond(w, http.StatusOK, map[string]any{"authenticated": false, "user": nil})
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	respond(w, http.StatusOK, map[string]any{"authenticated": true, "user": user})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}

func decodeCredentials(w http.ResponseWriter, r *http.Request) (credentials, error) {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var input credentials
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		return credentials{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return credentials{}, errors.New("multiple request values")
	}
	return input, nil
}

func (s *Service) allowAttempt(r *http.Request) bool {
	// RemoteAddr is supplied by the HTTP server. Do not trust forwarded headers.
	key, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		key = r.RemoteAddr
	}
	if len(key) > 128 {
		return false
	}
	if store, ok := s.store.(AuthAttemptStore); ok {
		allowed, err := store.AllowAuthAttempt(r.Context(), tokenHash(key), s.now())
		return err == nil && allowed
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, v := range s.attempts {
		if !v.Until.After(now) {
			delete(s.attempts, k)
		}
	}
	v, found := s.attempts[key]
	if !found && len(s.attempts) >= 4096 {
		return false
	}
	if !found {
		v.Until = now.Add(time.Minute)
	}
	v.Count++
	s.attempts[key] = v
	return v.Count <= 10
}

func (s *Service) guardWrite(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "POST required")
		return false
	}
	if err := s.CheckCSRF(r); err != nil {
		fail(w, http.StatusForbidden, "invalid request origin or CSRF token")
		return false
	}
	if !s.allowAttempt(r) {
		w.Header().Set("Retry-After", "60")
		fail(w, http.StatusTooManyRequests, "try again later")
		return false
	}
	return true
}

func (s *Service) Signup(w http.ResponseWriter, r *http.Request) {
	if !s.guardWrite(w, r) {
		return
	}
	input, err := decodeCredentials(w, r)
	if err != nil || !usernamePattern.MatchString(input.Username) || len(input.Email) > 254 || len(input.Name) > 150 {
		fail(w, http.StatusBadRequest, "invalid account details")
		return
	}
	if input.Email != "" {
		address, err := mail.ParseAddress(input.Email)
		if err != nil || address.Address != input.Email {
			fail(w, http.StatusBadRequest, "invalid account details")
			return
		}
	}
	hash, err := HashPassword(r.Context(), input.Password)
	if err != nil {
		fail(w, http.StatusBadRequest, "password must contain between 12 and 1024 bytes")
		return
	}
	user, err := s.store.CreatePasswordUser(r.Context(), User{Username: input.Username, Email: input.Email, Name: input.Name}, hash)
	if errors.Is(err, ErrConflict) {
		fail(w, http.StatusConflict, "username unavailable")
		return
	}
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	if err := s.createSession(w, r, user); err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	respond(w, http.StatusCreated, map[string]any{"authenticated": true, "user": user})
}

// A fixed dummy hash makes unknown-user login perform the same KDF as new users.
const dummyHash = "pbkdf2_sha256$1000000$non-secret-dummy-salt$9NRNZDjAwSULJWNrCGvaVaAu7V80qtxutSq33IWBTj4="

func (s *Service) Login(w http.ResponseWriter, r *http.Request) {
	if !s.guardWrite(w, r) {
		return
	}
	input, err := decodeCredentials(w, r)
	if err != nil || len(input.Username) > 150 || len(input.Password) > 1024 {
		fail(w, http.StatusBadRequest, "invalid login details")
		return
	}
	user, hash, err := s.store.FindByUsername(r.Context(), input.Username)
	if err != nil && !errors.Is(err, ErrNotFound) {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	found := err == nil
	if !found {
		hash = dummyHash
	}
	valid, err := VerifyPassword(r.Context(), input.Password, hash)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	if !found || !valid {
		fail(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if err := s.createSession(w, r, user); err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	respond(w, http.StatusOK, map[string]any{"authenticated": true, "user": user})
}

func (s *Service) createSession(w http.ResponseWriter, r *http.Request, user User) error {
	// Rotate existing authenticated sessions so successful login cannot fixate one.
	if old, err := r.Cookie(s.config.CookieName); err == nil && len(old.Value) == 52 {
		if err := s.store.DeleteSession(r.Context(), tokenHash(old.Value)); err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	token := randomToken()
	if err := s.store.CreateSession(r.Context(), Session{TokenHash: tokenHash(token), UserID: user.ID, ExpiresAt: s.now().Add(s.config.SessionTTL)}); err != nil {
		return err
	}
	s.cookie(w, s.config.CookieName, token, s.config.SessionTTL, true)
	s.cookie(w, "csrftoken", randomToken(), s.config.SessionTTL, false)
	return nil
}

func (s *Service) Logout(w http.ResponseWriter, r *http.Request) {
	if !s.guardWrite(w, r) {
		return
	}
	if err := s.RevokeSession(r); err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	s.ClearCookies(w)
	respond(w, http.StatusOK, map[string]bool{"authenticated": false})
}

// RevokeSession is for application-specific logout handlers. They must check
// same-origin CSRF before calling it.
func (s *Service) RevokeSession(r *http.Request) error {
	if cookie, err := r.Cookie(s.config.CookieName); err == nil && len(cookie.Value) == 52 {
		err = s.store.DeleteSession(r.Context(), tokenHash(cookie.Value))
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	return nil
}

// ClearCookies is also safe after the application has erased a user's rows.
func (s *Service) ClearCookies(w http.ResponseWriter) {
	s.cookie(w, s.config.CookieName, "", -time.Hour, true)
	s.cookie(w, "csrftoken", "", -time.Hour, false)
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, message string) {
	respond(w, status, map[string]string{"error": message})
}

func requireGET(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		fail(w, http.StatusMethodNotAllowed, "GET required")
		return false
	}
	return true
}

type boundedTransport struct {
	base     http.RoundTripper
	maxBytes int64
}

func (b boundedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := b.base.RoundTrip(r)
	if err != nil {
		return nil, errors.New("identity provider connection failed")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, b.maxBytes+1))
	_ = response.Body.Close()
	if err != nil || int64(len(body)) > b.maxBytes {
		return nil, errors.New("identity provider response exceeds limit")
	}
	response.Body = io.NopCloser(strings.NewReader(string(body)))
	return response, nil
}
