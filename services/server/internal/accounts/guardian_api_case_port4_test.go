package accounts

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestPrivacyCasePort4OwnAccountAPIRequiresAuthenticationAndReportsAdultAuthority(t *testing.T) {
	s := accountFixture(t)
	if out := accountRequest(s, platform.Actor{}, "GET", "/api/accounts/me/", ""); out.Code != 401 && out.Code != 403 {
		t.Fatal("anonymous own-profile API was not gated", out.Code)
	}
	a := accountUser(t, s, "privacy-case4-own-adult", "adult", "adult")
	out := accountRequest(s, a, "GET", "/api/accounts/me/", "")
	if out.Code != 200 {
		t.Fatal("own-profile API refused current adult", out.Code)
	}
	body := accountJSON(t, out)
	if body["username"] != a.Username || body["cohort"] != "adult" || body["can_participate"] != true || body["requires_parental_consent"] != false {
		t.Fatal("own adult API profile/participation fields drifted")
	}
	if !a.Admin() && a.Moderator() {
		t.Fatal("ordinary actor unexpectedly had moderator capability")
	}
	for _, scenario := range []struct {
		role             string
		admin, moderator bool
	}{{"admin", true, true}, {"moderator", false, true}, {"user", false, false}} {
		actor := platform.Actor{Role: scenario.role, IsActive: true}
		if actor.Admin() != scenario.admin || actor.Moderator() != scenario.moderator {
			t.Fatal("native role-helper truth table drifted", scenario.role)
		}
	}
}

func TestPrivacyCasePort4GuardianOwnFlagWardListPatchAndStrangerVeto(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	g := accountUser(t, s, "privacy-case4-role-guardian", "adult", "adult")
	w := accountUser(t, s, "privacy-case4-role-ward", "under_16", "child")
	stranger := accountUser(t, s, "privacy-case4-role-stranger", "adult", "adult")
	token := casePortGuardianInvite(t, s, g, w)
	if out := accountRequest(s, w, "POST", "/api/accounts/guardian-links/"+token+"/accept/", `{}`); out.Code != 200 {
		t.Fatal("mutual guardian ceremony failed", out.Code)
	}
	if out := accountRequest(s, g, "GET", "/api/accounts/me/", ""); out.Code != 200 || accountJSON(t, out)["role"] != "user" || accountJSON(t, out)["is_guardian"] != true {
		t.Fatal("actual ownAPI omitted role/guardian flag", out.Code)
	}
	listing := accountRequest(s, g, "GET", "/api/accounts/wards/", "")
	var wards []map[string]any
	if listing.Code != 200 || json.Unmarshal(listing.Body.Bytes(), &wards) != nil || len(wards) != 1 || wards[0]["username"] != w.Username {
		t.Fatal("actual ward list leaked other accounts or omitted ward", listing.Code)
	}
	path := "/api/accounts/wards/" + w.PublicID + "/"
	if out := accountRequest(s, g, "PATCH", path, `{"display_name":"Kiddo"}`); out.Code != 200 {
		t.Fatal("authorized ward profile edit failed", out.Code)
	}
	var display string
	if err := s.DB.QueryRow(context.Background(), `SELECT display_name FROM accounts_user WHERE id=$1`, w.ID).Scan(&display); err != nil || display != "Kiddo" {
		t.Fatal("ward display edit was not durable", err)
	}
	if out := accountRequest(s, stranger, "GET", path, ""); out.Code != 403 {
		t.Fatal("non-guardian wardGET status drifted", out.Code)
	}
	if out := accountRequest(s, stranger, "PATCH", path, `{"display_name":"Unrelated replacement"}`); out.Code != 403 {
		t.Fatal("non-guardian wardPATCH status drifted", out.Code)
	}
	if err := s.DB.QueryRow(context.Background(), `SELECT display_name FROM accounts_user WHERE id=$1`, w.ID).Scan(&display); err != nil || display != "Kiddo" {
		t.Fatal("stranger changed protected ward display", err)
	}
	crypto := &casePortGuardianMessaging{}
	if err := s.RevokeGuardian(context.Background(), g, w.ID, crypto); err != nil {
		t.Fatal(err)
	}
	var active int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$2 AND status='active'`, g.ID, w.ID).Scan(&active); err != nil || active != 0 {
		t.Fatal("guardian revocation left active link", err)
	}
	if out := accountRequest(s, g, "GET", "/api/accounts/me/", ""); out.Code != 200 || accountJSON(t, out)["is_guardian"] != false {
		t.Fatal("revoked ownAPI retained guardian flag", out.Code)
	}
}

func TestPrivacyCasePort4ConsentCurrentExpiredRevokedAndIdentityParticipation(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	child := accountUser(t, s, "privacy-case4-consent-child", "under_16", "child")
	adult := accountUser(t, s, "privacy-case4-consent-adult", "adult", "adult")
	unverified := accountUser(t, s, "privacy-case4-consent-unverified", "adult", "adult")
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_identity_verified=false WHERE id=$1`, unverified.ID); err != nil {
		t.Fatal(err)
	}
	unverified.IdentityVerified = false
	if err := platform.Participate(ctx, s.DB, adult); err != nil {
		t.Fatal("current verified adult could not participate", err)
	}
	if err := platform.Participate(ctx, s.DB, unverified); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("unverified adult participated", err)
	}
	if err := platform.Participate(ctx, s.DB, child); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("verified minor participated without consent", err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_parentalconsent(minor_id,guardian_identifier,status,scope,granted_at,expires_at,revoked_at,renewal_notice,created_at,updated_at) VALUES($1,'synthetic-case4-consent','active','',now(),NULL,NULL,'',now(),now())`, child.ID); err != nil {
		t.Fatal(err)
	}
	if err := platform.Participate(ctx, s.DB, child); err != nil {
		t.Fatal("non-expiring active consent was not valid", err)
	}
	for _, scenario := range []struct {
		name   string
		expiry *time.Time
		status string
	}{{"expired", casePortTime(time.Now().Add(-time.Hour)), "active"}, {"revoked", nil, "revoked"}} {
		t.Run(scenario.name, func(t *testing.T) {
			if _, err := s.DB.Exec(ctx, `UPDATE accounts_parentalconsent SET expires_at=$2,status=$3 WHERE minor_id=$1`, child.ID, scenario.expiry, scenario.status); err != nil {
				t.Fatal(err)
			}
			if err := platform.Participate(ctx, s.DB, child); !errors.Is(err, platform.ErrForbidden) {
				t.Fatal("expired/revoked consent retained participation", err)
			}
		})
	}
	if out := accountRequest(s, child, "GET", "/api/accounts/me/", ""); out.Code != 200 || accountJSON(t, out)["cohort"] != "child" || accountJSON(t, out)["requires_parental_consent"] != true {
		t.Fatal("cohort/consent requirement disclosure drifted", out.Code)
	}
}

func TestPrivacyCasePort4SelfGuardianInviteCannotCreateAuthority(t *testing.T) {
	s := accountFixture(t)
	s.Config.AllowMinorOnboarding = true
	a := accountUser(t, s, "privacy-case4-self-guardian", "adult", "adult")
	if out := accountRequest(s, a, "POST", "/api/accounts/guardian-links/", `{"ward":"`+a.PublicID+`"}`); out.Code < 400 || out.Code >= 500 {
		t.Fatal("self guardian invitation not refused", out.Code)
	}
	var links int
	if err := s.DB.QueryRow(context.Background(), `SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 AND ward_id=$1`, a.ID).Scan(&links); err != nil || links != 0 {
		t.Fatal("self ceremony persisted guardian authority", err)
	}
}
