package safety

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"strings"
	"testing"
)

func TestPrivacyCasePort5ReportAcknowledgementActionAndDismissOutcome(t *testing.T) {
	for _, decision := range []string{"ban", "dismiss"} {
		t.Run(decision, func(t *testing.T) {
			s, _ := privacy5Fixture(t)
			ctx := context.Background()
			mod := user(t, s, "privacy5-report-mod", true)
			reporter := user(t, s, "privacy5-report-reporter", false)
			subject := user(t, s, "privacy5-report-subject", false)
			id, err := s.FileReport(ctx, reporter, privacy5Target(t, s, "user", subject.ID), "harassment", "Synthetic incident detail")
			if err != nil {
				t.Fatal(err)
			}
			var count int
			var title string
			if err := s.DB.QueryRow(ctx, `SELECT count(*),COALESCE(max(title),'') FROM notifications_notification WHERE recipient_id=$1 AND kind='system'`, reporter.ID).Scan(&count, &title); err != nil || count != 1 || !strings.Contains(strings.ToLower(title), "report") {
				t.Fatal("report acknowledgement missing", err)
			}
			var persistedReporter int64
			if err := s.DB.QueryRow(ctx, `SELECT reporter_id FROM safety_report WHERE id=$1`, id).Scan(&persistedReporter); err != nil || persistedReporter != reporter.ID {
				t.Fatal("authenticated report lost exact reporter FK", err)
			}
			if _, err := s.DB.Exec(ctx, `DELETE FROM notifications_notification`); err != nil {
				t.Fatal(err)
			}
			out := request(s, mod, "POST", fmt.Sprintf("/api/safety/moderation/reports/%d/resolve/", id), fmt.Sprintf(`{"decision":%q,"reason":"harassment","notes":"private decision note"}`, decision))
			if out.Code != 200 {
				t.Fatal("moderator resolution", out.Code)
			}
			if err := s.DB.QueryRow(ctx, `SELECT count(*),COALESCE(max(title),'') FROM notifications_notification WHERE recipient_id=$1`, reporter.ID).Scan(&count, &title); err != nil || count != 1 || !strings.Contains(strings.ToLower(title), "reviewed") {
				t.Fatal("one report review outcome missing", err)
			}
			var status string
			var handled int64
			var audited int
			event := "moderation.action"
			want := "actioned"
			if decision == "dismiss" {
				event = "report.dismissed"
				want = "dismissed"
			}
			if err := s.DB.QueryRow(ctx, `SELECT status,handled_by_id,(SELECT count(*) FROM safety_auditlog WHERE event=$2) FROM safety_report WHERE id=$1`, id, event).Scan(&status, &handled, &audited); err != nil || status != want || handled != mod.ID || audited != 1 {
				t.Fatal("resolution status/handler/audit failed", err)
			}
			if decision == "ban" {
				var active bool
				if err := s.DB.QueryRow(ctx, `SELECT is_active FROM accounts_user WHERE id=$1`, subject.ID).Scan(&active); err != nil || active {
					t.Fatal("role-only moderator did not ban", err)
				}
			}
		})
	}
}

func TestPrivacyCasePort5AnonymousReportUsesNullableReporterAndNoNotice(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	subject := user(t, s, "privacy5-anonymous-target", false)
	id, err := s.FileReport(ctx, platform.Actor{}, privacy5Target(t, s, "user", subject.ID), "spam", "")
	if err != nil {
		t.Fatal("anonymous report violated nullable reporter FK", err)
	}
	var anonymous bool
	var notices int
	if err := s.DB.QueryRow(ctx, `SELECT reporter_id IS NULL,(SELECT count(*) FROM notifications_notification) FROM safety_report WHERE id=$1`, id).Scan(&anonymous, &notices); err != nil || !anonymous || notices != 0 {
		t.Fatal("anonymous report manufactured identity/notice", err)
	}
}

