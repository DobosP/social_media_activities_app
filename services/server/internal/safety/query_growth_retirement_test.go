package safety

import (
	"context"
	"testing"

	"github.com/DobosP/social_media_activities_app/services/server/internal/accounts"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
)

func TestRetirementPostgresModerationTriageQueryGrowth(t *testing.T) {
	s := safetyFixture(t)
	db, trace := testdb.TracedPool(t, s.DB)
	s.DB = db
	ctx := context.Background()
	moderator := user(t, s, "growth-triage-moderator", true)
	reporter := user(t, s, "growth-triage-reporter", false)
	var contentType int64
	if err := db.QueryRow(ctx, `SELECT id FROM django_content_type WHERE app_label='accounts' AND model='user'`).Scan(&contentType); err != nil {
		t.Fatal(err)
	}
	seed := func(begin, end int) {
		if _, err := db.Exec(ctx, `INSERT INTO safety_report(reporter_id,target_type_id,target_id,reason,detail,status,handled_by_id,handled_at,resolution,created_at) SELECT $1,$2,$1,CASE WHEN i%2=0 THEN 'harassment' ELSE 'grooming' END,'Synthetic incident details','open',NULL,NULL,'',now()+i*interval '1 millisecond' FROM generate_series($3::int,$4::int) i`, reporter.ID, contentType, begin, end-1); err != nil {
			t.Fatal(err)
		}
	}
	read := func(n int) int64 {
		trace.Reset()
		rows, err := s.ReportQueue(ctx, moderator, "open")
		if err != nil || len(rows) != n {
			t.Fatal("triage fixture incomplete", err)
		}
		count := trace.Count()
		for _, row := range rows {
			triage, ok := row["triage"].(map[string]any)
			if !ok || triage["open_duplicates"] != n {
				t.Fatal("incident-local triage summary missing")
			}
		}
		return count
	}
	seed(0, 4)
	small := read(4)
	account := accounts.New(db, nil, "synthetic-record-growth", accounts.Config{})
	trace.Reset()
	if record, err := account.SafetyRecord(ctx, reporter.ID); err != nil || record["reports_total"] != 4 {
		t.Fatal("subject record fixture incomplete", err)
	}
	smallRecord := trace.Count()
	seed(4, 28)
	large := read(28)
	trace.Reset()
	if record, err := account.SafetyRecord(ctx, reporter.ID); err != nil || record["reports_total"] != 28 {
		t.Fatal("subject record fixture incomplete", err)
	}
	largeRecord := trace.Count()
	if small == 0 || large > small+1 {
		t.Fatalf("triage introduced per-report queries: %d -> %d", small, large)
	}
	if smallRecord == 0 || largeRecord != smallRecord {
		t.Fatalf("subject record introduced per-report queries: %d -> %d", smallRecord, largeRecord)
	}
}
