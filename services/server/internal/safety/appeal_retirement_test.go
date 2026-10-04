package safety

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
)

func TestRetirementAppealInputOwnershipAndDecidedReplay(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "retirement-appeal-mod", true)
	subject := user(t, s, "retirement-appeal-subject", false)
	other := user(t, s, "retirement-appeal-outsider", false)
	target, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	action, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "warn", Reason: "spam"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{"", "   ", strings.Repeat("x", 2001)} {
		if _, err := s.FileAppeal(ctx, subject, action, statement); !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("invalid appeal statement accepted", err)
		}
	}
	if _, err := s.FileAppeal(ctx, other, action, "Other account contest"); !errors.Is(err, platform.ErrNotFound) {
		t.Fatal("proxy contest disclosed or accepted another account's action", err)
	}
	appeal, err := s.FileAppeal(ctx, subject, action, "  Please reconsider  ")
	if err != nil {
		t.Fatal(err)
	}
	var trimmed bool
	if err := s.DB.QueryRow(ctx, `SELECT statement='Please reconsider' AND status='pending' FROM safety_moderationappeal WHERE id=$1`, appeal).Scan(&trimmed); err != nil || !trimmed {
		t.Fatal("appeal did not trim and pend reviewed statement")
	}
	if _, err := s.FileAppeal(ctx, subject, action, "Duplicate contest"); !errors.Is(err, ErrConflict) {
		t.Fatal("duplicate action received a second appeal", err)
	}
	if _, err := s.ResolveAppeal(ctx, other, appeal, true, ""); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("ordinary account decided an appeal", err)
	}
	if _, err := s.ResolveAppeal(ctx, mod, appeal, false, "Private decision notes"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveAppeal(ctx, mod, appeal, true, "Replay decision"); !errors.Is(err, ErrConflict) {
		t.Fatal("decided appeal replay changed its outcome", err)
	}
	var rows, audits, notices int
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM safety_moderationappeal WHERE action_id=$1 AND status='upheld'),(SELECT count(*) FROM safety_auditlog WHERE event='moderation.appeal_resolved'),(SELECT count(*) FROM notifications_notification WHERE recipient_id=$2 AND title='Your appeal was reviewed')`, action, subject.ID).Scan(&rows, &audits, &notices); err != nil || rows != 1 || audits != 1 || notices != 1 {
		t.Fatal("appeal replay manufactured state, audit or notice")
	}
}

func TestRetirementSanctionCommitsWhenSubjectNotificationFails(t *testing.T) {
	s := safetyFixture(t)
	ctx := context.Background()
	mod := user(t, s, "retirement-notice-mod", true)
	subject := user(t, s, "retirement-notice-subject", false)
	if _, err := s.DB.Exec(ctx, `ALTER TABLE notifications_notification ADD CONSTRAINT retirement_refuse_notice CHECK(kind<>'moderation')`); err != nil {
		t.Fatal(err)
	}
	target, err := s.ResolveTarget(ctx, s.DB, "accounts", "user", subject.ID)
	if err != nil {
		t.Fatal(err)
	}
	action, err := s.TakeAction(ctx, mod, target, ActionInput{Decision: "suspend", Reason: "harassment", SuspendDays: 1}, 0)
	if err != nil || action < 1 {
		t.Fatal("subject notice failure rolled back governed sanction", err)
	}
	var active bool
	var audits, notices int
	if err := s.DB.QueryRow(ctx, `SELECT is_active,(SELECT count(*) FROM safety_auditlog WHERE event='moderation.action'),(SELECT count(*) FROM notifications_notification WHERE recipient_id=$1) FROM accounts_user WHERE id=$1`, subject.ID).Scan(&active, &audits, &notices); err != nil || active || audits != 1 || notices != 0 {
		t.Fatal("notice failure left partial sanction/audit state")
	}
}
