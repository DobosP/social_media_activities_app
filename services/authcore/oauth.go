package authcore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const oauthTTL = 5 * time.Minute

// AuthAttemptStore lets an application enforce its attempt budget atomically
// across replicas. Keys are digests of the trusted server-side client address.
// Implementations should expire their counters and cap retained keys.
type AuthAttemptStore interface {
	AllowAuthAttempt(context.Context, string, time.Time) (bool, error)
}

type oauthFlow struct {
	Provider    string
	BrowserHash string
	Verifier    string
	Nonce       string
	RedirectURI string
	ReturnPath  string
	ExpiresAt   time.Time
}

// OAuth flows are short lived and capped; restarting a process fails outstanding
// flows closed. A replicated deployment should implement OAuthFlowStore in its
// persistent Store so any replica can atomically consume a flow exactly once.
type OAuthFlowStore interface {
	CreateOAuthFlow(context.Context, string, OAuthFlow) error
	ConsumeOAuthFlow(context.Context, string, time.Time) (OAuthFlow, error)
}

// OAuthFlow contains sensitive, short-lived PKCE material. Never log it.
type OAuthFlow struct {
	Provider    string
	BrowserHash string
	Verifier    string
	Nonce       string
	RedirectURI string
	ReturnPath  string
	ExpiresAt   time.Time
}

func (f oauthFlow) exported() OAuthFlow { return OAuthFlow(f) }

func (s *Service) providerEnabled(provider string) bool {
	switch provider {
	case "google":
		return s.config.GoogleClientID != "" && s.config.GoogleClientSecret != ""
	case "facebook":
		return s.config.FacebookClientID != "" && s.config.FacebookClientSecret != "" && versionPattern.MatchString(s.config.FacebookAPIVersion)
	default:
		return false
	}
}

func (s *Service) Providers(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	respond(w, http.StatusOK, map[string]bool{"password": true, "google": s.providerEnabled("google"), "facebook": s.providerEnabled("facebook")})
}

func (s *Service) googleProvider(ctx context.Context) (*oidc.Provider, error) {
	s.googleMu.Lock()
	defer s.googleMu.Unlock()
	if s.google != nil {
		return s.google, nil
	}
	provider, err := oidc.NewProvider(oidc.ClientContext(ctx, s.http), s.googleIssuer)
	if err != nil {
		return nil, errors.New("identity provider unavailable")
	}
	s.google = provider
	return provider, nil
}

func (s *Service) oauthConfig(ctx context.Context, provider, redirect string) (*oauth2.Config, error) {
	if !s.providerEnabled(provider) {
		return nil, errors.New("identity provider unavailable")
	}
	if provider == "google" {
		google, err := s.googleProvider(ctx)
		if err != nil {
			return nil, err
		}
		return &oauth2.Config{ClientID: s.config.GoogleClientID, ClientSecret: s.config.GoogleClientSecret, RedirectURL: redirect, Scopes: []string{oidc.ScopeOpenID, "email", "profile"}, Endpoint: google.Endpoint()}, nil
	}
	return &oauth2.Config{ClientID: s.config.FacebookClientID, ClientSecret: s.config.FacebookClientSecret, RedirectURL: redirect, Scopes: []string{"public_profile", "email"}, Endpoint: oauth2.Endpoint{AuthURL: s.facebookAuth, TokenURL: s.facebookBase + "/oauth/access_token", AuthStyle: oauth2.AuthStyleInParams}}, nil
}

func safeReturnPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || strings.HasPrefix(u.Path, "//") || strings.ContainsAny(raw, "\\\r\n") || strings.ContainsAny(u.Path, "\\\r\n") {
		return "/"
	}
	for _, r := range u.Path {
		if r < 32 || r == 127 {
			return "/"
		}
	}
	return u.String()
}

func (s *Service) createFlow(ctx context.Context, state string, flow oauthFlow) error {
	if store, ok := s.store.(OAuthFlowStore); ok {
		return store.CreateOAuthFlow(ctx, tokenHash(state), flow.exported())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for key, value := range s.flows {
		if !value.ExpiresAt.After(now) {
			delete(s.flows, key)
		}
	}
	if len(s.flows) >= 1024 {
		return errors.New("too many pending identity flows")
	}
	s.flows[tokenHash(state)] = flow
	return nil
}

func (s *Service) consumeFlow(ctx context.Context, state string) (oauthFlow, error) {
	if store, ok := s.store.(OAuthFlowStore); ok {
		flow, err := store.ConsumeOAuthFlow(ctx, tokenHash(state), s.now())
		return oauthFlow(flow), err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	flow, ok := s.flows[tokenHash(state)]
	delete(s.flows, tokenHash(state))
	if !ok || !flow.ExpiresAt.After(s.now()) {
		return oauthFlow{}, ErrNotFound
	}
	return flow, nil
}

func (s *Service) OAuthStart(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	provider := r.PathValue("provider")
	if !s.providerEnabled(provider) {
		fail(w, http.StatusServiceUnavailable, "identity provider unavailable")
		return
	}
	if !s.allowAttempt(r) {
		w.Header().Set("Retry-After", "60")
		fail(w, http.StatusTooManyRequests, "try again later")
		return
	}
	callback := s.config.OAuthCallbackPaths[provider]
	if callback == "" {
		callback = strings.TrimSuffix(r.URL.Path, "/start") + "/callback"
	}
	redirect := s.origin + callback
	config, err := s.oauthConfig(r.Context(), provider, redirect)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "identity provider unavailable")
		return
	}
	state, browser, nonce := randomToken(), randomToken(), randomToken()
	verifier := oauth2.GenerateVerifier()
	flow := oauthFlow{Provider: provider, BrowserHash: tokenHash(browser), Nonce: nonce, Verifier: verifier, RedirectURI: redirect, ReturnPath: safeReturnPath(r.URL.Query().Get("next")), ExpiresAt: s.now().Add(oauthTTL)}
	if err := s.createFlow(r.Context(), state, flow); err != nil {
		fail(w, http.StatusServiceUnavailable, "identity provider unavailable")
		return
	}
	s.cookie(w, "oauth_"+provider, browser, oauthTTL, true)
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), http.StatusFound)
}

