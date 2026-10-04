package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/flosch/pongo2/v6"
)

// Exercise the registered API and legacy HTML adapters with a captured actor,
// then withdraw its database authority before the next profile disclosure.
// All accounts, connections and declared interests are isolated test fixtures.
func TestProfileRoutesRejectStaleViewerAuthority(t *testing.T) {
	mutations := []struct {
		name, query string
	}{
		{"inactive", `UPDATE accounts_user SET is_active=false WHERE id=$1`},
		{"unassigned", `UPDATE accounts_user SET cohort='unassigned' WHERE id=$1`},
		{"cohort_changed", `UPDATE accounts_user SET cohort='teen' WHERE id=$1`},
	}
	for _, cohort := range []string{"adult", "child"} {
		for _, mutation := range mutations {
			t.Run(cohort+"/"+mutation.name, func(t *testing.T) {
				s, viewer, target, interest, mux := profileAuthorityFixture(t, cohort)
				avatar, err := accounts.Avatar(context.Background(), s.DB, target.ID)
				if err != nil {
					t.Fatal(err)
				}
				paths := profileAuthorityPaths(target.PublicID)
				for _, path := range paths {
					response := profileAuthorityRead(mux, viewer, path)
					if response.Code != http.StatusOK {
						t.Fatalf("fresh same-cohort profile %s: status %d", path, response.Code)
					}
					if strings.HasPrefix(path, "/api/") {
						var card map[string]any
						if err := json.Unmarshal(response.Body.Bytes(), &card); err != nil {
							t.Fatal(err)
						}
						if card["tier"] != "connected" || card["username"] != target.Username {
							t.Fatal("accepted fixture connection did not expose the guarded handle")
						}
						if cohort == "adult" && !strings.Contains(response.Body.String(), interest) {
							t.Fatal("adult fixture did not exercise declared-interest disclosure")
						}
						if cohort == "child" && (card["interests"] != nil || card["show_photo"] != false || strings.Contains(response.Body.String(), interest)) {
							t.Fatal("connected child fixture escaped the profile clamp")
						}
					} else {
						profileAuthorityHTMLFields(t, response, target, avatar, interest, true, cohort == "adult")
						card := profileAuthorityContext(t, s, viewer, target, strings.HasSuffix(path, "/card/"))
						if card["tier"] != "connected" || card["username"] != target.Username {
							t.Fatal("HTML context lost the authorized connected card")
						}
					}
				}
				profileAuthorityMutationAfterAdmission(t, s, mutation.query)
				// Retain the original active/verified/cohort snapshot deliberately:
				// adapters must not turn captured request context into authority.
				for _, path := range paths {
					response := profileAuthorityRead(mux, viewer, path)
					if response.Code != http.StatusNotFound {
						t.Errorf("stale viewer profile %s: status %d, want 404", path, response.Code)
					}
					for _, private := range []string{target.Username, interest, target.PublicID, target.DisplayName} {
						if strings.Contains(response.Body.String(), private) {
							t.Errorf("stale viewer response %s disclosed target profile data", path)
						}
					}
				}
			})
		}
	}
}