func TestPrivacyCasePort5ReportAPIVisibilityIndistinguishableAndVisiblePositive(t *testing.T) {
	for _, tc := range []struct {
		name, model, cohort string
		known               bool
		status              int
	}{
		{"user_allowed", "user", "adult", true, 201}, {"unknown_activity", "activity", "adult", false, 404}, {"invisible_activity", "activity", "teen", true, 404}, {"visible_activity", "activity", "adult", true, 201}, {"cross_cohort_post", "post", "teen", true, 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, soc := privacy5Fixture(t)
			ctx := context.Background()
			reporter := user(t, s, "privacy5-visibility-reporter", false)
			owner := testdb.Actor(t, s.DB, "privacy5-visibility-owner", tc.cohort)
			id := owner.ID
			if tc.model != "user" {
				id = privacy5Activity(t, s, soc, owner)
				if tc.model == "post" {
					id = privacy5Post(t, soc, owner, id, "synthetic private post")
				}
			}
			if !tc.known {
				id = 9_000_000_000
			}
			out := request(s, reporter, "POST", "/api/safety/reports/", fmt.Sprintf(`{"target_type":%q,"target_id":%d,"reason":"spam"}`, tc.model, id))
			if out.Code != tc.status {
				t.Fatal("report target status", out.Code, tc.status)
			}
			var reports int
			if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report`).Scan(&reports); err != nil {
				t.Fatal(err)
			}
			want := 0
			if tc.status == 201 {
				want = 1
			}
			if reports != want {
				t.Fatal("rejected invisible target manufactured report", reports)
			}
			if tc.status == 404 {
				var data map[string]any
				if err := json.Unmarshal(out.Body.Bytes(), &data); err != nil {
					t.Fatal(err)
				}
				if len(data) != 1 || data["detail"] != "Not found." {
					t.Fatal("invisible target response leaked existence", data)
				}
			}
		})
	}
}

func TestPrivacyCasePort5ModeratorRoleQueueWallsAndExact100Cap(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-role-moderator", true)
	plain := user(t, s, "privacy5-role-plain", false)
	if mod.IsStaff || mod.IsSuperuser {
		t.Fatal("fixture accidentally grants staff")
	}
	var ct int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_report(reporter_id,target_type_id,target_id,reason,detail,status,resolution,created_at) SELECT $1,$2,$1,'spam','','open','',now() FROM generate_series(1,105)`, plain.ID, ct); err != nil {
		t.Fatal(err)
	}
	for _, a := range []platform.Actor{{}, plain} {
		out := request(s, a, "GET", "/api/safety/moderation/reports/", "")
		want := 403
		if a.ID == 0 {
			want = 401
		}
		if out.Code != want || strings.Contains(out.Body.String(), "triage") {
			t.Fatal("private moderation queue accessible", out.Code)
		}
	}
	out := request(s, mod, "GET", "/api/safety/moderation/reports/", "")
	var rows []map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &rows); err != nil || out.Code != 200 || len(rows) != 100 {
		t.Fatal("role-only moderator queue cap", out.Code, len(rows), err)
	}
}