func (s *Service) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	if !requireGET(w, r) {
		return
	}
	provider := r.PathValue("provider")
	state := r.URL.Query().Get("state")
	if !s.providerEnabled(provider) || len(state) != 52 {
		fail(w, http.StatusBadRequest, "invalid identity response")
		return
	}
	flow, err := s.consumeFlow(r.Context(), state)
	cookie, cookieErr := r.Cookie("oauth_" + provider)
	s.cookie(w, "oauth_"+provider, "", -time.Hour, true)
	if err != nil || flow.Provider != provider || cookieErr != nil || len(cookie.Value) != 52 || subtle.ConstantTimeCompare([]byte(tokenHash(cookie.Value)), []byte(flow.BrowserHash)) != 1 {
		fail(w, http.StatusBadRequest, "invalid identity response")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" || len(code) > 4096 || r.URL.Query().Get("error") != "" {
		fail(w, http.StatusBadRequest, "identity authorization declined")
		return
	}
	config, err := s.oauthConfig(r.Context(), provider, flow.RedirectURI)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "identity provider unavailable")
		return
	}
	ctx := context.WithValue(r.Context(), oauth2.HTTPClient, s.http)
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(flow.Verifier))
	if err != nil {
		fail(w, http.StatusBadRequest, "invalid identity response")
		return
	}
	var subject string
	var profile User
	if provider == "google" {
		subject, profile, err = s.googleIdentity(ctx, token, flow.Nonce)
	} else {
		subject, profile, err = s.facebookIdentity(ctx, token)
	}
	if err != nil || subject == "" || len(subject) > 255 {
		fail(w, http.StatusBadRequest, "invalid identity response")
		return
	}
	user, err := s.store.FindOrCreateExternal(ctx, provider, subject, profile)
	if err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	if err := s.createSession(w, r, user); err != nil {
		fail(w, http.StatusServiceUnavailable, "authentication unavailable")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, flow.ReturnPath, http.StatusSeeOther)
}

func (s *Service) googleIdentity(ctx context.Context, token *oauth2.Token, nonce string) (string, User, error) {
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 32<<10 {
		return "", User{}, errors.New("missing ID token")
	}
	provider, err := s.googleProvider(ctx)
	if err != nil {
		return "", User{}, err
	}
	verified, err := provider.VerifierContext(oidc.ClientContext(ctx, s.http), &oidc.Config{ClientID: s.config.GoogleClientID}).Verify(ctx, raw)
	if err != nil || len(verified.Nonce) != len(nonce) || subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(nonce)) != 1 {
		return "", User{}, errors.New("invalid ID token")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
	}
	if err := verified.Claims(&claims); err != nil || len(claims.Email) > 254 || len(claims.Name) > 150 {
		return "", User{}, errors.New("invalid identity profile")
	}
	if !claims.EmailVerified {
		claims.Email = ""
	}
	return verified.Subject, User{Email: claims.Email, Name: claims.Name}, nil
}

func (s *Service) facebookIdentity(ctx context.Context, token *oauth2.Token) (string, User, error) {
	if token.AccessToken == "" || len(token.AccessToken) > 4096 {
		return "", User{}, errors.New("invalid access token")
	}
	// Verify the returned token belongs to this application and this stable,
	// app-scoped identity, rather than trusting a profile supplied by the browser.
	endpoint := s.facebookBase + "/debug_token?" + url.Values{"input_token": {token.AccessToken}}.Encode()
	var debug struct {
		Data struct {
			IsValid   bool   `json:"is_valid"`
			AppID     string `json:"app_id"`
			UserID    string `json:"user_id"`
			ExpiresAt int64  `json:"expires_at"`
		} `json:"data"`
	}
	if err := s.providerJSON(ctx, endpoint, s.config.FacebookClientID+"|"+s.config.FacebookClientSecret, &debug); err != nil {
		return "", User{}, err
	}
	if !debug.Data.IsValid || debug.Data.AppID != s.config.FacebookClientID || debug.Data.UserID == "" || debug.Data.ExpiresAt <= s.now().Unix() {
		return "", User{}, errors.New("invalid access token")
	}
	mac := hmac.New(sha256.New, []byte(s.config.FacebookClientSecret))
	_, _ = mac.Write([]byte(token.AccessToken))
	endpoint = s.facebookBase + "/me?" + url.Values{"fields": {"id,name,email"}, "appsecret_proof": {hex.EncodeToString(mac.Sum(nil))}}.Encode()
	var profile struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	if err := s.providerJSON(ctx, endpoint, token.AccessToken, &profile); err != nil {
		return "", User{}, err
	}
	if profile.ID != debug.Data.UserID || len(profile.Name) > 150 || len(profile.Email) > 254 {
		return "", User{}, errors.New("invalid identity profile")
	}
	return profile.ID, User{Name: profile.Name, Email: profile.Email}, nil
}

func (s *Service) providerJSON(ctx context.Context, endpoint, bearer string, target any) error {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return errors.New("invalid identity endpoint")
	}
	r.Header.Set("Authorization", "Bearer "+bearer)
	r.Header.Set("Accept", "application/json")
	response, err := s.http.Do(r)
	if err != nil {
		return errors.New("identity provider unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return errors.New("identity provider rejected response")
	}
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return errors.New("invalid identity response")
	}
	return nil
}
