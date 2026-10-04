package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DobosP/cat_de_roman_esti/shared-go/authcore"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestAdministratorBootstrapRejectsInvalidInputsBeforeDatabase(t *testing.T) {
	service := &Service{}
	for _, username := range []string{"", "ab", " user", "user ", "bad/name", "bad\nname", strings.Repeat("u", 151)} {
		if _, err := service.BootstrapAdministrator(context.Background(), username, "Synthetic-bootstrap-password"); err == nil || !strings.Contains(err.Error(), "username") {
			t.Fatal("invalid operator username accepted")
		}
	}
	for _, password := range []string{"", "short", strings.Repeat("p", 1025)} {
		if _, err := service.BootstrapAdministrator(context.Background(), "fixture_admin", password); err == nil || !strings.Contains(err.Error(), "password") {
			t.Fatal("invalid operator password accepted")
		}
	}
}
func TestNativeAdministratorBootstrapFreshOnlyUnassignedAndAudited(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	password := "Synthetic-bootstrap-password-9236"
	actor, err := s.BootstrapAdministrator(ctx, "generated_bootstrap_admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if !actor.IsActive || !actor.IsStaff || !actor.IsSuperuser || actor.Role != "admin" || actor.AgeBand != "unknown" || actor.Cohort != "unassigned" || actor.IdentityVerified {
		t.Fatal("operator bootstrap granted nonadministrative identity authority")
	}
	if err = platform.Participate(ctx, s.DB, actor); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("unverified administrator can participate")
	}
	var hash string
	if err = s.DB.QueryRow(ctx, `SELECT password FROM accounts_user WHERE id=$1`, actor.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash == password || !strings.HasPrefix(hash, "pbkdf2_sha256$") {
		t.Fatal("operator password was not hashed")
	}
	valid, err := authcore.VerifyPassword(ctx, password, hash)
	if err != nil || !valid {
		t.Fatal("shared authentication hash is invalid")
	}
	var assurances, consents, relationships, audits int
	if err = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts_ageassurance WHERE user_id=$1),(SELECT count(*) FROM accounts_parentalconsent WHERE minor_id=$1),(SELECT count(*) FROM accounts_guardianrelationship WHERE guardian_id=$1 OR ward_id=$1),(SELECT count(*) FROM safety_auditlog WHERE event='accounts.administrator_bootstrapped' AND target_ref='accounts.user:'||$1::text)`, actor.ID).Scan(&assurances, &consents, &relationships, &audits); err != nil {
		t.Fatal(err)
	}
	if assurances != 0 || consents != 0 || relationships != 0 || audits != 1 {
		t.Fatal("bootstrap identity/audit scope invalid")
	}
	if _, err = s.BootstrapAdministrator(ctx, "generated_bootstrap_admin", "Different-synthetic-bootstrap-password"); !errors.Is(err, authcore.ErrConflict) {
		t.Fatal("existing administrator overwritten")
	}
	ordinary := accountUser(t, s, "generated_existing_member", "unknown", "unassigned")
	if _, err = s.BootstrapAdministrator(ctx, "GENERATED_EXISTING_MEMBER", password); !errors.Is(err, authcore.ErrConflict) {
		t.Fatal("existing account shadowed by casing")
	}
	existing, err := s.actor(ctx, s.DB, ordinary.ID)
	if err != nil || existing.IsStaff || existing.IsSuperuser || existing.Role != "user" || existing.IdentityVerified || existing.Cohort != "unassigned" {
		t.Fatal("existing account promoted or assured")
	}
	var sameHash string
	if err = s.DB.QueryRow(ctx, `SELECT password FROM accounts_user WHERE id=$1`, actor.ID).Scan(&sameHash); err != nil || sameHash != hash {
		t.Fatal("existing password overwritten")
	}
	var secretLeaks int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_auditlog WHERE data::text LIKE '%'||$1||'%' OR data::text LIKE '%'||$2||'%'`, password, hash).Scan(&secretLeaks); err != nil || secretLeaks != 0 {
		t.Fatal("bootstrap secret entered audit payload")
	}
}
func TestNativeAdministratorBootstrapAuditFailureRollsBackAccount(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `ALTER TABLE safety_auditlog ADD CONSTRAINT bootstrap_fixture_refuse_audit CHECK(event<>'accounts.administrator_bootstrapped')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BootstrapAdministrator(ctx, "generated_rollback_admin", "Synthetic-bootstrap-password"); err == nil {
		t.Fatal("unaudited administrator created")
	}
	var count int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_user WHERE username='generated_rollback_admin'`).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed bootstrap left account behind")
	}
}
