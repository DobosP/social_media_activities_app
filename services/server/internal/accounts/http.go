package accounts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func randomState() string { return rand.Text() + rand.Text() }
func hashState(raw string) string {
	hash := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(hash[:])
}

func (s *Service) Register(mux *http.ServeMux) {
	for _, prefix := range []string{"/api/accounts", "/api/v1/accounts"} {
		registerExact(mux, "GET "+prefix+"/me/", s.Me)
		registerExact(mux, "DELETE "+prefix+"/me/", s.DeleteMe)
		registerExact(mux, "GET "+prefix+"/me/export/", s.MeExport)
		registerExact(mux, "GET "+prefix+"/me/settings/", s.Settings)
		registerExact(mux, "PUT "+prefix+"/me/settings/", s.Settings)
		registerExact(mux, "GET "+prefix+"/me/avatar-style/", s.AvatarStyle)
		registerExact(mux, "POST "+prefix+"/me/avatar-style/", s.AvatarStyle)
		registerExact(mux, "GET "+prefix+"/wards/", s.Wards)
		registerExact(mux, "GET "+prefix+"/wards/{public_id}/", s.WardDetail)
		registerExact(mux, "PATCH "+prefix+"/wards/{public_id}/", s.WardDetail)
		registerExact(mux, "DELETE "+prefix+"/wards/{public_id}/", s.WardDetail)
		registerExact(mux, "GET "+prefix+"/wards/{public_id}/export/", s.WardExport)
		registerExact(mux, "POST "+prefix+"/wards/{public_id}/consent/", s.WardConsent)
		registerExact(mux, "DELETE "+prefix+"/wards/{public_id}/consent/", s.WardConsent)
		registerExact(mux, "GET "+prefix+"/guardian-links/", s.GuardianLinks)
		registerExact(mux, "POST "+prefix+"/guardian-links/", s.GuardianLinks)
		registerExact(mux, "POST "+prefix+"/guardian-links/{token}/accept/", s.AcceptGuardian)
		registerExact(mux, "POST "+prefix+"/guardian-links/{token}/decline/", s.DeclineGuardian)
		registerExact(mux, "POST "+prefix+"/verify-age/start/", s.AgeStart)
		registerExact(mux, "POST "+prefix+"/verify-age/", s.AgeVerify)
	}
	for _, path := range []string{"/api/auth/token/", "/api/v1/auth/token/"} {
		registerExact(mux, "POST "+path, s.ObtainToken)
		registerExact(mux, "DELETE "+path, s.RevokeToken)
	}
}

func (s *Service) Me(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	s.Auth.EnsureCSRF(w, r)
	payload, err := s.Self(r.Context(), s.DB, a)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, payload)
}
func (s *Service) DeleteMe(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	if err := s.Erase(r.Context(), a, a); err != nil {
		platform.Fail(w, err)
		return
	}
	s.Auth.ClearCookies(w)
	platform.JSON(w, 204, nil)
}
func (s *Service) AvatarStyle(w http.ResponseWriter, r *http.Request) {
	a, ok := s.require(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodPost {
		allowed, err := s.allowAction(r.Context(), a.ID, "avatar_style", 30, time.Hour)
		if err != nil {
			platform.Fail(w, err)
			return
		}
		if !allowed {
			platform.Error(w, 429, "Too many style changes; please try again later.")
			return
		}
		var body struct {
			Generation int `json:"generation"`
		}
		if platform.Decode(w, r, &body) != nil {
			platform.Fail(w, platform.ErrInvalid)
			return
		}
		if err := s.PickStyle(r.Context(), a, body.Generation); err != nil {
			platform.Fail(w, err)
			return
		}
	}
	result, err := s.Style(r.Context(), s.DB, a.ID, r.Method == http.MethodGet)
	if err != nil {
		platform.Fail(w, err)
		return
	}
	platform.JSON(w, 200, result)
}

func registerExact(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if strings.HasSuffix(pattern, "/") {
		pattern += "{$}"
	}
	mux.HandleFunc(pattern, handler)
}
