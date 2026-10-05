package safety

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRestrictedProofSurvivesFullFailureTableAndThrottlesPeer(t *testing.T) {
	s, subject, _, _ := restrictedCaseFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_go_login_failure(key_hash,epoch,failures,failure_until,touched_at)
 SELECT encode(sha256(i::text::bytea),'hex'),encode(sha256(i::text::bytea),'hex'),1,now()+interval '15 minutes',now() FROM generate_series(1,10000) i`); err != nil {
		t.Fatal(err)
	}
	statement, _, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.40:1")
	if err != nil || statement == nil {
		t.Fatal("full failure table blocked the DSA statement of reasons", err)
	}
	s.Config.Accounts.RatePolicies = map[string]budgets.Policy{"auth.restricted": {Limit: 3, Window: 15 * time.Minute}}
	for i := 0; i < 3; i++ {
		if _, _, err := s.RestrictionAccess(ctx, subject.Username, "incorrect", fmt.Sprintf("198.51.100.41:%d", 1000+i)); !errors.Is(err, platform.ErrForbidden) {
			t.Fatal("restricted proof inside its peer cap", i, err)
		}
	}
	if _, _, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.41:2000"); !errors.Is(err, ErrRate) {
		t.Fatal("restricted proof lacks a per-prefix total-attempt cap", err)
	}
	if statement, _, err := s.RestrictionAccess(ctx, subject.Username, restrictedCasePassword, "198.51.100.42:1"); err != nil || statement == nil {
		t.Fatal("one peer's proofs throttled another peer", err)
	}
}
