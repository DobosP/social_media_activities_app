package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

const postNoticeCookie = "social_post_notice"
const postNoticeLifetime = 60 * time.Second

// Only these two repository-owned messages can cross a redirect. The cookie
// contains a fixed code, expiry and signature; no subject IDs, text or errors.
var postNoticeMessages = map[string]socialMessage{
	"d": {"tags": "info", "message": "This message had already been hidden by a moderation decision. Your deletion is recorded — the message will stay deleted even if that decision is later reversed. You can review the decision in your safety record."},
	"p": {"tags": "error", "message": "You're contesting the moderation decision that hid this message, so it wasn't deleted. If your contest succeeds and nothing else is holding the message, it will come back — you can delete it then if you still want to."},
}

func (s *Server) postNoticeTime() time.Time {
	if s.Accounts != nil && s.Accounts.Config.Now != nil {
		return s.Accounts.Config.Now()
	}
	return time.Now()
}

func (s *Server) postNoticeSignature(code, expiry string, a platform.Actor, path string) []byte {
	if s.Accounts == nil || len(s.Accounts.Secret) == 0 || a.ID < 1 {
		return nil
	}
	mac := hmac.New(sha256.New, s.Accounts.Secret)
	fmt.Fprintf(mac, "social.post_notice.v1\x00%s\x00%s\x00%d\x00%s", code, expiry, a.ID, path)
	return mac.Sum(nil)
}

func (s *Server) postNoticeCookieValue(r *http.Request, value string, maxAge int, expires time.Time) *http.Cookie {
	return &http.Cookie{Name: postNoticeCookie, Value: value, Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(s.Config.PublicURL, "https://"), SameSite: http.SameSiteLaxMode, MaxAge: maxAge, Expires: expires}
}

func (s *Server) redirectPostNotice(w http.ResponseWriter, r *http.Request, a platform.Actor, target, code string) {
	_, known := postNoticeMessages[code]
	expires := s.postNoticeTime().Add(postNoticeLifetime)
	expiry := strconv.FormatInt(expires.Unix(), 10)
	signature := s.postNoticeSignature(code, expiry, a, target)
	if !known || len(signature) == 0 || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") || strings.ContainsAny(target, "?\\\r\n\x00#") {
		platform.Error(w, 503, "Message feedback unavailable.")
		return
	}
	value := code + "." + expiry + "." + base64.RawURLEncoding.EncodeToString(signature)
	http.SetCookie(w, s.postNoticeCookieValue(r, value, int(postNoticeLifetime.Seconds()), expires))
	http.Redirect(w, r, target, http.StatusFound)
}

// Called only after a private view has authorized its destination. Clearing the
// browser cookie consumes the notice in normal navigation; a stateless signature
// does not prevent a caller replaying its own saved value during the short TTL.
func (s *Server) consumePostNotice(w http.ResponseWriter, r *http.Request, a platform.Actor) []socialMessage {
	cookie, err := r.Cookie(postNoticeCookie)
	if err != nil || len(cookie.Value) > 128 {
		return nil
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 {
		return nil
	}
	message, known := postNoticeMessages[parts[0]]
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	now := s.postNoticeTime().Unix()
	if !known || err != nil || expiry <= now || expiry > now+int64(postNoticeLifetime.Seconds()) {
		return nil
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	want := s.postNoticeSignature(parts[0], parts[1], a, r.URL.Path)
	if err != nil || len(want) == 0 || !hmac.Equal(signature, want) {
		return nil
	}
	http.SetCookie(w, s.postNoticeCookieValue(r, "", -1, time.Unix(1, 0)))
	text := message["message"]
	if s.Renderer != nil {
		text = s.Renderer.catalog.translate(language(r), text, 1)
	}
	return []socialMessage{{"tags": message["tags"], "message": text}}
}
