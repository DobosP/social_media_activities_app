package catalog_test

import (
	"context"
	"flag"
	"testing"
	"time"

	"github.com/DobosP/social_media_activities_app/services/server/internal/catalog"
	"github.com/DobosP/social_media_activities_app/services/server/internal/platform"
	"github.com/DobosP/social_media_activities_app/services/server/internal/social"
	"github.com/DobosP/social_media_activities_app/services/server/internal/testdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCasePortPostgresClosedVenueRefusesActivityAfterSubquorumWasCreatable(t *testing.T) {
	dsn := flag.Lookup("catalog-test-dsn")
	if dsn == nil {
		t.Fatal("fixture flag missing")
	}
	db := testdb.New(t, dsn.Value.String(), func(ctx context.Context, db *pgxpool.Pool) error { return catalog.New(db).Migrate(ctx) })
	ctx := context.Background()
	c := catalog.New(db)
	policy := catalog.DefaultPolicy()
	policy.ClosureReportThreshold = 2
	c.Policy = policy
	ctx = catalog.WithPolicy(ctx, policy)
	place := testdb.Place(t, db, "Closure write venue", "osm")
	owner := testdb.Actor(t, db, "closure-write-owner", "adult")
	peer := testdb.Actor(t, db, "closure-write-peer", "adult")
	var typ int64
	if err := db.QueryRow(ctx, `SELECT id FROM taxonomy_activitytype WHERE slug='basketball'`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReportVenue(ctx, owner, place, true); err != nil {
		t.Fatal(err)
	}
	s := social.New(db, platform.RecordAudit)
	in := social.ActivityInput{Place: place, ActivityType: typ, Title: "Before closure quorum", StartsAt: time.Now().Add(24 * time.Hour)}
	id, err := s.CreateActivity(ctx, owner, in)
	if err != nil || id < 1 {
		t.Fatal("belowquorum creation failed", err)
	}
	var storedPlace int64
	if err = db.QueryRow(ctx, `SELECT place_id FROM social_activity WHERE id=$1`, id).Scan(&storedPlace); err != nil || storedPlace != place {
		t.Fatal("created activity lost original place", err)
	}
	if _, err = c.ReportVenue(ctx, peer, place, true); err != nil {
		t.Fatal(err)
	}
	in.Title = "After closure quorum"
	if _, err = s.CreateActivity(ctx, owner, in); err == nil {
		t.Fatal("closed venue remained creatable")
	}
	var count int
	if err = db.QueryRow(ctx, `SELECT count(*) FROM social_activity WHERE title=$1`, in.Title).Scan(&count); err != nil || count != 0 {
		t.Fatal("refused activity persisted", err)
	}
}
