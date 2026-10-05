package web

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestWebCasePort4MyMeetupsOwnAdmittedUpcomingScope(t *testing.T) {
	s, owner, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	mine := socialLegacyActivity(t, s, owner, place, typ, "Sunset run")
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET meeting_point='By the fountain' WHERE id=$1`, mine); err != nil {
		t.Fatal(err)
	}
	viewer := webCasePort3Member(t, s, owner, mine, "case-port4-meetup-viewer")
	t.Run("own_meeting_point_and_other_members_meetup_excluded", func(t *testing.T) {
		theirs := socialLegacyActivity(t, s, owner, place, typ, "Their secret game")
		other := webCasePort3Member(t, s, owner, theirs, "case-port4-other-meetup-member")
		body := webCasePortHTML(t, mux, viewer, fmt.Sprintf("/my-meetups/?user_id=%d", other.ID))
		webCasePortContains(t, body, "Sunset run", "By the fountain")
		webCasePortAbsent(t, body, "Their secret game")
	})
	t.Run("cancelled_hidden_and_past_do_not_resurrect", func(t *testing.T) {
		for _, change := range []struct{ title, sql string }{{"Case4 cancelled meetup", "status='cancelled'"}, {"Case4 hidden meetup", "is_hidden=true"}, {"Case4 past meetup", "starts_at=now()-interval '1 hour'"}} {
			activity := socialLegacyActivity(t, s, owner, place, typ, change.title)
			membership, err := s.Social.Join(ctx, viewer, activity)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.Social.Vote(ctx, owner, membership, true, false); err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET `+change.sql+` WHERE id=$1`, activity); err != nil {
				t.Fatal(err)
			}
			webCasePortAbsent(t, webCasePortHTML(t, mux, viewer, "/my-meetups/"), change.title)
		}
	})
	t.Run("stale_cross_cohort_membership_excluded", func(t *testing.T) {
		activity := socialLegacyActivity(t, s, owner, place, typ, "Kids-only club")
		membership, err := s.Social.Join(ctx, viewer, activity)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.Social.Vote(ctx, owner, membership, true, false); err != nil {
			t.Fatal(err)
		}
		child := testdb.Actor(t, s.DB, "case-port4-child-meetup-owner", "child")
		if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET owner_id=$2,cohort='child' WHERE id=$1`, activity, child.ID); err != nil {
			t.Fatal(err)
		}
		webCasePortAbsent(t, webCasePortHTML(t, mux, viewer, "/my-meetups/"), "Kids-only club")
	})
	t.Run("removed_or_requested_membership_excluded", func(t *testing.T) {
		for _, state := range []string{"removed", "requested"} {
			activity := socialLegacyActivity(t, s, owner, place, typ, "Case4 left game "+state)
			membership, err := s.Social.Join(ctx, viewer, activity)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.DB.Exec(ctx, `UPDATE social_membership SET state=$2 WHERE id=$1`, membership, state); err != nil {
				t.Fatal(err)
			}
			webCasePortAbsent(t, webCasePortHTML(t, mux, viewer, "/my-meetups/"), "Case4 left game "+state)
		}
	})
	t.Run("login_wall_and_brand_new_empty_state", func(t *testing.T) {
		w := webCasePortRead(mux, platform.Actor{}, "/my-meetups/")
		if (w.Code != 301 && w.Code != 302) || !strings.Contains(w.Header().Get("Location"), "/login/") {
			t.Fatal("source offline page login gate", w.Code)
		}
		lonely := testdb.Actor(t, s.DB, "case-port4-empty-meetups", "adult")
		webCasePortContains(t, webCasePortHTML(t, mux, lonely, "/my-meetups/"), "No upcoming meetups")
	})
}

func TestWebCasePort4OfflineRegistrationAndSharedPhonePurgeWiring(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	body := webCasePortHTML(t, mux, actor, "/my-meetups/")
	webCasePortContains(t, body, "js/site.js", "js/my-meetups.js", "data-meetups-owner", actor.PublicID)
	webCasePortAbsent(t, webCasePortHTML(t, mux, platform.Actor{}, "/"), "/sw.js", "mz-meetups-owner")
	js, err := os.ReadFile(filepath.Join(s.Config.Root, "static/js/site.js"))
	if err != nil {
		t.Fatal(err)
	}
	webCasePortContains(t, string(js), "serviceWorker", "mz-meetups-owner", "caches.delete", `"purge"`, `form[action$="/logout/"]`)
}

func TestWebCasePort4ServiceWorkerAnonymousRootProtocol(t *testing.T) {
	// The script is public and must remain independently usable without
	// account/service dependencies; its cached HTML stays session-bound.
	s := &Server{}
	mux := http.NewServeMux()
	s.Register(mux)
	w := webCasePortRead(mux, platform.Actor{}, "/sw.js")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") || w.Header().Get("Service-Worker-Allowed") != "/" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("source root service-worker protocol", w.Code, w.Header())
	}
	webCasePortContains(t, w.Body.String(), "/my-meetups/", "fetch(req)", "caches.match(PAGE)", "'purge'")
	// This independent frozen oracle hash covers every request/cache/purge
	// branch, so the native Go gate also rejects an unchecked scope expansion.
	if got := fmt.Sprintf("%x", sha256.Sum256(w.Body.Bytes())); got != "a6d20d592b64b4dcdc3816a1bbc5e21c727bfb665900bf3f5aca30669e80bbd9" {
		t.Fatal("served worker differs from frozen original script", got)
	}
}
