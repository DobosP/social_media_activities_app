package web

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/safety"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func webCasePort3Moderator(t *testing.T, s *Server) platform.Actor {
	t.Helper()
	mod := testdb.Actor(t, s.DB, "case-port3-private-moderator", "adult")
	mod.Role, mod.IsStaff = "moderator", true
	if _, err := s.DB.Exec(context.Background(), `UPDATE accounts_user SET role='moderator',is_staff=true WHERE id=$1`, mod.ID); err != nil {
		t.Fatal(err)
	}
	return mod
}

func webCasePort3Moderate(t *testing.T, s *Server, mod platform.Actor, app, model string, id int64, decision, reason, note string) int64 {
	t.Helper()
	ctx := context.Background()
	target, err := s.Safety.ResolveTarget(ctx, s.DB, app, model, id)
	if err != nil {
		t.Fatal(err)
	}
	action, err := s.Safety.TakeAction(ctx, mod, target, safety.ActionInput{Decision: decision, Reason: reason, Notes: note}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return action
}

func TestWebCasePort3ProfileProvenanceExcludesRawAttestation(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'Synthetic source proof','openid4vp','adult',now(),now()+interval '1 year','{"age_over_16":true,"age_over_18":true,"holder_proof":"private-fixture","jwt_vc":"private-fixture"}','')`, actor.ID); err != nil {
		t.Fatal(err)
	}
	body := webCasePortHTML(t, mux, actor, "/profile/")
	webCasePortContains(t, body, "Verified as:", "Re-verify")
	webCasePortAbsent(t, body, "age_over_16", "age_over_18", "holder_proof", "jwt_vc", "private-fixture")
}

func TestWebCasePort3SafetyRecordOwnScopeAndRestorationLimit(t *testing.T) {
	s, actor, place, typ, mux := webCasePortFixture(t)
	ctx := context.Background()
	mod := webCasePort3Moderator(t, s)
	other := testdb.Actor(t, s.DB, "case-port3-other-safety-subject", "adult")
	webCasePort3Moderate(t, s, mod, "accounts", "user", actor.ID, "warn", "spam", "case-port3-secret moderator note")
	webCasePort3Moderate(t, s, mod, "accounts", "user", other.ID, "warn", "grooming", "other private decision")
	target, err := s.Safety.ResolveTarget(ctx, s.DB, "accounts", "user", other.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Safety.FileReport(ctx, actor, target, "harassment", "they were rude"); err != nil {
		t.Fatal(err)
	}
	t.Run("own_records_without_moderator_or_other_decision", func(t *testing.T) {
		body := webCasePortHTML(t, mux, actor, "/my-safety-record/")
		webCasePortContains(t, body, "Your safety record", "your account", "they were rude")
		webCasePortAbsent(t, body, mod.Username, "case-port3-secret moderator note", "Grooming / predatory contact", "other private decision")
	})
	t.Run("anonymous_redirect_to_login", func(t *testing.T) {
		w := webCasePortRead(mux, platform.Actor{}, "/my-safety-record/")
		if (w.Code != 301 && w.Code != 302) || !strings.Contains(w.Header().Get("Location"), "/login/") {
			t.Fatal("source safety record login gate", w.Code, w.Header().Get("Location"))
		}
	})
	t.Run("author_deleted_remove_has_one_restoration_limit", func(t *testing.T) {
		activity := socialLegacyActivity(t, s, actor, place, typ, "Case port3 safety post scope")
		deleted, err := s.Social.WritePost(ctx, actor, "activity", activity, social.PostInput{Body: "my own words"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.Social.DeletePost(ctx, actor, deleted); err != nil {
			t.Fatal(err)
		}
		webCasePort3Moderate(t, s, mod, "social", "post", deleted, "remove", "other", "")
		kept, err := s.Social.WritePost(ctx, actor, "activity", activity, social.PostInput{Body: "perfectly fine"}, false)
		if err != nil {
			t.Fatal(err)
		}
		webCasePort3Moderate(t, s, mod, "social", "post", kept, "remove", "spam", "")
		body := webCasePortHTML(t, mux, actor, "/my-safety-record/")
		if strings.Count(body, "You also deleted this message yourself.") != 1 {
			t.Fatal("source restoration limit must appear for exactly author-deleted row")
		}
	})
}

func TestWebCasePort3LoggedInContestBindsCurrentOwner(t *testing.T) {
	s, actor, _, _, mux := webCasePortFixture(t)
	ctx := context.Background()
	mod := webCasePort3Moderator(t, s)
	other := testdb.Actor(t, s.DB, "case-port3-other-appeal-owner", "adult")
	otherAction := webCasePort3Moderate(t, s, mod, "accounts", "user", other.ID, "warn", "spam", "")
	t.Run("other_users_action_404_no_appeal", func(t *testing.T) {
		w := webCasePort2Post(t, mux, actor, "/my-safety-record/", "/my-safety-record/contest/", url.Values{"action_id": {spaText(otherAction)}, "statement": {"not mine"}})
		if w.Code != 404 {
			t.Fatal("source other-owner contest wall", w.Code, w.Body.String())
		}
		var count int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_moderationappeal`).Scan(&count); err != nil || count != 0 {
			t.Fatal("unauthorized contest created appeal", err, count)
		}
	})
	t.Run("own_warn_roundtrip_creates_owned_appeal", func(t *testing.T) {
		action := webCasePort3Moderate(t, s, mod, "accounts", "user", actor.ID, "warn", "spam", "")
		w := webCasePort2Post(t, mux, actor, "/my-safety-record/", "/my-safety-record/contest/", url.Values{"action_id": {spaText(action)}, "statement": {"the warning was unfair"}})
		if w.Code != 302 || w.Header().Get("Location") != "/my-safety-record/" {
			t.Fatal("source own contest redirect", w.Code, w.Body.String())
		}
		webCasePortHTML(t, mux, actor, w.Header().Get("Location"))
		var count int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_moderationappeal WHERE action_id=$1 AND appellant_id=$2 AND statement='the warning was unfair'`, action, actor.ID).Scan(&count); err != nil || count != 1 {
			t.Fatal("registered contest did not bind action and appellant", err, count)
		}
	})
}
