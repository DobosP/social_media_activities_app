package safety

import (
	"context"
	"strings"
	"testing"
)

func TestRetirementOverlappingLifetimeBansKeepWalletUntilLastOverturn(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "retirement-ban-mod", true)
	subject := user(t, s, "retirement-ban-subject", false)
	holder := strings.Repeat("d", 64)
	if _, err := s.DB.Exec(ctx, `INSERT INTO accounts_identitybinding(holder_hash,user_id,created_at,released_at) VALUES($1,$2,now(),NULL)`, holder, subject.ID); err != nil {
		t.Fatal(err)
	}
	target, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	actions := []int64{}
	for _, reason := range []string{"grooming", "spam"} {
		action, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "ban", Reason: reason}, 0)
		if err != nil {
			t.Fatal(err)
		}
		actions = append(actions, action)
	}
	for i, action := range actions {
		appeal, err := s.FileAppeal(ctx, subject, action, "Synthetic contest of this decision")
		if err != nil {
			t.Fatal(err)
		}
		outcome, err := s.ResolveAppeal(ctx, mod, appeal, true, "Reviewed synthetic evidence")
		if err != nil {
			t.Fatal(err)
		}
		var active, blocked bool
		if err := s.DB.QueryRow(ctx, `SELECT is_active,EXISTS(SELECT 1 FROM accounts_bannedidentity WHERE holder_hash=$2) FROM accounts_user WHERE id=$1`, subject.ID, holder).Scan(&active, &blocked); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if active || !blocked || outcome.Reactivated {
				t.Fatal("first overturn bypassed separate lifetime restriction or released wallet")
			}
		} else if !active || blocked || !outcome.Reactivated {
			t.Fatal("last lifetime overturn retained stale account or wallet restriction")
		}
	}
	bare := user(t, s, "retirement-ban-no-binding", false)
	bareTarget, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", bare.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.TakeAction(ctx, mod, bareTarget, ActionInput{Decision: "ban", Reason: "other"}, 0); err != nil {
		t.Fatal(err)
	}
	var bans int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM accounts_bannedidentity`).Scan(&bans); err != nil || bans != 0 {
		t.Fatal("unbound subject fabricated lifetime identity ledger entry")
	}
}
