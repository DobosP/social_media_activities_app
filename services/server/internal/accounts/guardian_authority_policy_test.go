package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

// Owner decision (ADR-0045): a link that outlived the ward's minority never
// grants guardian authority, even if revocation somehow did not run.
func TestGuardianAuthorityPolicyAdultWardRowRefusesLeftoverLink(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	ctx := context.Background()
	g := accountUser(t, s, "policy-leftover-guardian", "adult", "adult")
	w := accountUser(t, s, "policy-leftover-ward", "16_17", "teen")
	token := casePortGuardianInvite(t, s, g, w)
	if out := accountRequest(s, w, "POST", "/api/accounts/guardian-links/"+token+"/accept/", `{}`); out.Code != 200 {
		t.Fatal("ward ceremony failed", out.Code)
	}
	path := "/api/accounts/wards/" + w.PublicID + "/"
	if out := accountRequest(s, g, "GET", path+"export/", ""); out.Code != 200 {
		t.Fatal("current teen ward export refused", out.Code)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET age_band='adult',cohort='adult' WHERE id=$1`, w.ID); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ method, path, body string }{
		{"GET", path, ""},
		{"PATCH", path, `{"display_name":"Renamed by former guardian"}`},
		{"GET", path + "export/", ""},
		{"DELETE", path, ""},
	} {
		if out := accountRequest(s, g, scenario.method, scenario.path, scenario.body); out.Code != 403 {
			t.Fatal("adult ward's leftover link granted guardian authority", scenario.method, scenario.path, out.Code)
		}
	}
	// The captured actor still says teen; Erase must use the current row.
	if err := s.Erase(ctx, g, w); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("erase service honoured a link over a current adult", err)
	}
	casePort2Exists(t, s, w, true)
	var display string
	var active int
	if err := s.DB.QueryRow(ctx, `SELECT u.display_name,(SELECT count(*) FROM accounts_guardianrelationship WHERE ward_id=u.id AND status='active') FROM accounts_user u WHERE u.id=$1`, w.ID).Scan(&display, &active); err != nil || display != w.DisplayName || active != 1 {
		t.Fatal("defensive refusal mutated the adult or the leftover link", err)
	}
}

// Owner decision (ADR-0045): the guardian's ward export omits the child's own
// safety reports entirely; the child's self-export keeps them.
func TestGuardianAuthorityPolicyWardExportOmitsChildsOwnReports(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	ctx := context.Background()
	g := accountUser(t, s, "policy-report-guardian", "adult", "adult")
	w := accountUser(t, s, "policy-report-ward", "under_16", "child")
	peer := accountUser(t, s, "policy-report-peer", "under_16", "child")
	mod := accountUser(t, s, "policy-report-moderator", "adult", "adult")
	token := casePortGuardianInvite(t, s, g, w)
	if out := accountRequest(s, w, "POST", "/api/accounts/guardian-links/"+token+"/accept/", `{}`); out.Code != 200 {
		t.Fatal("ward ceremony failed", out.Code)
	}
	if out := accountRequest(s, g, "POST", "/api/accounts/wards/"+w.PublicID+"/consent/", `{}`); out.Code != 201 {
		t.Fatal("guardian consent failed", out.Code)
	}
	const detail = "synthetic child's own private report words"
	const resolution = "synthetic moderator resolution words"
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_report(reporter_id,target_type_id,target_id,reason,detail,status,handled_by_id,handled_at,resolution,created_at) VALUES($1,(SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'),$2,'grooming',$3,'actioned',NULL,now(),$4,now())`, w.ID, peer.ID, detail, resolution); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_moderationaction(target_id,action,reason,notes,expires_at,created_at,moderator_id,target_type_id,report_id,lifted_at) VALUES($1,'warn','spam','Private moderator notes',NULL,now(),$2,(SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'),NULL,NULL)`, w.ID, mod.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, w.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, path  string
		actor       platform.Actor
		wantReports bool
	}{
		{"guardian", "/api/accounts/wards/" + w.PublicID + "/export/", g, false},
		{"subject", "/api/accounts/me/export/", w, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			out := accountRequest(s, scenario.actor, "GET", scenario.path, "")
			if out.Code != 200 {
				t.Fatal("authorized export refused", out.Code)
			}
			var payload map[string]any
			if err := json.Unmarshal(out.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			record := payload["safety_record"].(map[string]any)
			if len(record["decisions"].([]any)) != 1 || record["decisions_total"] != float64(1) {
				t.Fatal("decisions about the ward's own account were not kept", record["decisions_total"])
			}
			body := out.Body.String()
			_, blocks := payload["blocks"]
			_, concerns := payload["own_sentiment_actions"].(map[string]any)["concerns"]
			if scenario.wantReports != blocks || scenario.wantReports != concerns {
				t.Fatal("blocks/concerns must be omitted from the guardian copy only", blocks, concerns)
			}
			if !scenario.wantReports {
				for _, key := range []string{"reports", "reports_total", "reports_truncated"} {
					if _, present := record[key]; present {
						t.Fatal("guardian ward export revealed the child's reports", key)
					}
				}
				if strings.Contains(body, detail) || strings.Contains(body, resolution) {
					t.Fatal("guardian ward export disclosed child report text")
				}
				return
			}
			reports := record["reports"].([]any)
			if len(reports) != 1 || record["reports_total"] != float64(1) || reports[0].(map[string]any)["detail"] != detail || reports[0].(map[string]any)["resolution"] != resolution {
				t.Fatal("child self-export lost its own reports")
			}
		})
	}
}
