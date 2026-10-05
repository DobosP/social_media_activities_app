package accounts

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/jackc/pgx/v5"
)

func casePortGuardianInvite(t *testing.T, s *Service, guardian, ward platform.Actor) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"ward": ward.PublicID})
	out := accountRequest(s, guardian, http.MethodPost, "/api/accounts/guardian-links/", string(body))
	if out.Code != http.StatusCreated {
		t.Fatalf("synthetic invite status=%d", out.Code)
	}
	return accountJSON(t, out)["token"].(string)
}

func casePortNoGuardianLink(t *testing.T, s *Service, guardian, ward int64) {
	t.Helper()
	var links int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active'`, guardian, ward).Scan(&links); err != nil || links != 0 {
		t.Fatal("refused ceremony left active guardian authority")
	}
}

func TestCasePortGuardianInviteCreationAndAcceptanceAuthority(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	ctx := context.Background()
	for _, scenario := range []struct{ name, guardianBand, guardianCohort, wardBand, wardCohort string }{
		{"teen-inviter", "16_17", "teen", "under_16", "child"},
		{"unverified-inviter", "unknown", "unassigned", "under_16", "child"},
		{"adult-ward", "adult", "adult", "adult", "adult"},
		{"unassigned-ward", "adult", "adult", "unknown", "unassigned"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			g := accountUser(t, s, "case-invite-g-"+scenario.name, scenario.guardianBand, scenario.guardianCohort)
			w := accountUser(t, s, "case-invite-w-"+scenario.name, scenario.wardBand, scenario.wardCohort)
			body, _ := json.Marshal(map[string]string{"ward": w.PublicID})
			if out := accountRequest(s, g, http.MethodPost, "/api/accounts/guardian-links/", string(body)); out.Code < 400 || out.Code >= 500 {
				t.Fatal("ineligible pair created guardian invitation")
			}
			casePortNoGuardianLink(t, s, g.ID, w.ID)
		})
	}
	for _, scenario := range []string{"wrong-addressed-ward", "expired-invite", "inviter-now-teen", "inviter-proof-expired"} {
		t.Run(scenario, func(t *testing.T) {
			g := accountUser(t, s, "case-accept-g-"+scenario, "adult", "adult")
			w := accountUser(t, s, "case-accept-w-"+scenario, "under_16", "child")
			token := casePortGuardianInvite(t, s, g, w)
			caller := w
			switch scenario {
			case "wrong-addressed-ward":
				caller = accountUser(t, s, "case-other-ward", "under_16", "child")
			case "expired-invite":
				if _, err := s.DB.Exec(ctx, `UPDATE accounts_guardianlinkinvite SET expires_at=now()-interval '1 minute' WHERE token=$1`, token); err != nil {
					t.Fatal(err)
				}
			case "inviter-now-teen":
				if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET age_band='16_17',cohort='teen' WHERE id=$1`, g.ID); err != nil {
					t.Fatal(err)
				}
			case "inviter-proof-expired":
				if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'synthetic','fixture','adult',now(),now()-interval '1 day','{}','')`, g.ID); err != nil {
					t.Fatal(err)
				}
			}
			if out := accountRequest(s, caller, http.MethodPost, "/api/accounts/guardian-links/"+token+"/accept/", `{}`); out.Code < 400 || out.Code >= 500 {
				t.Fatal("changed authority or stale invitation created a link")
			}
			casePortNoGuardianLink(t, s, g.ID, w.ID)
			if scenario == "expired-invite" {
				var status string
				if err := s.DB.QueryRow(ctx, `SELECT status FROM accounts_guardianlinkinvite WHERE token=$1`, token).Scan(&status); err != nil || status != "expired" {
					t.Fatal("expired invite did not persist handled status")
				}
			}
		})
	}
}

