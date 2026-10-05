package safety

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

// Reporting is the sole DSA Art-16 channel, so its eligibility never borrows a
// block-aware read gate. Cohort walls and a thread-seat anchor still hold.

func eligibilitySeat(t *testing.T, s *Service, activity, user int64, role, state string) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO social_membership(activity_id,user_id,role,state,attendance_intent,transit_status,brings_support_person,created_at,updated_at,decided_at) VALUES($1,$2,$3,$4,'unknown','none',false,now(),now(),now())`, activity, user, role, state); err != nil {
		t.Fatal(err)
	}
}

func eligibilityBlock(t *testing.T, s *Service, blocker, blocked int64) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO safety_block(blocker_id,blocked_id,created_at) VALUES($1,$2,now())`, blocker, blocked); err != nil {
		t.Fatal(err)
	}
}

func eligibilityUnblock(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `DELETE FROM safety_block`); err != nil {
		t.Fatal(err)
	}
}

func eligibilityReport(s *Service, a platform.Actor, model string, id int64) int {
	return request(s, a, "POST", "/api/safety/reports/", fmt.Sprintf(`{"target_type":%q,"target_id":%d,"reason":"harassment"}`, model, id)).Code
}

func TestReportEligibilityActivityIgnoresOwnerBlocksAndHiddenButKeepsCohort(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	owner := testdb.Actor(t, s.DB, "eligibility-activity-owner", "adult")
	reporter := testdb.Actor(t, s.DB, "eligibility-activity-reporter", "adult")
	teen := testdb.Actor(t, s.DB, "eligibility-activity-teen", "teen")
	activity := privacy5Activity(t, s, soc, owner)
	for _, pair := range [][2]int64{{reporter.ID, owner.ID}, {owner.ID, reporter.ID}} {
		eligibilityBlock(t, s, pair[0], pair[1])
		if code := eligibilityReport(s, reporter, "activity", activity); code != 201 {
			t.Fatal("a block with the owner refused the activity report", pair, code)
		}
		eligibilityUnblock(t, s)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE social_activity SET is_hidden=true WHERE id=$1`, activity); err != nil {
		t.Fatal(err)
	}
	if code := eligibilityReport(s, reporter, "activity", activity); code != 201 {
		t.Fatal("a hidden activity report was refused", code)
	}
	if code := eligibilityReport(s, teen, "activity", activity); code != 404 {
		t.Fatal("cross-cohort activity report admitted", code)
	}
	var reports int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report WHERE reporter_id=$1`, reporter.ID).Scan(&reports); err != nil || reports != 3 {
		t.Fatal("activity report rows", reports, err)
	}
}