func TestProfileRoutesKeepMinimalAndSharedCapsAfterIdentityOrConsentWithdrawal(t *testing.T) {
	cases := []struct {
		name, cohort, mutation string
	}{
		{"adult_identity", "adult", `UPDATE accounts_user SET is_identity_verified=false,role='user',is_staff=false,is_superuser=false WHERE id=$1`},
		{"child_identity", "child", `UPDATE accounts_user SET is_identity_verified=false,role='user',is_staff=false,is_superuser=false WHERE id=$1`},
		{"child_consent_expired", "child", `UPDATE accounts_parentalconsent SET status='active',expires_at=now()-interval '1 hour' WHERE minor_id=$1`},
		{"child_consent_revoked", "child", `UPDATE accounts_parentalconsent SET status='revoked',revoked_at=now() WHERE minor_id=$1`},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			s, viewer, peer, interest, mux := profileAuthorityFixture(t, scenario.cohort)
			sharedTitle := profileAuthoritySharedActivity(t, s, viewer, peer)
			stranger := testdb.Actor(t, s.DB, "private-stranger-profile-handle", scenario.cohort)
			stranger.DisplayName = "Stranger profile display"
			if _, err := s.DB.Exec(context.Background(), `UPDATE accounts_user SET display_name=$2 WHERE id=$1`, stranger.ID, stranger.DisplayName); err != nil {
				t.Fatal(err)
			}
			baseline := profileAuthorityJSON(t, profileAuthorityRead(mux, viewer, profileAuthorityPaths(peer.PublicID)[0]))
			if baseline["tier"] != "shared" || baseline["can_connect"] != true {
				t.Fatal("live shared fixture did not exercise the connect affordance")
			}
			profileAuthorityMutationAfterAdmission(t, s, scenario.mutation)
			// Deliberately forged captured role/verification flags must not restore
			// the affordance after the database authority has been withdrawn.
			viewer.Role, viewer.IsStaff, viewer.IsSuperuser = "admin", true, true
			for _, target := range []platform.Actor{stranger, peer} {
				avatar, err := accounts.Avatar(context.Background(), s.DB, target.ID)
				if err != nil {
					t.Fatal(err)
				}
				shared := target.ID == peer.ID
				for _, path := range profileAuthorityPaths(target.PublicID) {
					response := profileAuthorityRead(mux, viewer, path)
					if response.Code != http.StatusOK {
						t.Fatalf("withdrawn participation erased visible profile %s: status %d", path, response.Code)
					}
					var card map[string]any
					if strings.HasPrefix(path, "/api/") {
						card = profileAuthorityJSON(t, response)
					} else {
						profileAuthorityHTMLFields(t, response, target, avatar, interest, shared, false)
						card = profileAuthorityContext(t, s, viewer, target, strings.HasSuffix(path, "/card/"))
						if strings.Contains(response.Body.String(), `action="/connections/request/"`) {
							t.Fatal("withdrawn participation rendered a connect form")
						}
					}
					if shared {
						if card["tier"] != "shared" || card["can_connect"] != false || card["interests"] != nil || card["show_photo"] != false || card["username"] != target.Username {
							t.Fatal("withdrawn participation broadened or erased the live shared card")
						}
						if !strings.Contains(response.Body.String(), sharedTitle) {
							t.Fatal("shared profile lost its authorized context")
						}
					} else if card["tier"] != "stranger" || len(card) != 5 {
						t.Fatal("withdrawn participation broadened the minimal card")
					}
					if card["minor"] != (scenario.cohort == "child") {
						t.Fatal("transport lost the current minor clamp")
					}
				}
			}
		})
	}
}

func TestProfilePagePhotosUseRegisteredMediaAuthority(t *testing.T) {
	for _, cohort := range []string{"adult", "child"} {
		t.Run(cohort, func(t *testing.T) {
			s, viewer, target, _, mux := profileAuthorityFixture(t, cohort)
			const privateKey = "photos/private-profile-fixture.webp"
			if _, err := s.DB.Exec(context.Background(), `INSERT INTO media_photo(kind,storage_key,content_type,byte_size,sha256,width,height,scan_status,exif_stripped,created_at,thread_id,uploader_id,phash,thumb_storage_key) VALUES('profile',$1,'image/webp',1,repeat('a',64),1,1,'clean',true,now(),NULL,$2,'','')`, privateKey, target.ID); err != nil {
				t.Fatal(err)
			}
			for _, path := range profileAuthorityPaths(target.PublicID) {
				response := profileAuthorityRead(mux, viewer, path)
				if response.Code != http.StatusOK {
					t.Fatalf("connected profile with fixture photo: status %d", response.Code)
				}
				wantPhoto := cohort == "adult" && path == "/people/"+target.PublicID+"/"
				if strings.Contains(response.Body.String(), "/api/media/file/") != wantPhoto || strings.Contains(response.Body.String(), privateKey) {
					t.Fatal("uploaded photo escaped its full-page adult surface or exposed storage data")
				}
			}
			if cohort != "adult" {
				return
			}
			card, err := s.Social.Profile(context.Background(), viewer, target.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.DB.Exec(context.Background(), `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, viewer.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			request := platform.WithActor(httptest.NewRequest(http.MethodGet, "https://fixture.local/people/"+target.PublicID+"/", nil), viewer)
			data := pongo2.Context{}
			err = s.populatePersonContext(request, card, data, true)
			if !errors.Is(err, platform.ErrNotFound) || data["photo_url"] != nil {
				t.Fatal("photo context reused profile authorization after current media access was blocked")
			}
			if response := profileAuthorityRead(mux, viewer, request.URL.Path); response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), target.Username) {
				t.Fatal("blocked full-page profile escaped its current read gate")
			}
		})
	}
}

func profileAuthoritySharedActivity(t *testing.T, s *Server, viewer, peer platform.Actor) string {
	t.Helper()
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `DELETE FROM connections_connection WHERE requester_id=$1 AND addressee_id=$2`, viewer.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	var place, activityType int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM places_place WHERE name='Library & Hall'`).Scan(&place); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&activityType); err != nil {
		t.Fatal(err)
	}
	if viewer.Cohort == "child" {
		if _, err := s.DB.Exec(ctx, `INSERT INTO places_approvedchildvenue(place_id,approved_by_id,note,created_at) VALUES($1,NULL,'Isolated profile fixture venue',now()) ON CONFLICT(place_id) DO NOTHING`, place); err != nil {
			t.Fatal(err)
		}
	}
	const title = "Shared profile context sentinel"
	activity := socialLegacyActivity(t, s, viewer, place, activityType, title)
	if _, err := s.DB.Exec(ctx, `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,'member','member','unknown','none',false,now(),now(),now())`, activity, peer.ID); err != nil {
		t.Fatal(err)
	}
	return title
}

func profileAuthorityJSON(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("profile JSON: status %d", response.Code)
	}
	var card map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &card); err != nil {
		t.Fatal(err)
	}
	return card
}

func profileAuthorityHTMLFields(t *testing.T, response *httptest.ResponseRecorder, target platform.Actor, avatar, interest string, handle, interests bool) {
	t.Helper()
	body := response.Body.String()
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") || !strings.Contains(body, target.DisplayName) || !strings.Contains(body, avatar) {
		t.Fatal("HTML profile omitted its authorized display or generated avatar")
	}
	if strings.Contains(body, target.Username) != handle || strings.Contains(body, interest) != interests {
		t.Fatal("HTML profile did not retain the source card's handle/interest cap")
	}
}