func TestPrivacyCasePort5TriagePrivacyDerivedOrderingDuplicatesAndAudit(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-triage-moderator", true)
	reporter := user(t, s, "privacy5-triage-reporter", false)
	adult := user(t, s, "privacy5-triage-adult", false)
	child := testdb.Actor(t, s.DB, "privacy5-triage-child", "child")
	file := func(model string, id int64, reason string) int64 {
		id, err := s.FileReport(ctx, reporter, privacy5Target(t, s, model, id), reason, "private incident detail")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	spam := file("user", adult.ID, "spam")
	grooming := file("user", child.ID, "grooming")
	harassment := file("user", adult.ID, "harassment")
	activity := privacy5Activity(t, s, soc, adult)
	post := privacy5Post(t, soc, adult, activity, "let's move this to whatsapp, my number 0712345678")
	contact := file("post", post, "grooming")
	var before int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_report`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	out := request(s, mod, "GET", "/api/safety/moderation/reports/?status=open", "")
	var rows []map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &rows); err != nil || out.Code != 200 || len(rows) != 4 {
		t.Fatal("triage queue", out.Code, err)
	}
	indexed := map[int64]map[string]any{}
	positions := map[int64]int{}
	for i, row := range rows {
		id := int64(row["id"].(float64))
		indexed[id] = row
		positions[id] = i
		triage := row["triage"].(map[string]any)
		if len(triage) != 5 {
			t.Fatal("triage privacy allowlist widened")
		}
		raw, _ := json.Marshal(triage)
		if strings.Contains(string(raw), "under_16") || strings.Contains(string(raw), "birth") {
			t.Fatal("triage leaked child age")
		}
	}
	childSignals := indexed[grooming]["triage"].(map[string]any)
	adultSignals := indexed[spam]["triage"].(map[string]any)
	postSignals := indexed[contact]["triage"].(map[string]any)
	if childSignals["involves_child"] != true || adultSignals["involves_child"] != false || childSignals["severity"].(float64) <= adultSignals["severity"].(float64) || positions[grooming] >= positions[harassment] || positions[harassment] >= positions[spam] {
		t.Fatal("triage severity/child ordering")
	}
	if adultSignals["open_duplicates"] != float64(2) || postSignals["contact_hint"] != true || adultSignals["contact_hint"] != false {
		t.Fatal("incident-local duplicate/contact signal")
	}
	terms := postSignals["contact_terms"].([]any)
	found := false
	for _, term := range terms {
		if term == "whatsapp" {
			found = true
		}
	}
	if !found {
		t.Fatal("contact terms lost WhatsApp signal")
	}
	var after int
	var actor int64
	var data []byte
	if err := s.DB.QueryRow(ctx, `SELECT (SELECT count(*) FROM safety_report),actor_ref,data FROM safety_auditlog WHERE event='moderation.queue_viewed' ORDER BY id DESC LIMIT 1`).Scan(&after, &actor, &data); err != nil || after != before || actor != mod.ID {
		t.Fatal("derived triage persisted reports or lacked audit", err)
	}
	if strings.Contains(string(data), "detail") || strings.Contains(string(data), "body") || strings.Contains(string(data), "private incident") {
		t.Fatal("queue audit retained incident content")
	}
	// Once both target reports are dismissed, a non-open report has zero open duplicates.
	for _, id := range []int64{spam, harassment} {
		if out := request(s, mod, "POST", fmt.Sprintf("/api/safety/moderation/reports/%d/resolve/", id), `{"decision":"dismiss","notes":"no violation"}`); out.Code != 200 {
			t.Fatal("dismiss", out.Code)
		}
	}
	queue, err := s.ReportQueue(ctx, mod, "dismissed")
	if err != nil || len(queue) != 2 {
		t.Fatal(err)
	}
	for _, row := range queue {
		if row["triage"].(map[string]any)["open_duplicates"] != 0 {
			t.Fatal("dismissed report fallback duplicate fabricated")
		}
	}
}

func TestPrivacyCasePort5Triage24MixedTargetsWithinExactQueryCeiling(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	s.Config.Accounts.DB = db
	mod := user(t, s, "privacy5-triage-query-mod", true)
	reporter := user(t, s, "privacy5-triage-query-reporter", false)
	for i := 0; i < 12; i++ {
		for _, cohort := range []string{"child", "adult"} {
			target := testdb.Actor(t, db, fmt.Sprintf("privacy5-triage-query-%s-%d", cohort, i), cohort)
			reason := "spam"
			if cohort == "child" {
				reason = "grooming"
			}
			if _, err := s.FileReport(ctx, reporter, privacy5Target(t, s, "user", target.ID), reason, ""); err != nil {
				t.Fatal(err)
			}
		}
	}
	trace.Reset()
	out := request(s, mod, "GET", "/api/safety/moderation/reports/?status=open", "")
	queries := trace.Count()
	var rows []map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &rows); err != nil || out.Code != 200 || len(rows) != 24 || queries < 1 || queries > 20 {
		t.Fatalf("24-target queue exact bound: status=%d rows=%d queries=%d err=%v", out.Code, len(rows), queries, err)
	}
	t.Logf("24 mixed target queue queries=%d ceiling=20", queries)
}