func TestCasePortMutualGuardianLinkConsentAndDisabledOnboarding(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	g := accountUser(t, s, "case-mutual-guardian", "adult", "adult")
	w := accountUser(t, s, "case-mutual-ward", "under_16", "child")
	body, _ := json.Marshal(map[string]string{"ward": w.PublicID})
	if out := accountRequest(s, g, http.MethodPost, "/api/accounts/guardian-links/", string(body)); out.Code != 400 {
		t.Fatal("disabled minor onboarding allowed guardian creation")
	}
	s.Config.AllowMinorOnboarding = true
	token := casePortGuardianInvite(t, s, g, w)
	casePortNoGuardianLink(t, s, g.ID, w.ID)
	var inviteStatus string
	if err := s.DB.QueryRow(ctx, `SELECT status FROM accounts_guardianlinkinvite WHERE token=$1`, token).Scan(&inviteStatus); err != nil || inviteStatus != "pending" {
		t.Fatal("creation did not retain a pending mutually confirmed ceremony")
	}
	pending := accountRequest(s, w, http.MethodGet, "/api/accounts/guardian-links/", "")
	var invites []map[string]any
	if pending.Code != 200 || json.Unmarshal(pending.Body.Bytes(), &invites) != nil || len(invites) != 1 || invites[0]["token"] != token {
		t.Fatal("addressed ward did not see its pending invitation")
	}
	accepted := accountRequest(s, w, http.MethodPost, "/api/accounts/guardian-links/"+token+"/accept/", `{}`)
	if accepted.Code != 200 || accountJSON(t, accepted)["can_participate"] != false {
		t.Fatal("guardian link alone granted consent/participation")
	}
	var links, audits int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active'),(SELECT count(*) FROM safety_auditlog WHERE event='guardian.link_accepted' AND actor_id=$2)`, g.ID, w.ID).Scan(&links, &audits); err != nil || links != 1 || audits != 1 {
		t.Fatal("accepted link or atomic audit was missing")
	}
	consent := fmt.Sprintf("/api/accounts/wards/%s/consent/", w.PublicID)
	s.Config.AllowMinorOnboarding = false
	if out := accountRequest(s, g, http.MethodPost, consent, `{}`); out.Code != 400 {
		t.Fatal("disabled onboarding granted parental consent")
	}
	s.Config.AllowMinorOnboarding = true
	granted := accountRequest(s, g, http.MethodPost, consent, `{}`)
	if granted.Code != 201 || accountJSON(t, granted)["can_participate"] != true {
		t.Fatal("authorized current guardian could not grant participation")
	}
	var activeConsent int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=$1 AND status='active'`, w.ID).Scan(&activeConsent); err != nil || activeConsent != 1 {
		t.Fatal("successful consent response lacked durable active consent")
	}
	if out := accountRequest(s, g, http.MethodDelete, consent, ""); out.Code != 204 {
		t.Fatal("authorized consent revocation failed")
	}
	if out := accountRequest(s, w, http.MethodGet, "/api/accounts/me/", ""); out.Code != 200 || accountJSON(t, out)["can_participate"] != false {
		t.Fatal("revoked consent retained participation")
	}
	declineWard := accountUser(t, s, "case-decline-ward", "under_16", "child")
	declineToken := casePortGuardianInvite(t, s, g, declineWard)
	if out := accountRequest(s, declineWard, http.MethodPost, "/api/accounts/guardian-links/"+declineToken+"/decline/", `{}`); out.Code != 204 {
		t.Fatal("addressed ward could not decline")
	}
	var status string
	if err := s.DB.QueryRow(ctx, `SELECT status FROM accounts_guardianlinkinvite WHERE token=$1`, declineToken).Scan(&status); err != nil || status != "declined" {
		t.Fatal("decline did not persist status")
	}
	casePortNoGuardianLink(t, s, g.ID, declineWard.ID)
}

func TestCasePortLatestAssuranceExpiryAndLegacyFallback(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	u := accountUser(t, s, "case-assurance-adult", "adult", "adult")
	var proofs int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_ageassurance WHERE user_id=$1`, u.ID).Scan(&proofs); err != nil || proofs != 0 {
		t.Fatal("legacy fallback fixture unexpectedly had an assurance")
	}
	if err := platform.Participate(ctx, s.DB, u); err != nil {
		t.Fatal("verified legacy account without proof lost supported fallback")
	}
	for _, scenario := range []struct {
		name    string
		expires *time.Time
		allowed bool
	}{
		{"future", casePortTime(time.Now().Add(30 * 24 * time.Hour)), true},
		{"non-expiring", nil, true},
		{"expired", casePortTime(time.Now().Add(-time.Minute)), false},
		{"reverified", casePortTime(time.Now().Add(30 * 24 * time.Hour)), true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_ageassurance(user_id,provider,method,age_band,verified_at,expires_at,raw,reverify_notice) VALUES($1,'synthetic','fixture','adult',clock_timestamp(),$2,'{}','')`, u.ID, scenario.expires); err != nil {
				t.Fatal(err)
			}
			err := platform.Participate(ctx, s.DB, u)
			if (err == nil) != scenario.allowed {
				t.Fatal("latest proof expiry did not govern current eligibility", err)
			}
			var verified bool
			if err := s.DB.QueryRow(ctx, `SELECT is_identity_verified FROM accounts_user WHERE id=$1`, u.ID).Scan(&verified); err != nil || !verified {
				t.Fatal("expiry changed the identity flag instead of gating latest proof")
			}
		})
	}
}

func casePortTime(value time.Time) *time.Time { return &value }

// This seam records evictions without claiming observer coverage: these account
// fixtures have no conversations. The messaging case port exercises that graph.
type casePortGuardianMessaging struct{ removals int }

func (*casePortGuardianMessaging) PruneObservers(context.Context, pgx.Tx, int64) error { return nil }
func (m *casePortGuardianMessaging) RemoveUser(context.Context, pgx.Tx, int64, string) error {
	m.removals++
	return nil
}

func TestCasePortGuardianConsentRevocationIsolation(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	ctx := context.Background()
	g1 := accountUser(t, s, "case-isolation-g1", "adult", "adult")
	g2 := accountUser(t, s, "case-isolation-g2", "adult", "adult")
	stranger := accountUser(t, s, "case-isolation-stranger", "adult", "adult")
	w := accountUser(t, s, "case-isolation-ward", "under_16", "child")
	consent := fmt.Sprintf("/api/accounts/wards/%s/consent/", w.PublicID)
	if out := accountRequest(s, stranger, http.MethodPost, consent, `{}`); out.Code < 400 || out.Code >= 500 {
		t.Fatal("unlinked adult granted consent")
	}
	var n int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=$1`, w.ID).Scan(&n); err != nil || n != 0 {
		t.Fatal("non-guardian denial persisted consent")
	}
	for _, guardian := range []platform.Actor{g1, g2} {
		token := casePortGuardianInvite(t, s, guardian, w)
		if out := accountRequest(s, w, http.MethodPost, "/api/accounts/guardian-links/"+token+"/accept/", `{}`); out.Code != 200 {
			t.Fatal("co-guardian ceremony failed")
		}
		if out := accountRequest(s, guardian, http.MethodPost, consent, `{}`); out.Code != 201 {
			t.Fatal("linked adult grant failed")
		}
	}
	crypto := &casePortGuardianMessaging{}
	for index, guardian := range []platform.Actor{g1, g2} {
		if err := s.RevokeGuardian(ctx, guardian, w.ID, crypto); err != nil {
			t.Fatal(err)
		}
		var ownActive, otherActive int
		if err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE guardian_identifier=$2),count(*) FILTER(WHERE guardian_identifier<>$2) FROM accounts_parentalconsent WHERE minor_id=$1 AND status='active'`, w.ID, guardian.PublicID).Scan(&ownActive, &otherActive); err != nil || ownActive != 0 || otherActive != 1-index {
			t.Fatal("revocation did not isolate each guardian's consent")
		}
		if allowed := platform.Participate(ctx, s.DB, w) == nil; allowed != (index == 0) {
			t.Fatal("remaining guardian consent did not control eligibility")
		}
		if crypto.removals != index {
			t.Fatal("ward removed while a current co-guardian consent remained")
		}
	}
}