func profileAuthorityContext(t *testing.T, s *Server, viewer, target platform.Actor, fragment bool) map[string]any {
	t.Helper()
	name, path := "person", "/people/"+target.PublicID+"/"
	if fragment {
		name, path = "person_card", path+"card/"
	}
	request := platform.WithActor(httptest.NewRequest(http.MethodGet, "https://fixture.local"+path, nil), viewer)
	request.SetPathValue("public_id", target.PublicID)
	data, _, err := s.view(request, viewer, name)
	if err != nil {
		t.Fatal(err)
	}
	card, ok := data["card"].(map[string]any)
	if !ok || !reflect.DeepEqual(card, data["person"]) || spaID(spaMap(data["person_user"])) != target.ID {
		t.Fatal("HTML context lost its authorized person/card aliases or guarded target ID")
	}
	return card
}

func profileAuthorityMutationAfterAdmission(t *testing.T, s *Server, mutation string) {
	t.Helper()
	// The fixture changes authority when the profile budget commits. A reload
	// before admission would still use the formerly eligible viewer and leak.
	// Each isolated fixture schema owns this function and trigger.
	mutation = strings.ReplaceAll(mutation, "$1", "NEW.user_id")
	for _, query := range []string{
		`CREATE FUNCTION profile_viewer_authority_transition() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN ` + mutation + `; RETURN NEW; END; $$`,
		`CREATE TRIGGER profile_viewer_authority_transition AFTER INSERT OR UPDATE ON go_rate_budget FOR EACH ROW WHEN (NEW.scope='social.profile_card') EXECUTE FUNCTION profile_viewer_authority_transition()`,
	} {
		if _, err := s.DB.Exec(context.Background(), query); err != nil {
			t.Fatal(err)
		}
	}
}

func profileAuthorityFixture(t *testing.T, cohort string) (*Server, platform.Actor, platform.Actor, string, *http.ServeMux) {
	t.Helper()
	s, _, _, activityType := socialLegacyFixture(t)
	viewer := testdb.Actor(t, s.DB, "profile-authority-viewer-"+cohort, cohort)
	target := testdb.Actor(t, s.DB, "private-profile-target-handle-"+cohort, cohort)
	target.DisplayName = "Profile target display"
	interest := "Private profile declared interest sentinel"
	ctx := context.Background()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE accounts_user SET display_name=$2 WHERE id=$1`, []any{target.ID, target.DisplayName}},
		{`UPDATE taxonomy_activitytype SET name=$2 WHERE id=$1`, []any{activityType, interest}},
		{`INSERT INTO recommendations_userinterest(user_id,activity_type_id,created_at) VALUES($1,$2,now())`, []any{target.ID, activityType}},
		{`INSERT INTO connections_connection(requester_id,addressee_id,status,created_at,decided_at) VALUES($1,$2,'accepted',now(),now())`, []any{viewer.ID, target.ID}},
	} {
		if _, err := s.DB.Exec(ctx, statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	// Authentication is not exercised or fabricated here. The real auth core is
	// supplied solely for the legacy renderer's ordinary CSRF-cookie creation.
	var err error
	s.Auth, err = authcore.New(authcore.Config{PublicURL: s.Config.PublicURL}, accounts.NewStore(s.DB))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	s.Social.Register(mux)
	s.Media.Register(mux)
	s.Register(mux)
	s.API = mux
	return s, viewer, target, interest, mux
}

func profileAuthorityPaths(publicID string) []string {
	return []string{
		"/api/connections/people/" + publicID + "/",
		"/api/v1/connections/people/" + publicID + "/",
		"/people/" + publicID + "/",
		"/people/" + publicID + "/card/",
	}
}

func profileAuthorityRead(mux *http.ServeMux, viewer platform.Actor, path string) *httptest.ResponseRecorder {
	request := platform.WithActor(httptest.NewRequest(http.MethodGet, "https://fixture.local"+path, nil), viewer)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}
