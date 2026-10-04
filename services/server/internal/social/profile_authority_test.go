package social

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

const profilePrivateInterest = "Synthetic private target interest"

func profileAuthorityStore(t *testing.T) *Service {
	t.Helper()
	s, _ := testStore(t)
	s.Avatar = func(context.Context, platform.Querier, int64) (string, error) {
		return "data:image/svg+xml,synthetic-profile-avatar", nil
	}
	return s
}

func profileAcceptedPair(t *testing.T, s *Service, viewer, target Actor) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO connections_connection(requester_id,addressee_id,status,created_at,decided_at) VALUES($1,$2,'accepted',now(),now())`, viewer.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(context.Background(), `UPDATE taxonomy_activitytype SET name=$1 WHERE slug='basketball'`, profilePrivateInterest); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO recommendations_userinterest(user_id,activity_type_id,created_at) SELECT $1,id,now() FROM taxonomy_activitytype WHERE slug='basketball'`, target.ID); err != nil {
		t.Fatal(err)
	}
}

// The real independently committed admission changes the viewer before Profile
// resolves visibility. Only disposable fixture rows and a fixed test scope are used.
func profileAdmissionMutation(t *testing.T, s *Service, assignment string) {
	t.Helper()
	query := fmt.Sprintf(`CREATE OR REPLACE FUNCTION fixture_profile_authority_change() RETURNS trigger LANGUAGE plpgsql AS $fixture$
 BEGIN
  IF NEW.scope='social.profile_card' AND NEW.user_id IS NOT NULL THEN
   UPDATE accounts_user SET %s WHERE id=NEW.user_id;
  END IF;
  RETURN NEW;
 END $fixture$;
 DROP TRIGGER IF EXISTS fixture_profile_authority_change ON go_rate_budget;
 CREATE TRIGGER fixture_profile_authority_change AFTER INSERT OR UPDATE ON go_rate_budget FOR EACH ROW EXECUTE FUNCTION fixture_profile_authority_change()`, assignment)
	if _, err := s.DB.Exec(context.Background(), query); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresProfileReloadAfterAdmissionVetoesStaleConnectedViewer(t *testing.T) {
	s := profileAuthorityStore(t)
	ctx := context.Background()
	viewer := fixtureUser(t, s, "profile-authority-viewer", "adult")
	target := fixtureUser(t, s, "profile-authority-target", "adult")
	profileAcceptedPair(t, s, viewer, target)
	baseline, err := s.Profile(ctx, viewer, target.PublicID)
	if err != nil || baseline["tier"] != "connected" || baseline["show_photo"] != true {
		t.Fatal("accepted adult baseline did not expose its reviewed connected shape", err)
	}
	interests, ok := baseline["interests"].([]string)
	if !ok || len(interests) != 1 || interests[0] != profilePrivateInterest {
		t.Fatal("private target-interest sentinel was not reachable at baseline")
	}
	for _, change := range []struct{ name, assignment string }{
		{"inactive", `is_active=false`},
		{"cross-cohort", `age_band='16_17',cohort='teen'`},
		{"unassigned", `age_band='unknown',cohort='unassigned',is_identity_verified=false`},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true,age_band='adult',cohort='adult',is_identity_verified=true WHERE id=$1`, viewer.ID); err != nil {
				t.Fatal(err)
			}
			profileAdmissionMutation(t, s, change.assignment)
			card, err := s.Profile(ctx, viewer, target.PublicID)
			if !errors.Is(err, platform.ErrNotFound) || card != nil {
				t.Fatal("post-admission authority change exposed connected profile fields", err)
			}
		})
	}
	var debits int
	if err := s.DB.QueryRow(ctx, `SELECT cardinality(events) FROM go_rate_budget WHERE scope='social.profile_card' AND user_id=$1`, viewer.ID).Scan(&debits); err != nil || debits != 4 {
		t.Fatalf("visibility veto did not follow committed real admission: debits=%d err=%v", debits, err)
	}
}

func TestPostgresProfileFreshMinorClampReplacesCapturedAdultFlags(t *testing.T) {
	s := profileAuthorityStore(t)
	ctx := context.Background()
	capturedAdult := fixtureUser(t, s, "profile-captured-adult", "adult")
	target := fixtureUser(t, s, "profile-current-teen-target", "teen")
	profileAcceptedPair(t, s, capturedAdult, target)
	profileAdmissionMutation(t, s, `age_band='16_17',cohort='teen',role='user',is_staff=false,is_superuser=false`)
	// Supplied privileges and cohort are deliberately stale. The database has
	// moved this viewer into the target's minor cohort at the admission boundary.
	capturedAdult.Role, capturedAdult.IsStaff, capturedAdult.IsSuperuser = "admin", true, true
	card, err := s.Profile(ctx, capturedAdult, target.PublicID)
	if err != nil || card["tier"] != "connected" || card["minor"] != true || card["show_photo"] != false || card["interests"] != nil {
		t.Fatal("profile used captured adult flags instead of current minor clamp", err)
	}
	if card["can_message"] != true || card["can_connect"] != false {
		t.Fatal("reload changed accepted-relationship affordance semantics")
	}
	for _, field := range []string{"age_band", "cohort", "role", "is_staff", "is_superuser"} {
		if _, exists := card[field]; exists {
			t.Fatal("profile exposed authority fields", field)
		}
	}
}

func TestPostgresProfileTargetVetoesRemainIndistinguishable(t *testing.T) {
	s := profileAuthorityStore(t)
	ctx := context.Background()
	viewer := fixtureUser(t, s, "profile-veto-viewer", "adult")
	target := fixtureUser(t, s, "profile-veto-target", "adult")
	veto := func(card map[string]any, err error) bool {
		if card != nil || err == nil {
			return false
		}
		response := httptest.NewRecorder()
		platform.Fail(response, err)
		return response.Code == 404 && response.Body.String() == "{\"detail\":\"Not found.\"}\n"
	}
	for _, public := range []string{viewer.PublicID, "00000000-0000-4000-8000-000000000000"} {
		if card, err := s.Profile(ctx, viewer, public); !veto(card, err) {
			t.Fatal("self or nonexistent profile did not collapse to 404", err)
		}
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	if card, err := s.Profile(ctx, viewer, target.PublicID); !veto(card, err) {
		t.Fatal("inactive target profile did not collapse to 404", err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=true WHERE id=$1`, target.ID); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]int64{{viewer.ID, target.ID}, {target.ID, viewer.ID}} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, ids[0], ids[1]); err != nil {
			t.Fatal(err)
		}
		if card, err := s.Profile(ctx, viewer, target.PublicID); !veto(card, err) {
			t.Fatal("mutually blocked target profile did not collapse to 404", err)
		}
		if _, err := s.DB.Exec(ctx, `DELETE FROM safety_block WHERE blocker_id=$1 AND blocked_id=$2`, ids[0], ids[1]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgresProfileWithdrawnIdentityKeepsSharedCardWithoutConnecting(t *testing.T) {
	s := profileAuthorityStore(t)
	ctx := context.Background()
	viewer := fixtureUser(t, s, "profile-unverified-viewer", "adult")
	target := fixtureUser(t, s, "profile-unverified-peer", "adult")
	stranger := fixtureUser(t, s, "profile-unverified-stranger", "adult")
	activity := fixtureActivity(t, s, viewer, nil)
	profileSharedPeer(t, s, activity, target.ID)
	profileAdmissionMutation(t, s, `is_identity_verified=false,role='user',is_staff=false,is_superuser=false`)
	viewer.Role, viewer.IsStaff, viewer.IsSuperuser = "admin", true, true
	card, err := s.Profile(ctx, viewer, target.PublicID)
	if err != nil || card["tier"] != "shared" || card["can_connect"] != false || card["show_photo"] != false || card["interests"] != nil {
		t.Fatal("captured identity/role restored connecting or erased source-policy shared card", err)
	}
	fresh, err := actorByID(ctx, s.DB, viewer.ID)
	if err != nil || fresh.IdentityVerified || fresh.Role != "user" || fresh.IsStaff || fresh.IsSuperuser {
		t.Fatal("admission did not apply current authority fixture")
	}
	minimal, err := s.Profile(ctx, viewer, stranger.PublicID)
	if err != nil || minimal["tier"] != "stranger" || len(minimal) != 5 {
		t.Fatal("withdrawn identity erased or broadened the source-policy minimal card", err)
	}
}

func profileSharedPeer(t *testing.T, s *Service, activity, user int64) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO social_membership(activity_id,user_id,role,state,created_at,updated_at,decided_at,attendance_intent,transit_status,brings_support_person) VALUES($1,$2,'member','member',now(),now(),now(),'unknown','none',false)`, activity, user); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresProfileLapsedConsentKeepsMinimalAndLiveSharedVisibility(t *testing.T) {
	s := profileAuthorityStore(t)
	ctx := context.Background()
	viewer := fixtureUser(t, s, "profile-consent-viewer", "child")
	peer := fixtureUser(t, s, "profile-consent-peer", "child")
	stranger := fixtureUser(t, s, "profile-consent-stranger", "child")
	activity := fixtureActivity(t, s, viewer, nil)
	profileSharedPeer(t, s, activity, peer.ID)
	for _, change := range []struct{ name, query string }{
		{"expired", `UPDATE accounts_parentalconsent SET status='active',expires_at=now()-interval '1 hour' WHERE minor_id=$1`},
		{"revoked", `UPDATE accounts_parentalconsent SET status='revoked',revoked_at=now() WHERE minor_id=$1`},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := s.DB.Exec(ctx, change.query, viewer.ID); err != nil {
				t.Fatal(err)
			}
			for _, target := range []Actor{stranger, peer} {
				card, err := s.Profile(ctx, viewer, target.PublicID)
				if err != nil || card["minor"] != true {
					t.Fatal("consent expiry erased source-policy same-cohort profile", err)
				}
				if target.ID == stranger.ID {
					if card["tier"] != "stranger" || len(card) != 5 {
						t.Fatal("lapsed-consent stranger exceeded minimal field cap")
					}
				} else if card["tier"] != "shared" || card["can_connect"] != false || card["interests"] != nil || card["show_photo"] != false {
					t.Fatal("lapsed consent enabled connecting or changed live shared tier")
				}
				for _, field := range []string{"age_band", "cohort", "progression", "last_seen"} {
					if _, exists := card[field]; exists {
						t.Fatal("profile exposed forbidden fields", field)
					}
				}
				if avatar, _ := card["avatar"].(string); !strings.Contains(avatar, "synthetic-profile-avatar") {
					t.Fatal("minimal/shared profile omitted generated avatar")
				}
			}
		})
	}
}
