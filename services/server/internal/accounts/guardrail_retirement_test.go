package accounts

import (
	"context"
	"errors"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRetirementGuardianGuardrailAuthorityValidationAndCanonicalStorage(t *testing.T) {
	s := accountFixture(t)
	ctx := context.Background()
	guardian := accountUser(t, s, "retirement-guardrail-guardian", "adult", "adult")
	ward := accountUser(t, s, "retirement-guardrail-child", "under_16", "child")
	teen := accountUser(t, s, "retirement-guardrail-teen", "16_17", "teen")
	if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, GuardrailInput{SupervisedOnly: true}); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("unlinked guardian changed child guardrails", err)
	}
	for _, id := range []int64{ward.ID, teen.ID} {
		if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,relationship,status,created_at,updated_at) VALUES($1,$2,'parent','active',now(),now())`, guardian.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetGuardianGuardrail(ctx, guardian, teen.ID, GuardrailInput{SupervisedOnly: true}); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("teen ward received child-only guardrail", err)
	}
	for _, hour := range []int{-1, 24} {
		for _, field := range []string{"earliest", "latest"} {
			in := GuardrailInput{}
			if field == "earliest" {
				in.Earliest = &hour
			} else {
				in.Latest = &hour
			}
			if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, in); !errors.Is(err, platform.ErrInvalid) {
				t.Fatal("invalid hour boundary accepted", err)
			}
		}
	}
	for _, cap := range []int{0, 51} {
		if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, GuardrailInput{MaxOpen: &cap}); !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("invalid open-join cap accepted", err)
		}
	}
	for _, weekdays := range [][]string{{"8"}, {"0"}, {"x"}, {"1x"}, {"90"}, {"1", "9"}} {
		if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, GuardrailInput{Weekdays: weekdays}); !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("malformed weekday allowlist accepted", err)
		}
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO taxonomy_activitycategory(name,slug,description,parent_id,created_at,updated_at) VALUES('Alpha','retirement-alpha','',NULL,now(),now()),('Beta','retirement-beta','',NULL,now(),now())`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, GuardrailInput{Categories: []string{"unknown-category"}}); !errors.Is(err, platform.ErrInvalid) {
		t.Fatal("unknown category created enforceable guardrail", err)
	}
	zero, latest, cap := 0, 23, 50
	in := GuardrailInput{Earliest: &zero, Latest: &latest, MaxOpen: &cap, Weekdays: []string{"3", "1", "3", "5"}, Categories: []string{"retirement-beta", "retirement-alpha", "retirement-beta"}}
	if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, in); err != nil {
		t.Fatal(err)
	}
	var earliest, storedLatest, storedCap int
	var weekdays string
	var categories []string
	if err := s.DB.QueryRow(ctx, `SELECT g.earliest_start_hour,g.latest_start_hour,g.max_open_joins,g.allowed_weekdays,g.allowed_categories FROM accounts_guardianguardrail g JOIN accounts_guardianrelationship r ON r.id=g.relationship_id WHERE r.guardian_id=$1 AND r.ward_id=$2`, guardian.ID, ward.ID).Scan(&earliest, &storedLatest, &storedCap, &weekdays, &categories); err != nil || earliest != 0 || storedLatest != 23 || storedCap != 50 || weekdays != "135" || len(categories) != 2 || categories[0] != "retirement-alpha" || categories[1] != "retirement-beta" {
		t.Fatal("boundary/null or canonical sorted-unique storage drifted")
	}
	if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, GuardrailInput{}); err != nil {
		t.Fatal(err)
	}
	var rows int
	var unrestricted bool
	if err := s.DB.QueryRow(ctx, `SELECT count(*),bool_and(g.earliest_start_hour IS NULL AND g.latest_start_hour IS NULL AND g.max_open_joins IS NULL AND g.allowed_weekdays='' AND cardinality(g.allowed_categories)=0) FROM accounts_guardianguardrail g JOIN accounts_guardianrelationship r ON r.id=g.relationship_id WHERE r.guardian_id=$1 AND r.ward_id=$2`, guardian.ID, ward.ID).Scan(&rows, &unrestricted); err != nil || rows != 1 || !unrestricted {
		t.Fatal("guardrail replacement duplicated row or empty limits became block-all")
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_guardianrelationship SET status='revoked' WHERE guardian_id=$1 AND ward_id=$2`, guardian.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetGuardianGuardrail(ctx, guardian, ward.ID, in); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("revoked relationship retained guardrail-write authority", err)
	}
}
