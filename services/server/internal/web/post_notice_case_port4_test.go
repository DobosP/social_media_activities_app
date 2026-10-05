package web

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestWebCasePort4PostNoticeCarryBoundToActorPathAndShortExpiry(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	activity := socialLegacyActivity(t, s, owner, place, typ, "Case4 notice target")
	other := socialLegacyActivity(t, s, owner, place, typ, "Case4 other notice path")
	peer := testdb.Actor(t, s.DB, "case-port4-other-notice-actor", "adult")
	path := fmt.Sprintf("/activities/%d/", activity)
	otherPath := fmt.Sprintf("/activities/%d/", other)
	created := time.Now().Truncate(time.Second)
	now := created
	s.Accounts.Config.Now = func() time.Time { return now }
	makeCookie := func(code string) *http.Cookie {
		w := httptest.NewRecorder()
		s.redirectPostNotice(w, httptest.NewRequest("POST", "https://fixture.local"+path, nil), owner, path, code)
		if w.Code != 302 || w.Header().Get("Location") != path || strings.Contains(w.Header().Get("Location"), "?") {
			t.Fatal("source feedback302 has private query payload", w.Code, w.Header().Get("Location"))
		}
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == postNoticeCookie {
				return cookie
			}
		}
		t.Fatal("source feedback cookie missing")
		return nil
	}
	read := func(cookie *http.Cookie, actor platform.Actor, target string) *httptest.ResponseRecorder {
		r := platform.WithActor(httptest.NewRequest("GET", "https://fixture.local"+target, nil), actor)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	cleared := func(w *httptest.ResponseRecorder) bool {
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == postNoticeCookie && cookie.Value == "" && cookie.MaxAge == -1 {
				return true
			}
		}
		return false
	}
	cookie := makeCookie("d")
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 3 || parts[0] != "d" || cookie.Path != "/" || cookie.MaxAge != 60 || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("notice must carry fixed code/expiry/MAC only with bounded browser flags")
	}
	if signature, err := base64.RawURLEncoding.DecodeString(parts[2]); err != nil || len(signature) != 32 {
		t.Fatal("notice signature format", err)
	}
	t.Run("valid_destination_message_clear_and_normal_browser_single_use", func(t *testing.T) {
		w := read(cookie, owner, path)
		if w.Code != 200 || !cleared(w) {
			t.Fatal("valid authorized notice not consumed", w.Code)
		}
		webCasePortContains(t, w.Body.String(), postNoticeMessages["d"]["message"])
		webCasePortAbsent(t, read(nil, owner, path).Body.String(), "This message had already been hidden by a moderation decision.")
	})
	t.Run("tampered_wrong_actor_wrong_path_unknown_and_expired_are_silent", func(t *testing.T) {
		signature, _ := base64.RawURLEncoding.DecodeString(parts[2])
		signature[0] ^= 1
		tampered := parts[0] + "." + parts[1] + "." + base64.RawURLEncoding.EncodeToString(signature)
		for _, check := range []struct {
			name, value, target string
			actor               platform.Actor
			expired             bool
		}{
			{"tampered", tampered, path, owner, false},
			{"wrong_actor", cookie.Value, path, peer, false},
			{"wrong_path", cookie.Value, otherPath, owner, false},
			{"unknown_code", "unknown." + parts[1] + "." + parts[2], path, owner, false},
			{"expired", cookie.Value, path, owner, true},
		} {
			t.Run(check.name, func(t *testing.T) {
				now = created
				if check.expired {
					now = created.Add(61 * time.Second)
				}
				copy := *cookie
				copy.Value = check.value
				w := read(&copy, check.actor, check.target)
				if w.Code != 200 || cleared(w) {
					t.Fatal("invalid/unbound notice consumed", w.Code)
				}
				webCasePortAbsent(t, w.Body.String(), "This message had already been hidden by a moderation decision.", "contesting the moderation decision that hid this message")
			})
		}
		now = created
	})
	t.Run("private_view_refusal_does_not_consume_notice", func(t *testing.T) {
		if _, err := s.DB.Exec(context.Background(), `UPDATE social_activity SET is_hidden=true WHERE id=$1`, activity); err != nil {
			t.Fatal(err)
		}
		w := read(cookie, owner, path)
		if w.Code != 404 || cleared(w) {
			t.Fatal("notice consumed before private view authorization", w.Code)
		}
		if _, err := s.DB.Exec(context.Background(), `UPDATE social_activity SET is_hidden=false WHERE id=$1`, activity); err != nil {
			t.Fatal(err)
		}
		w = read(cookie, owner, path)
		if w.Code != 200 || !cleared(w) {
			t.Fatal("authorized retry lost notice", w.Code)
		}
	})
	t.Run("pending_remedy_static_copy_and_insecure_development_cookie", func(t *testing.T) {
		w := read(makeCookie("p"), owner, path)
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		webCasePortContains(t, w.Body.String(), "contesting the moderation decision that hid this message", "nothing else is holding the message")
		s.Config.PublicURL = "http://fixture.local"
		r := httptest.NewRequest("GET", "http://fixture.local/", nil)
		if s.postNoticeCookieValue(r, "", -1, time.Unix(1, 0)).Secure {
			t.Fatal("development HTTP cookie marked secure without TLS")
		}
	})
	t.Run("pending_signal_only_for_current_author_api_stays_generic", func(t *testing.T) {
		ctx := context.Background()
		post, err := s.Social.WritePost(ctx, owner, "activity", activity, social.PostInput{Body: "private pending notice scope"}, false)
		if err != nil {
			t.Fatal(err)
		}
		mod := webCasePort3Moderator(t, s)
		action := webCasePort3Moderate(t, s, mod, "social", "post", post, "remove", "other", "private note never carried")
		if _, err := s.Safety.FileAppeal(ctx, owner, action, "current owned appeal"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Social.DeletePost(ctx, owner, post); !errors.Is(err, social.ErrPendingPostAppeal) || !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("owner-specific refusal classification", err)
		}
		if _, err := s.Social.DeletePost(ctx, peer, post); !errors.Is(err, platform.ErrForbidden) || errors.Is(err, social.ErrPendingPostAppeal) {
			t.Fatal("pending appeal disclosed to nonauthor", err)
		}
		for _, actor := range []platform.Actor{owner, peer} {
			r := platform.WithActor(httptest.NewRequest("DELETE", fmt.Sprintf("/api/social/posts/%d/", post), nil), actor)
			w := httptest.NewRecorder()
			s.API.ServeHTTP(w, r)
			if w.Code != 403 || strings.TrimSpace(w.Body.String()) != `{"detail":"Permission denied."}` {
				t.Fatal("native API changed private refusal wire", w.Code, w.Body.String())
			}
		}
		var deleted bool
		if err := s.DB.QueryRow(ctx, `SELECT is_author_deleted FROM social_post WHERE id=$1`, post).Scan(&deleted); err != nil || deleted {
			t.Fatal("pending refusal changed author provenance", err, deleted)
		}
	})
}
