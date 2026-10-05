package safety

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

func privacy5Record(t *testing.T, s *Service, subject int64) map[string]any {
	t.Helper()
	record, err := s.Config.Accounts.SafetyRecord(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func privacy5Decisions(t *testing.T, record map[string]any) []map[string]any {
	t.Helper()
	raw, err := json.Marshal(record["decisions"])
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestPrivacyCasePort5SafetyRecordScopeOwnContentReportsAndPrivateProjection(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-record-secret-mod", true)
	subject := user(t, s, "privacy5-record-subject", false)
	other := user(t, s, "privacy5-record-other", false)
	account := privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "warn", Reason: "spam", Notes: "private moderator note never projected"})
	record := privacy5Record(t, s, subject.ID)
	rows := privacy5Decisions(t, record)
	if len(rows) != 1 || rows[0]["action_id"] != float64(account) || rows[0]["scope"] != "your account" {
		t.Fatal("own account record missing")
	}
	for _, row := range rows {
		if len(row) != 10 {
			t.Fatal("decision field allowlist widened", row)
		}
	}
	activity := privacy5Activity(t, s, soc, subject)
	privacy5Action(t, s, mod, "activity", activity, ActionInput{Decision: "remove", Reason: "other"})
	rows = privacy5Decisions(t, privacy5Record(t, s, subject.ID))
	seen := false
	for _, row := range rows {
		if row["scope"] == "one of your activities" {
			seen = true
		}
	}
	if !seen {
		t.Fatal("own activity decision absent")
	}
	if _, err := s.FileReport(ctx, subject, privacy5Target(t, s, "user", other.ID), "harassment", "my own submitted report"); err != nil {
		t.Fatal(err)
	}
	record = privacy5Record(t, s, subject.ID)
	raw, err := json.Marshal(record["reports"])
	if err != nil {
		t.Fatal(err)
	}
	var reports []map[string]any
	if err := json.Unmarshal(raw, &reports); err != nil || len(reports) != 1 || reports[0]["status_label"] != "Open" || reports[0]["detail"] != "my own submitted report" {
		t.Fatal("own reporter record missing", err)
	}
	if len(reports[0]) != 6 {
		t.Fatal("report field allowlist widened")
	}
	privacy5Action(t, s, mod, "user", other.ID, ActionInput{Decision: "warn", Reason: "spam"})
	if len(privacy5Decisions(t, privacy5Record(t, s, subject.ID))) != 2 {
		t.Fatal("other user's decision leaked")
	}
	spectator := user(t, s, "privacy5-record-spectator", false)
	if len(privacy5Decisions(t, privacy5Record(t, s, spectator.ID))) != 0 {
		t.Fatal("spectator got another account decisions")
	}
	privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "suspend", Reason: "harassment", Notes: "private moderator note never projected"})
	record = privacy5Record(t, s, subject.ID)
	rows = privacy5Decisions(t, record)
	if rows[0]["is_sanction"] != true || rows[0]["is_active"] != true {
		t.Fatal("current suspension flag absent")
	}
	raw, err = json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{mod.Username, "private moderator note never projected", other.Username, "moderator_id", "handled_by_id", "target_id"} {
		if strings.Contains(string(raw), hidden) {
			t.Fatal("self record exposed private identity/notes/target")
		}
	}
}