func TestReportEligibilityPostNeedsAThreadSeatNotReadAccess(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	owner := testdb.Actor(t, s.DB, "eligibility-post-owner", "adult")
	member := testdb.Actor(t, s.DB, "eligibility-post-member", "adult")
	stranger := testdb.Actor(t, s.DB, "eligibility-post-stranger", "adult")
	pending := testdb.Actor(t, s.DB, "eligibility-post-pending", "adult")
	teen := testdb.Actor(t, s.DB, "eligibility-post-teen", "teen")
	activity := privacy5Activity(t, s, soc, owner)
	eligibilitySeat(t, s, activity, member.ID, "member", "member")
	eligibilitySeat(t, s, activity, pending.ID, "member", "requested")
	post := privacy5Post(t, soc, owner, activity, "synthetic owner thread post")
	eligibilityBlock(t, s, member.ID, owner.ID)
	if code := eligibilityReport(s, member, "post", post); code != 201 {
		t.Fatal("the member's own block refused the post report", code)
	}
	eligibilityUnblock(t, s)
	eligibilityBlock(t, s, owner.ID, member.ID)
	if code := eligibilityReport(s, member, "post", post); code != 201 {
		t.Fatal("the owner's block refused the post report", code)
	}
	// Leaving is a safe exit too: it must not depend on the block-aware read gate.
	if _, err := soc.Leave(ctx, member, activity); err != nil {
		t.Fatal("leave under an owner block", err)
	}
	if code := eligibilityReport(s, member, "post", post); code != 201 {
		t.Fatal("a member who left under a block lost the post report", code)
	}
	eligibilityUnblock(t, s)
	if code := eligibilityReport(s, member, "post", post); code != 201 {
		t.Fatal("a member who left lost the post report", code)
	}
	for _, refused := range []platform.Actor{stranger, pending, teen} {
		if code := eligibilityReport(s, refused, "post", post); code != 404 {
			t.Fatal("seatless or cross-cohort post report admitted", refused.Username, code)
		}
	}
	soc.AllowUserGroups = true
	var typ int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	group, err := soc.CreateGroup(ctx, owner, social.GroupInput{City: "Cluj-Napoca", ActivityType: &typ, Title: "Eligibility standing group"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec(ctx, `INSERT INTO social_groupmembership(group_id,user_id,role,state,joined_at) VALUES($1,$2,'member','member',now())`, group, member.ID); err != nil {
		t.Fatal(err)
	}
	groupPost, err := soc.WritePost(ctx, owner, "group", group, social.PostInput{Body: "synthetic group thread post"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if code := eligibilityReport(s, member, "post", groupPost); code != 201 {
		t.Fatal("a current group member's post report was refused", code)
	}
	if _, err = s.DB.Exec(ctx, `UPDATE social_groupmembership SET state='left' WHERE group_id=$1 AND user_id=$2`, group, member.ID); err != nil {
		t.Fatal(err)
	}
	if code := eligibilityReport(s, member, "post", groupPost); code != 201 {
		t.Fatal("a member who left the group lost the post report", code)
	}
	if code := eligibilityReport(s, stranger, "post", groupPost); code != 404 {
		t.Fatal("a never-member group post report was admitted", code)
	}
	// Labels name the author by display name only, never a username.
	target, err := s.ReportTarget(ctx, member, "post", post)
	if err != nil || target.Label != owner.DisplayName {
		t.Fatal("post report label", target.Label, err)
	}
	// Eligibility is wider than read access; labels are not.
	eligibilityBlock(t, s, owner.ID, member.ID)
	if target, err = s.ReportTarget(ctx, member, "post", post); err != nil || target.Label != "A member" {
		t.Fatal("post report label named the author across a block", target.Label, err)
	}
	if target, err = s.ReportTarget(ctx, member, "activity", activity); err != nil || target.Label != "this activity" {
		t.Fatal("activity report label showed a title the read gate hides", target.Label, err)
	}
	eligibilityUnblock(t, s)
	if _, err = s.DB.Exec(ctx, `UPDATE accounts_user SET display_name='' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if target, err = s.ReportTarget(ctx, member, "post", post); err != nil || target.Label != "A member" {
		t.Fatal("post report label exposed a username", target.Label, err)
	}
}

func TestReportEligibilityUserTargetsKeepProfileVetoes(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	reporter := testdb.Actor(t, s.DB, "eligibility-user-reporter", "adult")
	teen := testdb.Actor(t, s.DB, "eligibility-user-teen", "teen")
	blocker := testdb.Actor(t, s.DB, "eligibility-user-blocker", "adult")
	inactive := testdb.Actor(t, s.DB, "eligibility-user-inactive", "adult")
	stranger := testdb.Actor(t, s.DB, "eligibility-user-stranger", "adult")
	eligibilityBlock(t, s, blocker.ID, reporter.ID)
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET is_active=false WHERE id=$1`, inactive.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET display_name='' WHERE id=$1`, stranger.ID); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []platform.Actor{teen, blocker, inactive} {
		if code := eligibilityReport(s, reporter, "user", hidden.ID); code != 404 {
			t.Fatal("user report crossed a profile veto", hidden.Username, code)
		}
		if _, err := s.ReportTarget(ctx, reporter, "user", hidden.ID); !errors.Is(err, platform.ErrNotFound) {
			t.Fatal("report page resolved a vetoed person", hidden.Username, err)
		}
	}
	if code := eligibilityReport(s, teen, "user", reporter.ID); code != 404 {
		t.Fatal("minor reached an adult user target", code)
	}
	if code := eligibilityReport(s, reporter, "user", stranger.ID); code != 201 {
		t.Fatal("same-cohort stranger report refused", code)
	}
	target, err := s.ReportTarget(ctx, reporter, "user", stranger.ID)
	if err != nil || target.Label != "A member" {
		t.Fatal("stranger report label exposed a username", target.Label, err)
	}
	var reports int
	if err = s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report`).Scan(&reports); err != nil || reports != 1 {
		t.Fatal("vetoed user reports persisted", reports, err)
	}
}

func TestReportEligibilityRefusalSpendsNoReportBudget(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	reporter := testdb.Actor(t, s.DB, "eligibility-budget-reporter", "adult")
	teen := testdb.Actor(t, s.DB, "eligibility-budget-teen", "teen")
	activity := privacy5Activity(t, s, soc, teen)
	for _, id := range []int64{activity, 9_000_000_000} {
		if code := eligibilityReport(s, reporter, "activity", id); code != 404 {
			t.Fatal("refused report probe status", id, code)
		}
	}
	var debits int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_go_actionbudget WHERE user_id=$1 AND action='report'`, reporter.ID).Scan(&debits); err != nil || debits != 0 {
		t.Fatal("refused report probes spent the report budget", debits, err)
	}
}

func TestUnsafeReportServiceOwnsTheSeatGateAndIgnoresOwnerBlocks(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	owner := testdb.Actor(t, s.DB, "eligibility-unsafe-owner", "adult")
	member := testdb.Actor(t, s.DB, "eligibility-unsafe-member", "adult")
	stranger := testdb.Actor(t, s.DB, "eligibility-unsafe-stranger", "adult")
	seat := testdb.Actor(t, s.DB, "eligibility-unsafe-guardian-seat", "adult")
	pending := testdb.Actor(t, s.DB, "eligibility-unsafe-pending", "adult")
	activity := privacy5Activity(t, s, soc, owner)
	eligibilitySeat(t, s, activity, member.ID, "member", "member")
	eligibilitySeat(t, s, activity, seat.ID, "guardian", "member")
	eligibilitySeat(t, s, activity, pending.ID, "member", "requested")
	eligibilityBlock(t, s, member.ID, owner.ID)
	eligibilityBlock(t, s, owner.ID, member.ID)
	first, err := s.UnsafeReport(ctx, member, activity)
	if err != nil || first.Repeat || first.ReportID < 1 {
		t.Fatal("a block with the owner pre-empted the safe exit", first, err)
	}
	for _, refused := range []platform.Actor{owner, stranger, seat, pending} {
		if _, err = s.UnsafeReport(ctx, refused, activity); !errors.Is(err, platform.ErrNotFound) {
			t.Fatal("unsafe report admitted without a member seat", refused.Username, err)
		}
	}
	if _, err = s.DB.Exec(ctx, `UPDATE social_activity SET is_hidden=true WHERE id=$1`, activity); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UnsafeReport(ctx, member, activity); !errors.Is(err, platform.ErrNotFound) {
		t.Fatal("unsafe report admitted on a hidden activity", err)
	}
	var debited, reports int
	if err = s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM safety_go_actionbudget WHERE action='unsafe_report' AND user_id<>$1),(SELECT count(*) FROM safety_report)`, member.ID).Scan(&debited, &reports); err != nil || debited != 0 || reports != 1 {
		t.Fatal("refused unsafe taps debited a budget or filed a report", debited, reports, err)
	}
}
