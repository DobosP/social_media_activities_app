package social

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Frozen source apps/social/tests/test_api.py, SHA-256
// fc35dea7bf6fc36444cb7aa4fcb52a274677bc0c78c328882dcd6ebdb759842f:
// arrived_action_marks_membership:362, arrived_ignores_on_behalf_of:371,
// transit_ignores_on_behalf_of:419. No production discrepancy is presumed.
func TestRESTSourcePresenceOwnActorAndGuardianNonDelegation(t *testing.T) {
	for _, prefix := range []string{"/api/social", "/api/v1/social"} {
		t.Run(prefix+"/adult_arrived", func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			owner := fixtureUser(t, s, "rest-presence-adult", "adult")
			activity := fixtureActivity(t, s, owner, nil)
			if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET starts_at=$2 WHERE id=$1`, activity, time.Now().Add(5*time.Minute)); err != nil {
				t.Fatal(err)
			}
			out := restTransportCall(t, s, owner, fmt.Sprintf("%s/activities/%d/arrived/", prefix, activity), map[string]any{})
			if out.Code != http.StatusOK {
				t.Errorf("own arrival REST status=%d, want200", out.Code)
			}
			var arrived *time.Time
			if err := s.DB.QueryRow(ctx, `SELECT arrived_at FROM social_membership WHERE activity_id=$1 AND user_id=$2`, activity, owner.ID).Scan(&arrived); err != nil || arrived == nil {
				t.Errorf("own arrival REST200 did not persist arrival: %v, %v", arrived, err)
			}
		})
		for _, action := range []string{"arrived", "transit"} {
			t.Run(prefix+"/guardian_"+action, func(t *testing.T) {
				s, _ := testStore(t)
				ctx := context.Background()
				ward := fixtureUser(t, s, "rest-presence-ward", "child")
				guardian := fixtureUser(t, s, "rest-presence-guardian", "adult")
				var consent int64
				if err := s.DB.QueryRow(ctx, `UPDATE accounts_parentalconsent SET guardian_identifier=$2 WHERE minor_id=$1 AND status='active' RETURNING id`, ward.ID, guardian.PublicID).Scan(&consent); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,consent_id,created_at,updated_at) VALUES($1,$2,'parent','active',$3,now(),now())`, guardian.ID, ward.ID, consent); err != nil {
					t.Fatal(err)
				}
				place := testdb.Place(t, s.DB, "Synthetic approved presence venue", "osm")
				if _, err := s.DB.Exec(ctx, `INSERT INTO places_approvedchildvenue(place_id,approved_by_id,note,created_at) VALUES($1,NULL,'Synthetic exact venue approval',now())`, place); err != nil {
					t.Fatal(err)
				}
				var typ int64
				if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
					t.Fatal(err)
				}
				start := time.Now().Add(5 * time.Minute)
				activity, err := s.CreateActivity(ctx, ward, ActivityInput{Place: place, ActivityType: typ, Title: "Synthetic eligible child presence", StartsAt: start})
				if err != nil {
					t.Fatal(err)
				}
				var eligible bool
				if err := s.DB.QueryRow(ctx, `SELECT w.is_active AND w.is_identity_verified AND w.cohort='child' AND g.is_active AND g.is_identity_verified AND g.cohort='adult' AND EXISTS(SELECT 1 FROM accounts_guardianrelationship r WHERE r.guardian_id=g.id AND r.ward_id=w.id AND r.status='active') AND EXISTS(SELECT 1 FROM accounts_parentalconsent c WHERE c.minor_id=w.id AND c.status='active' AND c.expires_at>now()) AND EXISTS(SELECT 1 FROM places_approvedchildvenue v WHERE v.place_id=$3) AND NOT EXISTS(SELECT 1 FROM social_membership m WHERE m.activity_id=$4 AND m.user_id=g.id) FROM accounts_user w JOIN accounts_user g ON g.id=$2 WHERE w.id=$1`, ward.ID, guardian.ID, place, activity).Scan(&eligible); err != nil || !eligible {
					t.Fatal("guardian/ward consent/cohort/venue/nonmember fixture is not eligible", eligible, err)
				}
				if err := platform.Participate(ctx, s.DB, ward); err != nil || !s.presenceWindow(activityState{StartsAt: start}, s.Now(), false) {
					t.Fatal("ward participation/presence window precondition", err)
				}
				path := fmt.Sprintf("%s/activities/%d/%s/", prefix, activity, action)
				body := map[string]any{"on_behalf_of": ward.PublicID}
				if action == "transit" {
					body["status"] = "on_my_way"
				}
				out := restTransportCall(t, s, guardian, path, body)
				if out.Code != http.StatusNotFound {
					t.Errorf("guardian %s delegated through REST: status=%d, want404", action, out.Code)
				}
				var arrived *time.Time
				var transit string
				if err := s.DB.QueryRow(ctx, `SELECT arrived_at,transit_status FROM social_membership WHERE activity_id=$1 AND user_id=$2`, activity, ward.ID).Scan(&arrived, &transit); err != nil {
					t.Fatal(err)
				}
				if arrived != nil || transit != "none" {
					t.Errorf("guardian REST refusal mutated ward presence: arrival=%v transit=%q", arrived, transit)
				}
				// Same-action own positive control excludes wrong IDs, lapsed consent,
				// unknown venue and a closed window as reasons for the preceding404.
				ownBody := map[string]any{}
				if action == "transit" {
					ownBody["status"] = "on_my_way"
				}
				own := restTransportCall(t, s, ward, path, ownBody)
				if own.Code != http.StatusOK {
					t.Errorf("eligible ward own %s positive control=%d, want200", action, own.Code)
				}
				if err := s.DB.QueryRow(ctx, `SELECT arrived_at,transit_status FROM social_membership WHERE activity_id=$1 AND user_id=$2`, activity, ward.ID).Scan(&arrived, &transit); err != nil {
					t.Fatal(err)
				}
				if action == "arrived" && arrived == nil || action == "transit" && transit != "on_my_way" {
					t.Errorf("ward own %s positive control not persisted: arrival=%v transit=%q", action, arrived, transit)
				}
			})
		}
	}
}