func TestPrivacyCasePort5SafetyRecordNewestPostBeyond1000AndActivityBeyond500(t *testing.T) {
	for _, kind := range []string{"post", "activity"} {
		t.Run(kind, func(t *testing.T) {
			s, soc := privacy5Fixture(t)
			ctx := context.Background()
			mod := user(t, s, "privacy5-volume-mod", true)
			subject := user(t, s, "privacy5-volume-subject", false)
			activity := privacy5Activity(t, s, soc, subject)
			target := activity
			if kind == "post" {
				var thread int64
				if err := s.DB.QueryRow(ctx, `SELECT id FROM social_thread WHERE activity_id=$1`, activity).Scan(&thread); err != nil {
					t.Fatal(err)
				}
				if _, err := s.DB.Exec(ctx, `INSERT INTO social_post(thread_id,author_id,body,is_hidden,is_author_deleted,is_announcement,created_at,updated_at) SELECT $1,$2,'synthetic filler',false,false,false,now()-interval '1 day',now()-interval '1 day' FROM generate_series(1,1000)`, thread, subject.ID); err != nil {
					t.Fatal(err)
				}
				target = privacy5Post(t, soc, subject, activity, "the reported newest post")
			} else {
				if _, err := s.DB.Exec(ctx, `INSERT INTO social_activity(owner_id,place_id,activity_type_id,title,description,starts_at,ends_at,cohort,join_threshold,capacity,min_to_go,guardian_accompanied,supervised,meeting_point,what_to_bring,organizer_note,first_time_note,cost_band,cost_amount,cost_note,difficulty,accessibility_notes,beginners_welcome,status,is_hidden,is_publicly_listed,owner_can_override,go_confirmed_at,series_id,created_at,updated_at) SELECT owner_id,place_id,activity_type_id,'synthetic filler',description,starts_at,ends_at,cohort,join_threshold,capacity,min_to_go,guardian_accompanied,supervised,meeting_point,what_to_bring,organizer_note,first_time_note,cost_band,cost_amount,cost_note,difficulty,accessibility_notes,beginners_welcome,status,is_hidden,is_publicly_listed,owner_can_override,go_confirmed_at,series_id,created_at,updated_at FROM social_activity CROSS JOIN generate_series(1,500) WHERE id=$1`, activity); err != nil {
					t.Fatal(err)
				}
				if err := s.DB.QueryRow(ctx, `SELECT max(id) FROM social_activity WHERE owner_id=$1`, subject.ID).Scan(&target); err != nil {
					t.Fatal(err)
				}
			}
			action := privacy5Action(t, s, mod, kind, target, ActionInput{Decision: "remove", Reason: "other"})
			rows := privacy5Decisions(t, privacy5Record(t, s, subject.ID))
			want := "one of your posts"
			if kind == "activity" {
				want = "one of your activities"
			}
			if len(rows) != 1 || rows[0]["scope"] != want || rows[0]["action_id"] != float64(action) || rows[0]["can_appeal"] != true {
				t.Fatal("large own-content prefilter lost newest decision/contest")
			}
		})
	}
}

