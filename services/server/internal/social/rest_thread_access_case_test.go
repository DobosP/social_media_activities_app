package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Frozen source apps/social/tests/test_api.py, SHA-256
// fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f:
// test_post_requires_membership:86, test_thread_posts_get_requires_membership:197,
// test_posts_cannot_be_ghostwritten_on_behalf_of:211. Correct production should pass.
func TestRESTSourceThreadMembershipPrivacyAndGuardianAuthorship(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		for _, scenario := range []string{"post_membership", "get_membership", "guardian_ghostwriting"} {
			t.Run(prefix+"/"+scenario, func(t *testing.T) {
				s, _ := testStore(t)
				ctx := context.Background()
				var owner, outsider Actor
				var activity int64
				if scenario == "guardian_ghostwriting" {
					owner = fixtureUser(t, s, "rest-thread-access-ward", "child")
					outsider = fixtureUser(t, s, "rest-thread-access-guardian", "adult")
					var consent int64
					if err := s.DB.QueryRow(ctx, `UPDATE accounts_parentalconsent SET guardian_identifier=$2 WHERE minor_id=$1 AND status='active' RETURNING id`, owner.ID, outsider.PublicID).Scan(&consent); err != nil {
						t.Fatal(err)
					}
					if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',$3,now(),now())`, outsider.ID, owner.ID, consent); err != nil {
						t.Fatal(err)
					}
					place := testdb.Place(t, s.DB, "Synthetic approved thread venue", "osm")
					if _, err := s.DB.Exec(ctx, `INSERT INTO places_approvedchildvenue(place_id,approved_by_id,note,created_at) VALUES($1,NULL,'Synthetic exact venue approval',now())`, place); err != nil {
						t.Fatal(err)
					}
					var typ int64
					if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
						t.Fatal(err)
					}
					var err error
					activity, err = s.CreateActivity(ctx, owner, ActivityInput{Place: place, ActivityType: typ, Title: "Kids game", StartsAt: time.Now()})
					if err != nil {
						t.Fatal(err)
					}
					var eligible bool
					if err := s.DB.QueryRow(ctx, `SELECT w.is_active AND w.is_identity_verified AND w.cohort='child' AND g.is_active AND g.is_identity_verified AND g.cohort='adult' AND EXISTS(SELECT 1 FROM accounts_guardianrelationship r JOIN accounts_parentalconsent c ON c.id=r.consent_id WHERE r.guardian_id=g.id AND r.ward_id=w.id AND r.status='active' AND c.id=$5 AND c.minor_id=w.id AND c.status='active' AND c.revoked_at IS NULL AND c.expires_at>now() AND c.guardian_identifier=g.public_id::text) AND EXISTS(SELECT 1 FROM places_approvedchildvenue v WHERE v.place_id=$3) AND NOT EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=$4 AND m.user_id=g.id) FROM accounts_user w JOIN accounts_user g ON g.id=$2 WHERE w.id=$1`, owner.ID, outsider.ID, place, activity, consent).Scan(&eligible); err != nil || !eligible {
						t.Fatal("guardian consent/cohort/venue/nonmember fixture is not eligible", eligible, err)
					}
					if err := platform.Participate(ctx, s.DB, owner); err != nil {
						t.Fatal("ward participation precondition", err)
					}
					ward, err := s.actingAs(ctx, outsider, owner.PublicID)
					if err != nil || ward.ID != owner.ID || ward.Cohort != "child" {
						t.Fatal("active guardian link cannot resolve its eligible ward", ward.ID, err)
					}
				} else {
					owner = fixtureUser(t, s, "rest-thread-access-owner", "adult")
					outsider = fixtureUser(t, s, "rest-thread-access-outsider", "adult")
					activity = fixtureActivity(t, s, owner, nil)
					if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET title='Hike',starts_at=$2 WHERE id=$1`, activity, time.Now()); err != nil {
						t.Fatal(err)
					}
					if _, err := s.Activity(ctx, outsider, activity); err != nil || outsider.Cohort != owner.Cohort {
						t.Fatal("outsider is not a same-cohort visible nonmember", err)
					}
					var seated bool
					if err := s.DB.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM social_membership WHERE activity_id=$1 AND user_id=$2)`, activity, outsider.ID).Scan(&seated); err != nil || seated {
						t.Fatal("outsider unexpectedly has a membership", seated, err)
					}
				}
				path := fmt.Sprintf("%s/activities/%d/posts/", prefix, activity)
				countPosts := func(body string) int {
					t.Helper()
					var count int
					if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM social_post p JOIN social_thread t ON t.id=p.thread_id WHERE t.activity_id=$1 AND ($2::text='' OR p.body=$2)`, activity, body).Scan(&count); err != nil {
						t.Fatal(err)
					}
					return count
				}
				if scenario == "get_membership" {
					if _, err := s.WritePost(ctx, owner, "activity", activity, PostInput{Body: "secret coordination"}, false); err != nil {
						t.Fatal(err)
					}
					mux := http.NewServeMux()
					s.Register(mux)
					get := func(a Actor) *httptest.ResponseRecorder {
						out := httptest.NewRecorder()
						mux.ServeHTTP(out, platform.WithActor(httptest.NewRequest(http.MethodGet, path, nil), a))
						return out
					}
					denied := get(outsider)
					if denied.Code != http.StatusForbidden {
						t.Errorf("private thread outsider GET status=%d, want403", denied.Code)
					}
					var deniedJSON any
					if err := json.Unmarshal(denied.Body.Bytes(), &deniedJSON); err != nil {
						t.Fatal(err)
					}
					canonical, err := json.Marshal(deniedJSON)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(canonical), "secret coordination") {
						t.Error("outsider GET disclosed the private thread body")
					}
					allowed := get(owner)
					if allowed.Code != http.StatusOK {
						t.Errorf("private thread owner GET status=%d, want200", allowed.Code)
					}
					raw := allowed.Body.Bytes()
					if prefix == "/api/v1/social" {
						var envelope map[string]json.RawMessage
						if err := json.Unmarshal(raw, &envelope); err != nil {
							t.Fatal(err)
						}
						raw = envelope["results"]
					}
					var posts []struct {
						Body string `json:"body"`
					}
					if err := json.Unmarshal(raw, &posts); err != nil || len(posts) != 1 || posts[0].Body != "secret coordination" {
						t.Errorf("owner did not read the actual private post: count=%d err=%v", len(posts), err)
					}
					return
				}
				body, ownBody := "hi", "Meet at 6"
				request := map[string]any{"body": body}
				if scenario == "guardian_ghostwriting" {
					body, ownBody = "ghostwritten", "Ward own message"
					request = map[string]any{"body": body, "on_behalf_of": owner.PublicID}
				}
				denied := restTransportCall(t, s, outsider, path, request)
				if denied.Code != http.StatusForbidden {
					t.Errorf("%s POST refusal status=%d, want403", scenario, denied.Code)
				}
				if countPosts(body) != 0 || countPosts("") != 0 {
					t.Errorf("%s POST refusal persisted a thread write", scenario)
				}
				// The same eligible owner/ward can write their own utterance; this
				// distinguishes an authorship refusal from an invalid child fixture.
				allowed := restTransportCall(t, s, owner, path, map[string]any{"body": ownBody})
				if allowed.Code != http.StatusCreated {
					t.Errorf("%s own POST positive control status=%d, want201", scenario, allowed.Code)
				}
				var author int64
				var storedBody string
				if err := s.DB.QueryRow(ctx, `SELECT p.author_id,p.body FROM social_post p JOIN social_thread t ON t.id=p.thread_id WHERE t.activity_id=$1 AND p.body=$2`, activity, ownBody).Scan(&author, &storedBody); err != nil || author != owner.ID || storedBody != ownBody {
					t.Errorf("own POST did not preserve actual author/body: author=%d body=%q err=%v", author, storedBody, err)
				}
			})
		}
	}
}
