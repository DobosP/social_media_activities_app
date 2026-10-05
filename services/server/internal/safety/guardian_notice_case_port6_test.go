package safety

import (
	"context"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/messaging"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"strings"
	"testing"
)

func privacy6NoticeLink(t *testing.T, s *Service, g, ward platform.Actor) {
	t.Helper()
	if _, err := s.DB.Exec(context.Background(), `INSERT INTO accounts_guardianrelationship(guardian_id,ward_id,status,relationship,consent_id,created_at,updated_at) VALUES($1,$2,'active','parent',NULL,now(),now())`, g.ID, ward.ID); err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyCasePort6GuardianModerationUnionExactPointersAndCurrentExclusions(t *testing.T) {
	for _, scenario := range []string{"offender_active_guardian", "pure_pointer", "content_owner", "child_reporter", "dismissed_child_reporter", "same_guardian_union", "two_guardians", "teen_offender", "blocked_guardian", "revoked_guardian", "all_adult"} {
		t.Run(scenario, func(t *testing.T) {
			s, soc := privacy5Fixture(t)
			ctx := context.Background()
			mod := user(t, s, "privacy6-secret-moderator", true)
			cohort := "child"
			if scenario == "teen_offender" {
				cohort = "teen"
			}
			if scenario == "child_reporter" || scenario == "dismissed_child_reporter" || scenario == "all_adult" {
				cohort = "adult"
			}
			offender := testdb.Actor(t, s.DB, "privacy6-private-offender", cohort)
			reporterCohort := "adult"
			if scenario == "child_reporter" || scenario == "dismissed_child_reporter" || scenario == "same_guardian_union" {
				reporterCohort = "child"
			}
			reporter := testdb.Actor(t, s.DB, "privacy6-private-reporter", reporterCohort)
			g := testdb.Actor(t, s.DB, "privacy6-private-guardian", "adult")
			ward := offender
			if scenario == "child_reporter" || scenario == "dismissed_child_reporter" {
				ward = reporter
			}
			privacy6NoticeLink(t, s, g, ward)
			if scenario == "same_guardian_union" {
				privacy6NoticeLink(t, s, g, reporter)
			}
			guardians := []platform.Actor{g}
			if scenario == "two_guardians" {
				second := testdb.Actor(t, s.DB, "privacy6-second-private-guardian", "adult")
				privacy6NoticeLink(t, s, second, offender)
				guardians = append(guardians, second)
			}
			if scenario == "blocked_guardian" {
				out := request(s, offender, "POST", "/api/safety/blocks/", fmt.Sprintf(`{"user_id":%d}`, g.ID))
				if out.Code != 204 {
					t.Fatal("actual blocker service", out.Code)
				}
			}
			if scenario == "revoked_guardian" {
				if err := s.Config.Accounts.RevokeGuardian(ctx, g, ward.ID, messaging.New(s.DB, platform.CursorCodec{})); err != nil {
					t.Fatal("actual guardian revoke", err)
				}
			}
			model, id := "user", offender.ID
			reason := "grooming"
			decision := "warn"
			if scenario == "pure_pointer" {
				decision = "suspend"
			}
			if scenario == "content_owner" {
				model = "activity"
				id = privacy5Activity(t, s, soc, offender)
				decision = "remove"
				reason = "off_platform"
			}
			report := int64(0)
			if scenario == "child_reporter" || scenario == "dismissed_child_reporter" || scenario == "same_guardian_union" || scenario == "all_adult" {
				var err error
				report, err = s.FileReport(ctx, reporter, privacy5Target(t, s, model, id), "harassment", "private reporter allegation")
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "dismissed_child_reporter" {
				out := request(s, mod, "POST", fmt.Sprintf("/api/safety/moderation/reports/%d/resolve/", report), `{"decision":"dismiss","notes":"private moderator outcome"}`)
				if out.Code != 200 {
					t.Fatal("actual dismissal", out.Code)
				}
			} else {
				if _, err := s.TakeAction(ctx, mod, privacy5Target(t, s, model, id), ActionInput{Decision: decision, Reason: reason, Notes: "private moderation outcome note"}, report); err != nil {
					t.Fatal(err)
				}
			}
			want := 1
			if scenario == "teen_offender" || scenario == "blocked_guardian" || scenario == "revoked_guardian" || scenario == "all_adult" {
				want = 0
			}
			const title = "A moderation decision concerning a child you look after"
			for _, guardian := range guardians {
				var count int
				var kind, body, url string
				if err := s.DB.QueryRow(ctx, `SELECT count(*),COALESCE(max(kind),''),COALESCE(max(body),''),COALESCE(max(url),'') FROM notifications_notification WHERE recipient_id=$1 AND title=$2`, guardian.ID, title).Scan(&count, &kind, &body, &url); err != nil || count != want {
					t.Fatal("guardian union/current exclusion count", count, want, err)
				}
				if want == 1 {
					if kind != "system" || url != "/wards/" {
						t.Fatal("guardian outcome is not exact non-mutable SYSTEM pointer")
					}
					for _, private := range []string{"grooming", "suspend", "off_platform", mod.Username, offender.Username, reporter.Username, "private reporter allegation", "private moderation outcome"} {
						if strings.Contains(strings.ToLower(body), private) {
							t.Fatal("guardian pointer leaked reason/identity/outcome detail")
						}
					}
				}
			}
			if scenario == "all_adult" {
				for _, subject := range []platform.Actor{offender, reporter} {
					var count int
					if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM notifications_notification WHERE recipient_id=$1 AND title=$2`, subject.ID, title).Scan(&count); err != nil || count != 0 {
						t.Fatal("adult outcome manufactured child pointer", err)
					}
				}
			}
		})
	}
}