func TestPrivacyCasePort5SafetyRecordAuthorDeletionTypedCollisionAndLiveMatrix(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-flag-mod", true)
	subject := user(t, s, "privacy5-flag-subject", false)
	if _, err := s.DB.Exec(ctx, `UPDATE accounts_user SET id=9876543 WHERE id=$1`, subject.ID); err != nil {
		t.Fatal(err)
	}
	subject.ID = 9876543
	activity := privacy5Activity(t, s, soc, subject)
	deleted := privacy5Post(t, soc, subject, activity, "author withdraws")
	if _, err := soc.DeletePost(ctx, subject, deleted); err != nil {
		t.Fatal(err)
	}
	expected := map[int64]bool{}
	expected[privacy5Action(t, s, mod, "post", deleted, ActionInput{Decision: "remove", Reason: "other"})] = true
	kept := privacy5Post(t, soc, subject, activity, "author kept")
	expected[privacy5Action(t, s, mod, "post", kept, ActionInput{Decision: "remove", Reason: "spam"})] = false
	expected[privacy5Action(t, s, mod, "post", deleted, ActionInput{Decision: "warn", Reason: "spam"})] = false
	live := privacy5Post(t, soc, subject, activity, "author-deleted then operator-unhidden")
	if _, err := soc.DeletePost(ctx, subject, live); err != nil {
		t.Fatal(err)
	}
	expected[privacy5Action(t, s, mod, "post", live, ActionInput{Decision: "remove", Reason: "other"})] = false
	if _, err := s.DB.Exec(ctx, `UPDATE social_post SET is_hidden=false WHERE id=$1`, live); err != nil {
		t.Fatal(err)
	}
	// Force a real user/post PK collision far outside either sequence.
	collisionID := int64(9_876_543)
	var thread int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM social_thread WHERE activity_id=$1`, activity).Scan(&thread); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO social_post(id,thread_id,author_id,body,is_hidden,is_author_deleted,is_announcement,created_at,updated_at) VALUES($1,$2,$3,'synthetic typed collision',true,true,false,now(),now())`, collisionID, thread, subject.ID); err != nil {
		t.Fatal(err)
	}
	expected[privacy5Action(t, s, mod, "post", collisionID, ActionInput{Decision: "remove", Reason: "other"})] = true
	expected[privacy5Action(t, s, mod, "activity", activity, ActionInput{Decision: "remove", Reason: "other"})] = false
	expected[privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "warn", Reason: "spam"})] = false
	expected[privacy5Action(t, s, mod, "user", collisionID, ActionInput{Decision: "remove", Reason: "spam"})] = false
	rows := privacy5Decisions(t, privacy5Record(t, s, subject.ID))
	if len(rows) != len(expected) {
		t.Fatal("deletion matrix missing rows", len(rows), len(expected))
	}
	for _, row := range rows {
		id := int64(row["action_id"].(float64))
		want, ok := expected[id]
		if !ok || row["content_author_deleted"] != want {
			t.Fatal("author-deletion flag omitted typed/action/live gate", id)
		}
	}
}

func TestPrivacyCasePort5SafetyRecordQueryCountFlatAuthorDeletedAndWithin13(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	s.Config.Accounts.DB = db
	soc.DB = db
	mod := user(t, s, "privacy5-query-mod", true)
	subject := user(t, s, "privacy5-query-subject", false)
	activity := privacy5Activity(t, s, soc, subject)
	add := func(n int) {
		for i := 0; i < n; i++ {
			post := privacy5Post(t, soc, subject, activity, "synthetic removed post")
			if _, err := soc.DeletePost(ctx, subject, post); err != nil {
				t.Fatal(err)
			}
			privacy5Action(t, s, mod, "post", post, ActionInput{Decision: "remove", Reason: "other"})
		}
	}
	add(2)
	privacy5Record(t, s, subject.ID)
	trace.Reset()
	smallRows := privacy5Decisions(t, privacy5Record(t, s, subject.ID))
	small := trace.Count()
	add(3)
	trace.Reset()
	largeRows := privacy5Decisions(t, privacy5Record(t, s, subject.ID))
	large := trace.Count()
	if len(smallRows) != 2 || len(largeRows) != 5 || small < 1 || large != small {
		t.Fatalf("author-deletion/totals count grows: %d -> %d", small, large)
	}
	for _, row := range largeRows {
		if row["content_author_deleted"] != true {
			t.Fatal("flat query omitted deletion assertions")
		}
	}
	privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "warn", Reason: "spam"})
	privacy5Action(t, s, mod, "activity", activity, ActionInput{Decision: "remove", Reason: "other"})
	if _, err := s.FileReport(ctx, subject, privacy5Target(t, s, "user", mod.ID), "spam", ""); err != nil {
		t.Fatal(err)
	}
	trace.Reset()
	record := privacy5Record(t, s, subject.ID)
	bounded := trace.Count()
	if record["decisions_total"] != 7 || record["reports_total"] != 1 || bounded < 1 || bounded > 13 {
		t.Fatal("mixed-scope self record exceeds source exact query ceiling", bounded)
	}
	t.Logf("author-deleted query count small=%d large=%d mixed=%d ceiling=13", small, large, bounded)
}

