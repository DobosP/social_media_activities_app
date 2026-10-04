package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
)

func TestPostgresCatalogRatePoliciesRetainDuplicateAndFailedReports(t *testing.T) {
	s := fixture(t, true)
	ctx := context.Background()
	s.RatePolicies = map[string]budgets.Policy{
		"open_now_report":      {Limit: 2, Window: 2 * time.Minute},
		"place_closure_report": {Limit: 1, Window: 3 * time.Minute},
		"event_report":         {Limit: 2, Window: 4 * time.Minute},
	}
	replica := New(s.DB)
	replica.RatePolicies = s.RatePolicies
	a := user(t, s, "rate-reporter", "adult")
	first := placeFixture(t, s, "First rate venue", "osm", 23.6, 46.77)
	second := placeFixture(t, s, "Second rate venue", "osm", 23.7, 46.78)
	if created, err := s.ReportVenue(ctx, a, first, false); !created || err != nil {
		t.Fatalf("initial venue report failed: created=%v err=%v", created, err)
	}
	if created, err := replica.ReportVenue(ctx, a, first, false); created || err != nil {
		t.Fatalf("duplicate venue report changed product state: created=%v err=%v", created, err)
	}
	if created, err := replica.ReportVenue(ctx, a, second, false); created || err != nil {
		t.Fatalf("duplicate report refunded the shared token: created=%v err=%v", created, err)
	}
	assertCatalogRatePolicy(t, s, a.ID, "catalog.open_now_report", 2, 2*time.Minute, 2)
	// A missing venue fails its FK after admission. The following replica's
	// valid closure report must see the independently committed debit.
	if _, err := s.ReportVenue(ctx, a, 9223372036854775000, true); err == nil {
		t.Fatal("missing venue report did not fail")
	}
	if created, err := replica.ReportVenue(ctx, a, second, true); created || err != nil {
		t.Fatalf("failed report refunded the shared token: created=%v err=%v", created, err)
	}
	assertCatalogRatePolicy(t, s, a.ID, "catalog.place_closure_report", 1, 3*time.Minute, 1)
	if err := replica.VoteFact(ctx, a, second, "drinking_water", true); err != nil {
		t.Fatal(err)
	}
	assertCatalogRatePolicy(t, s, a.ID, "catalog.place_fact_vote", 40, time.Hour, 1)
	firstEvent := eventFixture(t, s, "First rate event", "scheduled", &first, time.Now().Add(time.Hour))
	secondEvent := eventFixture(t, s, "Second rate event", "scheduled", &second, time.Now().Add(time.Hour))
	if created, err := s.ReportEvent(ctx, a, firstEvent, "wrong_time"); !created || err != nil {
		t.Fatalf("initial event report failed: created=%v err=%v", created, err)
	}
	if created, err := replica.ReportEvent(ctx, a, firstEvent, "wrong_time"); created || err != nil {
		t.Fatalf("duplicate event report changed product state: created=%v err=%v", created, err)
	}
	if created, err := replica.ReportEvent(ctx, a, secondEvent, "moved"); created || err != nil {
		t.Fatalf("replicas did not share event report budget: created=%v err=%v", created, err)
	}
	assertCatalogRatePolicy(t, s, a.ID, "catalog.event_report", 2, 4*time.Minute, 2)
	invalidActor := user(t, s, "invalid-rate-reporter", "adult")
	s.RatePolicies["open_now_report"] = budgets.Policy{}
	if created, err := s.ReportVenue(ctx, invalidActor, first, false); created || err == nil {
		t.Fatalf("explicit invalid policy silently used default: created=%v err=%v", created, err)
	}
	var invalidDebits int
	if err := s.DB.QueryRow(ctx, `SELECT count(*) FROM go_rate_budget WHERE user_id=$1`, invalidActor.ID).Scan(&invalidDebits); err != nil || invalidDebits != 0 {
		t.Fatalf("invalid report policy created budget state: count=%d err=%v", invalidDebits, err)
	}
}

func assertCatalogRatePolicy(t *testing.T, s *Service, actor int64, scope string, limit int, window time.Duration, events int) {
	t.Helper()
	var actualLimit, actualEvents int
	var actualWindow int64
	if err := s.DB.QueryRow(context.Background(), `SELECT policy_limit,window_us,cardinality(events) FROM go_rate_budget WHERE user_id=$1 AND scope=$2`, actor, scope).Scan(&actualLimit, &actualWindow, &actualEvents); err != nil {
		t.Fatal(err)
	}
	if actualLimit != limit || actualWindow != window.Microseconds() || actualEvents != events {
		t.Fatalf("%s retained limit=%d window_us=%d events=%d", scope, actualLimit, actualWindow, actualEvents)
	}
}
