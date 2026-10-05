package catalog

import (
	"context"
	"testing"
	"time"
)

// GO-CATALOG-01 residual. Owner rule (2026-10-05): never refuse new users
// because a table is full. A full actor family evicts its own soonest-expiring
// keys, so a brand-new user's crowd report is still admitted.
func TestPostgresFullActorFamilyStillAdmitsNewUsersEventReport(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	filler := user(t, s, "synthetic-family-filler", "adult")
	// The actor family's real seeded limit: 50,000 live keys.
	if _, err := s.DB.Exec(ctx, `INSERT INTO go_rate_budget(scope,subject,user_id,policy_limit,window_us,events,expires_at) SELECT 'catalog.event_report',decode(md5(i::text)||md5('filler'||i::text),'hex'),$1::bigint,10,3600000000,ARRAY[clock_timestamp()],now()+interval '30 minutes'+i*interval '1 millisecond' FROM generate_series(1,50000) i`, filler.ID); err != nil {
		t.Fatal(err)
	}
	var keys, maxKeys int
	if err := s.DB.QueryRow(ctx, `SELECT keys,max_keys FROM go_rate_budget_family_capacity WHERE family='actor'`).Scan(&keys, &maxKeys); err != nil || keys != maxKeys {
		t.Fatalf("actor family not full: keys=%d max=%d err=%v", keys, maxKeys, err)
	}
	place := placeFixture(t, s, "Family capacity venue", "osm", 23.6, 46.77)
	event := eventFixture(t, s, "Family capacity event", "scheduled", &place, time.Now().Add(time.Hour))
	reporter := user(t, s, "synthetic-new-reporter", "adult")
	if created, err := s.ReportEvent(ctx, reporter, event, "moved"); !created || err != nil {
		t.Fatalf("full actor family refused a new user's report: created=%v err=%v", created, err)
	}
	var reporterRows, fillerRows int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FILTER(WHERE user_id=$1),count(*) FILTER(WHERE user_id=$2) FROM go_rate_budget`, reporter.ID, filler.ID).Scan(&reporterRows, &fillerRows); err != nil {
		t.Fatal(err)
	}
	if reporterRows != 1 || fillerRows != 50000-64 {
		t.Fatalf("eviction: reporter=%d filler=%d", reporterRows, fillerRows)
	}
}