func TestPrivacyCasePort5ActivityLogMappedGroupAndReportDedup(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	subject := user(t, s, "privacy5-activity-log-subject", false)
	other := user(t, s, "privacy5-activity-log-other", false)
	if _, err := s.FileReport(ctx, subject, privacy5Target(t, s, "user", other.ID), "harassment", "synthetic private report"); err != nil {
		t.Fatal(err)
	}
	if err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(ctx, tx, subject, "messaging.message_reported", "private.target:17", map[string]string{"private": "must never appear"})
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ActivityLog(ctx, subject.ID)
	if err != nil || len(rows) != 0 {
		t.Fatal("report action duplicated safety record", err)
	}
	if err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(ctx, tx, subject, "group.created", "private.target:18", map[string]string{"private": "must never appear"})
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ActivityLog(ctx, subject.ID)
	if err != nil || len(rows) != 1 || rows[0]["label"] != "You created a group" || len(rows[0]) != 2 || rows[0]["when"] == nil {
		t.Fatal("mapped activity log projection", err)
	}
	raw, _ := json.Marshal(rows)
	for _, hidden := range []string{"group.created", "private.target", other.Username, "must never appear"} {
		if strings.Contains(string(raw), hidden) {
			t.Fatal("activity log leaked private audit contents")
		}
	}
}

func TestPrivacyCasePort5ReaderLimitsExactTotalsTruncationZeroAndBounds(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-limits-mod", true)
	subject := user(t, s, "privacy5-limits-subject", false)
	other := user(t, s, "privacy5-limits-other", false)
	activity := privacy5Activity(t, s, soc, subject)
	first := privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "warn", Reason: "spam"})
	second := privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "suspend", Reason: "spam"})
	third := privacy5Action(t, s, mod, "activity", activity, ActionInput{Decision: "remove", Reason: "other"})
	for i := 0; i < 3; i++ {
		if _, err := s.FileReport(ctx, subject, privacy5Target(t, s, "user", mod.ID), "spam", fmt.Sprintf("synthetic own report %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	rec, err := s.Config.Accounts.SafetyRecord(ctx, subject.ID, 2)
	if err != nil {
		t.Fatal(err)
	}
	rows := privacy5Decisions(t, rec)
	raw, _ := json.Marshal(rec["reports"])
	var reports []map[string]any
	if err := json.Unmarshal(raw, &reports); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(reports) != 2 || rec["decisions_total"] != 3 || rec["reports_total"] != 3 || rec["decisions_truncated"] != true || rec["reports_truncated"] != true || rows[0]["action_id"] != float64(third) || rows[1]["action_id"] != float64(second) || reports[0]["detail"] != "synthetic own report 2" {
		t.Fatal("limit2 exact newest/totals/truncation source contract")
	}
	full := privacy5Record(t, s, subject.ID)
	rows = privacy5Decisions(t, full)
	if len(rows) != 3 || rows[2]["action_id"] != float64(first) || full["decisions_total"] != 3 || full["reports_total"] != 3 || full["decisions_truncated"] != false || full["reports_truncated"] != false {
		t.Fatal("default reader changed complete record")
	}
	zero, err := s.Config.Accounts.SafetyRecord(ctx, subject.ID, 0)
	if err != nil || len(privacy5Decisions(t, zero)) != 0 || zero["decisions_total"] != 3 || zero["reports_total"] != 3 || zero["decisions_truncated"] != true || zero["reports_truncated"] != true {
		t.Fatal("limit0 erased honest totals", err)
	}
	for _, limits := range [][]int{{-1}, {1001}, {1, 2}} {
		if _, err := s.Config.Accounts.SafetyRecord(ctx, subject.ID, limits...); !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("invalid optional self-record limit accepted", limits, err)
		}
		if _, err := s.ActivityLog(ctx, subject.ID, limits...); !errors.Is(err, platform.ErrInvalid) {
			t.Fatal("invalid optional log limit accepted", limits, err)
		}
	}
	for i := 0; i < 5; i++ {
		if err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			return platform.RecordAudit(ctx, tx, subject, "user.blocked", "private.target", map[string]int{"private": i})
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
		return platform.RecordAudit(ctx, tx, other, "group.created", "private.other", nil)
	}); err != nil {
		t.Fatal(err)
	}
	rows, err = s.ActivityLog(ctx, subject.ID, 3)
	if err != nil || len(rows) != 3 {
		t.Fatal("activity log source limit3", err)
	}
	for _, row := range rows {
		if len(row) != 2 || row["label"] != "You blocked someone" {
			t.Fatal("limited log broadened subject/fields")
		}
	}
	if rows, err = s.ActivityLog(ctx, subject.ID, 0); err != nil || len(rows) != 0 {
		t.Fatal("limit0 log", err)
	}
	if rows, err = s.ActivityLog(ctx, subject.ID); err != nil || len(rows) != 5 {
		t.Fatal("default100 log", err)
	}
	if _, err := s.Config.Accounts.SafetyRecord(ctx, subject.ID, 1000); err != nil {
		t.Fatal("bounded upper reader limit", err)
	}
	if _, err := s.ActivityLog(ctx, subject.ID, 1000); err != nil {
		t.Fatal("bounded upper log limit", err)
	}
}

