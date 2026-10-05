package accounts_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func correctionAccountRequest(s *accounts.Service, a platform.Actor, method, path, body string) *httptest.ResponseRecorder {
	r := platform.WithActor(httptest.NewRequest(method, path, strings.NewReader(body)), a)
	mux := http.NewServeMux()
	s.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestCoverageCorrectionWardExportNeverReleasesWithdrawnWordsToGuardian(t *testing.T) {
	s := correctionAccounts(t)
	s.Config.AllowMinorOnboarding = true
	ctx := context.Background()
	g := testdb.Actor(t, s.DB, "correction-ward-export-guardian", "adult")
	w := testdb.Actor(t, s.DB, "correction-ward-export-subject", "child")
	// testdb's generic consent belongs to a synthetic fixture identifier. Use the
	// actual mutually confirmed relationship and current guardian's own grant.
	if _, err := s.DB.Exec(ctx, `DELETE FROM accounts_parentalconsent WHERE minor_id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	created := correctionAccountRequest(s, g, "POST", "/api/accounts/guardian-links/", `{"ward":"`+w.PublicID+`"}`)
	if created.Code != 201 {
		t.Fatal("guardian invitation handler failed", created.Code)
	}
	var invitation map[string]any
	if err := json.Unmarshal(created.Body.Bytes(), &invitation); err != nil {
		t.Fatal(err)
	}
	token := invitation["token"].(string)
	if accepted := correctionAccountRequest(s, w, "POST", "/api/accounts/guardian-links/"+token+"/accept/", `{}`); accepted.Code != 200 {
		t.Fatal("ward acceptance handler failed", accepted.Code)
	}
	if consent := correctionAccountRequest(s, g, "POST", "/api/accounts/wards/"+w.PublicID+"/consent/", `{}`); consent.Code != 201 {
		t.Fatal("guardian consent handler failed", consent.Code)
	}
	native, activity := correctionActivity(t, s, w)
	const withdrawn = "synthetic ward's affirmatively withdrawn private words"
	post, err := native.WritePost(ctx, w, "activity", activity, social.PostInput{Body: withdrawn}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := native.DeletePost(ctx, w, post); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name           string
		actor          platform.Actor
		path, wantBody string
	}{
		{"guardian", g, "/api/accounts/wards/" + w.PublicID + "/export/", "[removed]"},
		{"subject", w, "/api/accounts/me/export/", withdrawn},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			out := correctionAccountRequest(s, scenario.actor, "GET", scenario.path, "")
			if out.Code != 200 {
				t.Fatal("actual export handler refused authorized subject", out.Code)
			}
			var payload map[string]any
			if err := json.Unmarshal(out.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			rows := payload["thread_posts"].(map[string]any)["items"].([]any)
			if len(rows) != 1 {
				t.Fatal("withdrawal export changed own post inventory")
			}
			row := rows[0].(map[string]any)
			if row["thread_id"] != float64(activity) || row["status"] != "deleted_by_you" || row["body"] != scenario.wantBody {
				t.Fatal("guardian/subject API withdrawal cap drift")
			}
			if scenario.name == "guardian" && strings.Contains(out.Body.String(), withdrawn) {
				t.Fatal("guardian actual API response disclosed withdrawn child text")
			}
		})
	}
}
