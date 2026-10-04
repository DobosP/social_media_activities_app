package recommendations

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/budgets"
	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresSavedSearchRatePoliciesRetainDuplicateAndFailedAdmissions(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t, *recommendationDSN, func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	cat := catalog.New(db)
	soc := social.New(db, platform.RecordAudit)
	s := New(db, cat, soc)
	s.RatePolicies = map[string]budgets.Policy{"saved_search_create": {Limit: 2, Window: 3 * time.Minute}}
	replica := New(db, cat, soc)
	replica.RatePolicies = s.RatePolicies
	a := testdb.Actor(t, db, "rate-search-owner", "adult")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	in := SavedSearchInput{ActivityType: &typ, City: "Cluj-Napoca"}
	if _, err := s.CreateSavedSearch(ctx, a, in); err != nil {
		t.Fatal(err)
	}
	if _, err := replica.CreateSavedSearch(ctx, a, in); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("duplicate saved search did not reject: %v", err)
	}
	if _, err := replica.CreateSavedSearch(ctx, a, SavedSearchInput{ActivityType: &typ, City: "Duplicate budget city"}); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("duplicate saved search refunded its shared debit: %v", err)
	}
	assertSavedSearchRatePolicy(t, s, a.ID, 2, 3*time.Minute, 2)
	var searches, deniedAreas int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM saved_searches_savedsearch WHERE user_id=$1`, a.ID).Scan(&searches); err != nil || searches != 1 {
		t.Fatalf("denial changed saved searches: count=%d err=%v", searches, err)
	}
	if err := db.QueryRow(ctx, `SELECT count(*) FROM communities_area WHERE city='Duplicate budget city'`).Scan(&deniedAreas); err != nil || deniedAreas != 0 {
		t.Fatalf("denial minted an area: count=%d err=%v", deniedAreas, err)
	}
	defaultActor := testdb.Actor(t, db, "default-rate-search-owner", "adult")
	defaultService := New(db, cat, soc)
	if _, err := defaultService.CreateSavedSearch(ctx, defaultActor, in); err != nil {
		t.Fatal(err)
	}
	assertSavedSearchRatePolicy(t, s, defaultActor.ID, 20, time.Hour, 1)
	invalidActor := testdb.Actor(t, db, "invalid-rate-search-owner", "adult")
	invalidService := New(db, cat, soc)
	invalidService.RatePolicies = map[string]budgets.Policy{"saved_search_create": {}}
	if _, err := invalidService.CreateSavedSearch(ctx, invalidActor, SavedSearchInput{ActivityType: &typ, City: "Invalid policy city"}); err == nil || errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("explicit invalid policy silently used the default: %v", err)
	}
	var invalidDebits int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM go_rate_budget WHERE user_id=$1`, invalidActor.ID).Scan(&invalidDebits); err != nil || invalidDebits != 0 {
		t.Fatalf("invalid policy created budget state: count=%d err=%v", invalidDebits, err)
	}
	failedActor := testdb.Actor(t, db, "hardcap-rate-search-owner", "adult")
	// Seed the existing product hard cap independently of its creation budget.
	// The actual create flow must debit before discovering this domain failure.
	if _, err := db.Exec(ctx, `INSERT INTO saved_searches_savedsearch(user_id,cohort,activity_type_id,category_id,area_id,beginners,cost_band,coarse_window,created_at) SELECT $1,'adult',$2,NULL,NULL,false,'','',now() FROM generate_series(1,20)`, failedActor.ID, typ); err != nil {
		t.Fatal(err)
	}
	failedService := New(db, cat, soc)
	failedService.RatePolicies = map[string]budgets.Policy{"saved_search_create": {Limit: 1, Window: 2 * time.Minute}}
	failedReplica := New(db, cat, soc)
	failedReplica.RatePolicies = failedService.RatePolicies
	if _, err := failedService.CreateSavedSearch(ctx, failedActor, SavedSearchInput{ActivityType: &typ, City: "Hard cap city"}); !errors.Is(err, platform.ErrInvalid) {
		t.Fatalf("product hard cap did not reject: %v", err)
	}
	if _, err := failedReplica.CreateSavedSearch(ctx, failedActor, SavedSearchInput{ActivityType: &typ, City: "Another hard cap city"}); !errors.Is(err, platform.ErrForbidden) {
		t.Fatalf("rolled-back domain failure refunded its shared debit: %v", err)
	}
	assertSavedSearchRatePolicy(t, s, failedActor.ID, 1, 2*time.Minute, 1)
}

func assertSavedSearchRatePolicy(t *testing.T, s *Service, actor int64, limit int, window time.Duration, events int) {
	t.Helper()
	var actualLimit, actualEvents int
	var actualWindow int64
	if err := s.DB.QueryRow(context.Background(), `SELECT policy_limit,window_us,cardinality(events) FROM go_rate_budget WHERE user_id=$1 AND scope='recommendations.saved_search_create'`, actor).Scan(&actualLimit, &actualWindow, &actualEvents); err != nil {
		t.Fatal(err)
	}
	if actualLimit != limit || actualWindow != window.Microseconds() || actualEvents != events {
		t.Fatalf("saved search retained limit=%d window_us=%d events=%d", actualLimit, actualWindow, actualEvents)
	}
}