func TestPrivacyCasePort5AppealHTTPRequiresOwnershipModeratorAndPrivateSelfListing(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-appeal-private-moderator", true)
	subject := user(t, s, "privacy5-appeal-subject", false)
	other := user(t, s, "privacy5-appeal-other", false)
	action := privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "warn", Reason: "spam", Notes: "private moderation note"})
	out := request(s, other, "POST", "/api/safety/appeals/", fmt.Sprintf(`{"action_id":%d,"statement":"proxy contest"}`, action))
	if out.Code != 404 {
		t.Fatal("another subject's action must be indistinguishable404", out.Code)
	}
	var appeals int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM safety_moderationappeal`).Scan(&appeals); err != nil || appeals != 0 {
		t.Fatal("proxy appeal persisted", err)
	}
	out = request(s, subject, "POST", "/api/safety/appeals/", fmt.Sprintf(`{"action_id":%d,"statement":"not spam"}`, action))
	if out.Code != 201 {
		t.Fatal("own appeal API", out.Code)
	}
	var appeal int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM safety_moderationappeal WHERE action_id=$1 AND appellant_id=$2 AND statement='not spam'`, action, subject.ID).Scan(&appeal); err != nil {
		t.Fatal(err)
	}
	out = request(s, subject, "GET", "/api/safety/appeals/", "")
	var rows []map[string]any
	if err := json.Unmarshal(out.Body.Bytes(), &rows); err != nil || out.Code != 200 || len(rows) != 1 {
		t.Fatal("own appeal listing", out.Code, err)
	}
	if len(rows[0]) != 7 || strings.Contains(out.Body.String(), mod.Username) || strings.Contains(out.Body.String(), "private moderation note") || strings.Contains(out.Body.String(), "decided_by") {
		t.Fatal("private self appeal projection widened")
	}
	out = request(s, other, "GET", "/api/safety/appeals/", "")
	if out.Code != 200 || strings.TrimSpace(out.Body.String()) != "[]" {
		t.Fatal("another subject's appeal leaked")
	}
	out = request(s, mod, "GET", "/api/safety/moderation/appeals/?status=pending", "")
	if err := json.Unmarshal(out.Body.Bytes(), &rows); err != nil || out.Code != 200 || len(rows) != 1 {
		t.Fatal("moderator pending queue", out.Code, err)
	}
	if out = request(s, subject, "GET", "/api/safety/moderation/appeals/", ""); out.Code != 403 {
		t.Fatal("appellant accessed moderator appeal queue", out.Code)
	}
	if out = request(s, subject, "POST", fmt.Sprintf("/api/safety/moderation/appeals/%d/resolve/", appeal), `{"grant":true}`); out.Code != 403 {
		t.Fatal("appellant resolved own appeal", out.Code)
	}
	out = request(s, mod, "POST", fmt.Sprintf("/api/safety/moderation/appeals/%d/resolve/", appeal), `{"grant":true,"notes":"private decision notes"}`)
	if out.Code != 200 {
		t.Fatal("moderator resolution", out.Code)
	}
	out = request(s, subject, "GET", "/api/safety/appeals/", "")
	if strings.Contains(out.Body.String(), "private decision notes") || strings.Contains(out.Body.String(), mod.Username) {
		t.Fatal("decided appeal revealed private actor/note")
	}
	// The source resolve API case contests a real suspension and must reactivate it.
	suspension := privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "suspend", Reason: "spam"})
	contest, err := s.FileAppeal(ctx, subject, suspension, "please")
	if err != nil {
		t.Fatal(err)
	}
	if out := request(s, subject, "POST", fmt.Sprintf("/api/safety/moderation/appeals/%d/resolve/", contest), `{"grant":true}`); out.Code != 403 {
		t.Fatal("ordinary subject resolved suspension", out.Code)
	}
	if out := request(s, mod, "POST", fmt.Sprintf("/api/safety/moderation/appeals/%d/resolve/", contest), `{"grant":true}`); out.Code != 200 {
		t.Fatal("moderator suspension appeal API", out.Code)
	}
	var active bool
	if err := s.DB.QueryRow(ctx, `SELECT is_active FROM accounts_user WHERE id=$1`, subject.ID).Scan(&active); err != nil || !active {
		t.Fatal("granted API suspension appeal did not reactivate", err)
	}
}

func TestPrivacyCasePort5AppealOwnedRecordStatusAndUphold(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-uphold-mod", true)
	subject := user(t, s, "privacy5-uphold-subject", false)
	action := privacy5Action(t, s, mod, "user", subject.ID, ActionInput{Decision: "suspend", Reason: "spam"})
	rows := privacy5Decisions(t, privacy5Record(t, s, subject.ID))
	if len(rows) != 1 || rows[0]["action_id"] != float64(action) || rows[0]["can_appeal"] != true {
		t.Fatal("owned contest affordance/actionID")
	}
	appeal, err := s.FileAppeal(ctx, subject, action, "please")
	if err != nil {
		t.Fatal(err)
	}
	rows = privacy5Decisions(t, privacy5Record(t, s, subject.ID))
	if rows[0]["can_appeal"] != false || rows[0]["appeal_status_label"] == nil || rows[0]["appeal_status_label"] == "" {
		t.Fatal("pending appeal status/affordance")
	}
	if _, err := s.ResolveAppeal(ctx, mod, appeal, false, "decision stands"); err != nil {
		t.Fatal(err)
	}
	var active bool
	var status string
	if err := s.DB.QueryRow(ctx, `SELECT is_active,(SELECT status FROM safety_moderationappeal WHERE id=$2) FROM accounts_user WHERE id=$1`, subject.ID, appeal).Scan(&active, &status); err != nil || active || status != "upheld" {
		t.Fatal("uphold undid restriction", err)
	}
}

func TestPrivacyCasePort5AppealReversesContentAndPreservesAuthorWithdrawal(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		deleted    bool
	}{{"activity", "activity", false}, {"normal_post", "post", false}, {"withdrawn_post", "post", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s, soc := privacy5Fixture(t)
			ctx := context.Background()
			mod := user(t, s, "privacy5-content-appeal-mod", true)
			subject := user(t, s, "privacy5-content-appeal-author", false)
			activity := privacy5Activity(t, s, soc, subject)
			id := activity
			if tc.kind == "post" {
				id = privacy5Post(t, soc, subject, activity, "synthetic content contested")
			}
			action := privacy5Action(t, s, mod, tc.kind, id, ActionInput{Decision: "remove", Reason: "other"})
			var initiallyHidden bool
			if err := s.DB.QueryRow(ctx, "SELECT is_hidden FROM social_"+tc.kind+" WHERE id=$1", id).Scan(&initiallyHidden); err != nil || !initiallyHidden {
				t.Fatal("removal did not hide original content", err)
			}
			if tc.deleted {
				if _, err := soc.DeletePost(ctx, subject, id); err != nil {
					t.Fatal(err)
				}
			}
			appeal, err := s.FileAppeal(ctx, subject, action, "please reconsider")
			if err != nil {
				t.Fatal(err)
			}
			result, err := s.ResolveAppeal(ctx, mod, appeal, true, "")
			if err != nil || result.Reactivated || result.LeftHiddenAuthorDeleted != tc.deleted {
				t.Fatal("content reversal outcome", result, err)
			}
			var hidden bool
			if err := s.DB.QueryRow(ctx, "SELECT is_hidden FROM social_"+tc.kind+" WHERE id=$1", id).Scan(&hidden); err != nil || hidden != tc.deleted {
				t.Fatal("content reversal violated author decision", err)
			}
			var body string
			if err := s.DB.QueryRow(ctx, `SELECT body FROM notifications_notification WHERE recipient_id=$1 AND title='Your appeal succeeded' ORDER BY id DESC LIMIT 1`, subject.ID).Scan(&body); err != nil {
				t.Fatal(err)
			}
			if tc.deleted {
				if !strings.Contains(body, "message stays deleted") {
					t.Fatal("withdrawn content misleading appeal notice")
				}
			} else {
				if body != "We reviewed your contest of a moderation decision and reversed it. Any restriction from that decision has been removed." {
					t.Fatal("normal appeal notice changed")
				}
			}
		})
	}
}

func TestPrivacyCasePort5DeleteDuringPendingRemoveAppealAndAfterDecision(t *testing.T) {
	s, soc := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-delete-appeal-mod", true)
	subject := user(t, s, "privacy5-delete-appeal-subject", false)
	activity := privacy5Activity(t, s, soc, subject)
	post := privacy5Post(t, soc, subject, activity, "my words")
	action := privacy5Action(t, s, mod, "post", post, ActionInput{Decision: "remove", Reason: "other"})
	appeal, err := s.FileAppeal(ctx, subject, action, "contest removal")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := soc.DeletePost(ctx, subject, post); !errors.Is(err, platform.ErrForbidden) {
		t.Fatal("pending remove appeal permitted author delete", err)
	}
	var deleted bool
	if err := s.DB.QueryRow(ctx, `SELECT is_author_deleted FROM social_post WHERE id=$1`, post).Scan(&deleted); err != nil || deleted {
		t.Fatal("refused delete mutated provenance", err)
	}
	var pending string
	if err := s.DB.QueryRow(ctx, `SELECT status FROM safety_moderationappeal WHERE id=$1`, appeal).Scan(&pending); err != nil || pending != "pending" {
		t.Fatal("refused withdrawal altered pending appeal", err)
	}
	if _, err := s.ResolveAppeal(ctx, mod, appeal, false, ""); err != nil {
		t.Fatal(err)
	}
	if wasHidden, err := soc.DeletePost(ctx, subject, post); err != nil || !wasHidden {
		t.Fatal("decided appeal still blocks withdrawal", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT is_author_deleted FROM social_post WHERE id=$1`, post).Scan(&deleted); err != nil || !deleted {
		t.Fatal("decided appeal withdrawal missing", err)
	}
	// A pending warning appeal is unrelated to restoration and never blocks deletion.
	fresh := privacy5Post(t, soc, subject, activity, "warning words")
	warn := privacy5Action(t, s, mod, "post", fresh, ActionInput{Decision: "warn", Reason: "spam"})
	privacy5Action(t, s, mod, "post", fresh, ActionInput{Decision: "remove", Reason: "other"})
	if _, err := s.FileAppeal(ctx, subject, warn, "contest warning"); err != nil {
		t.Fatal(err)
	}
	if wasHidden, err := soc.DeletePost(ctx, subject, fresh); err != nil || !wasHidden {
		t.Fatal("pending warning blocked unrelated self delete", err)
	}
	var hidden bool
	if err := s.DB.QueryRow(ctx, `SELECT is_hidden,is_author_deleted FROM social_post WHERE id=$1`, fresh).Scan(&hidden, &deleted); err != nil || !hidden || !deleted {
		t.Fatal("pending warning hid author withdrawal provenance", err)
	}
	// Granted removal: restored content takes the ordinary live deletion path.
	granted := privacy5Post(t, soc, subject, activity, "granted removal words")
	removal := privacy5Action(t, s, mod, "post", granted, ActionInput{Decision: "remove", Reason: "other"})
	contest, err := s.FileAppeal(ctx, subject, removal, "wrong")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolveAppeal(ctx, mod, contest, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT is_hidden FROM social_post WHERE id=$1`, granted).Scan(&hidden); err != nil || hidden {
		t.Fatal("grant did not restore content", err)
	}
	if wasHidden, err := soc.DeletePost(ctx, subject, granted); err != nil || wasHidden {
		t.Fatal("granted appeal ordinary withdrawal mislabeled", err)
	}
	if err := s.DB.QueryRow(ctx, `SELECT is_hidden,is_author_deleted FROM social_post WHERE id=$1`, granted).Scan(&hidden, &deleted); err != nil || !hidden || !deleted {
		t.Fatal("granted appeal subsequent withdrawal missing", err)
	}
}

func TestPrivacyCasePort5ReadersKeepExactDefault50And100Caps(t *testing.T) {
	s, _ := privacy5Fixture(t)
	ctx := context.Background()
	mod := user(t, s, "privacy5-default-cap-mod", true)
	subject := user(t, s, "privacy5-default-cap-subject", false)
	var ct int64
	if err := s.DB.QueryRow(ctx, `SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'`).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_moderationaction(target_id,action,reason,notes,created_at,moderator_id,target_type_id) SELECT $1,'warn','spam','',now()+i*interval '1 millisecond',$2,$3 FROM generate_series(1,55) i`, subject.ID, mod.ID, ct); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(ctx, `INSERT INTO safety_report(target_type_id,target_id,reason,detail,status,resolution,created_at,reporter_id) SELECT $1,$2,'spam','synthetic report '||i,'open','',now()+i*interval '1 millisecond',$2 FROM generate_series(1,55) i`, ct, subject.ID); err != nil {
		t.Fatal(err)
	}
	rec := privacy5Record(t, s, subject.ID)
	rows := privacy5Decisions(t, rec)
	raw, _ := json.Marshal(rec["reports"])
	var reports []map[string]any
	if err := json.Unmarshal(raw, &reports); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 50 || len(reports) != 50 || rec["decisions_total"] != 55 || rec["reports_total"] != 55 || rec["decisions_truncated"] != true || rec["reports_truncated"] != true || reports[0]["detail"] != "synthetic report 55" {
		t.Fatal("default50 truthful cap changed")
	}
	for i := 0; i < 105; i++ {
		if err := platform.Transaction(ctx, s.DB, func(tx pgx.Tx) error {
			return platform.RecordAudit(ctx, tx, subject, "user.blocked", "private.ref", nil)
		}); err != nil {
			t.Fatal(err)
		}
	}
	log, err := s.ActivityLog(ctx, subject.ID)
	if err != nil || len(log) != 100 {
		t.Fatal("default100 log cap changed", err)
	}
	for i := 1; i < len(log); i++ {
		if log[i-1]["when"].(string) < log[i]["when"].(string) {
			t.Fatal("activity log newest-first ordering changed")
		}
	}
}
